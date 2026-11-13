package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// LogQuery selects log records for one confirmed capture.
type LogQuery struct {
	From            time.Time
	Until           time.Time
	DeviceKey       string
	SessionKeys     []string
	AttemptKeys     []string
	MaxLines        int
	IncludeWarnings bool
	WarningFrom     time.Time
}

// LogRecord is one structured log row. Message is free text and is not exported.
type LogRecord struct {
	Time    time.Time
	Level   string
	Message string
	Attrs   map[string]string
}

// Alias is a stable pseudonym. The same raw id always maps to the same
// alias, and different ids do not share one.
func Alias(scope, raw string) string {
	if raw == "" {
		return ""
	}
	if scope == "device" {
		return devicePublicID(raw)
	}
	sum := sha256.Sum256([]byte("ether-support-alias:" + scope + ":" + raw))
	return scope + ":" + hex.EncodeToString(sum[:8])
}

// LogCorrelated reports whether rec belongs to this capture by an exact
// correlation field inside the capture window. A shorter id is not a match
// for a longer one, and free text is not searched.
func LogCorrelated(rec LogRecord, q LogQuery) bool {
	if rec.Time.IsZero() || rec.Time.Before(q.From) || rec.Time.After(q.Until) {
		return false
	}
	if q.IncludeWarnings && !rec.Time.Before(q.WarningFrom) && (strings.EqualFold(rec.Level, "WARN") || strings.EqualFold(rec.Level, "ERROR")) {
		return true
	}
	if exactAttr(rec.Attrs, deviceAttrNames, q.DeviceKey) {
		return true
	}
	for _, key := range q.SessionKeys {
		if exactAttr(rec.Attrs, sessionAttrNames, key) {
			return true
		}
	}
	for _, key := range q.AttemptKeys {
		if exactAttr(rec.Attrs, attemptAttrNames, key) {
			return true
		}
	}
	return false
}

var (
	deviceAttrNames  = []string{"client", "clientid", "client_id", "device", "devicekey", "device_key"}
	sessionAttrNames = []string{"session", "sessionid", "session_id", "sessionkey"}
	attemptAttrNames = []string{"attempt", "attemptkey", "attempt_id"}
)

func exactAttr(attrs map[string]string, names []string, want string) bool {
	if want == "" || len(attrs) == 0 {
		return false
	}
	for k, v := range attrs {
		if v != want {
			continue
		}
		lk := strings.ToLower(k)
		for _, name := range names {
			if lk == name {
				return true
			}
		}
	}
	return false
}

// ParseLogLine reads a slog-style line into structured fields.
// It returns false when the line has no key=value fields.
func ParseLogLine(line string) (LogRecord, bool) {
	attrs := parseKV(line)
	if len(attrs) == 0 {
		return LogRecord{}, false
	}
	rec := LogRecord{Attrs: attrs, Level: attrFold(attrs, "level"), Message: attrFold(attrs, "msg")}
	if raw := attrFold(attrs, "time"); raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			rec.Time = t
		} else if t, err := time.Parse(time.RFC3339, raw); err == nil {
			rec.Time = t
		}
	}
	return rec, true
}

func attrFold(attrs map[string]string, name string) string {
	for k, v := range attrs {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func parseKV(line string) map[string]string {
	out := map[string]string{}
	i := 0
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		eq := strings.IndexByte(line[i:], '=')
		if eq <= 0 {
			break
		}
		key := line[i : i+eq]
		if strings.ContainsAny(key, " \t") {
			break
		}
		i += eq + 1
		if i >= len(line) {
			out[key] = ""
			break
		}
		if line[i] == '"' {
			var b strings.Builder
			i++
			for i < len(line) {
				if line[i] == '\\' && i+1 < len(line) {
					b.WriteByte(line[i+1])
					i += 2
					continue
				}
				if line[i] == '"' {
					i++
					break
				}
				b.WriteByte(line[i])
				i++
			}
			out[key] = b.String()
			continue
		}
		j := i
		for j < len(line) && line[j] != ' ' && line[j] != '\t' {
			j++
		}
		out[key] = line[i:j]
		i = j
	}
	return out
}

type exportLog struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level,omitempty"`
	Device  string    `json:"device,omitempty"`
	Session string    `json:"session,omitempty"`
	Attempt string    `json:"attempt,omitempty"`
	Status  string    `json:"status,omitempty"`
	Kind    string    `json:"kind,omitempty"`
	// Method, Route and Dur describe a request line: which endpoint the
	// device asked for and how long it took, with identifying segments masked.
	// Msg is the log line's message, a fixed string in Ether's code. Err
	// is its error or reason, secrets and URL queries removed; it often
	// names a release, so it is kept only when titles are included.
	Msg       string `json:"msg,omitempty"`
	Err       string `json:"err,omitempty"`
	Method    string `json:"method,omitempty"`
	Route     string `json:"route,omitempty"`
	Dur       string `json:"dur,omitempty"`
	PageStart string `json:"pageStart,omitempty"`
	PageSize  string `json:"pageSize,omitempty"`
	Username  string `json:"username,omitempty"`
	Peer      string `json:"peer,omitempty"`
	Title     string `json:"title,omitempty"`
}

type logIdentity struct {
	users  []string
	peers  []string
	titles []string
}

// projectLogs writes one JSON object per correlated record. Free text,
// paths, and fields the owner did not opt into are left out.
func projectLogs(records []LogRecord, q LogQuery, flags [3]bool, uses []secretUse) (body []byte, ident logIdentity, truncated bool, gaps []Gap) {
	gaps = append(gaps, Gap{
		Collector: "logs",
		Status:    "omitted",
		Detail:    "Debug lines are included. Free-text log messages are kept only as fixed phrases; file paths and unrecognized fields were omitted. Request routes are kept with identifying parts masked, and error text only when titles are included. Usernames, addresses, and titles are included only when the summary lists them.",
	})
	limit := q.MaxLines
	if limit <= 0 || limit > maxLogLines {
		limit = maxLogLines
	}
	var b strings.Builder
	n := 0
	for _, rec := range records {
		if !LogCorrelated(rec, q) {
			continue
		}
		if n >= limit || b.Len() >= maxLogBytes {
			truncated = true
			break
		}
		// Uncorrelated warnings have no device identity. Retain only a known
		// performance or playback phrase and safe numeric fields, never their free text.
		onlyCorrelated := q
		onlyCorrelated.IncludeWarnings = false
		if !LogCorrelated(rec, onlyCorrelated) {
			row := exportLog{At: rec.Time.UTC(), Level: boundToken(rec.Level), Msg: playbackWarningPhrase(rec.Message)}
			if rec.Message == "request over performance budget" {
				row.Msg = "request over performance budget"
				row.Route = redactNetworkText(requestRoute(attrFold(rec.Attrs, "route")))
				row.Method = boundToken(attrFold(rec.Attrs, "method"))
				row.Dur = boundToken(attrFold(rec.Attrs, "duration"))
			}
			line, err := marshalChecked(row, uses)
			if err == nil && b.Len()+len(line)+1 <= maxLogBytes {
				b.Write(line)
				b.WriteByte('\n')
				n++
			}
			continue
		}
		row := exportLog{At: rec.Time.UTC(), Level: boundToken(rec.Level)}
		if key := exactValue(rec.Attrs, deviceAttrNames, q.DeviceKey); key != "" {
			row.Device = Alias("device", key)
		}
		if key := firstExact(rec.Attrs, sessionAttrNames, q.SessionKeys); key != "" {
			row.Session = Alias("session", key)
		}
		if key := firstExact(rec.Attrs, attemptAttrNames, q.AttemptKeys); key != "" {
			row.Attempt = Alias("attempt", key)
		}
		row.Status = boundToken(attrFold(rec.Attrs, "status"))
		row.Kind = boundToken(attrFold(rec.Attrs, "kind"))
		if len(rec.Message) <= 120 {
			row.Msg = plainMessage(clipNetwork(rec.Message, 120, uses))
		}
		if flags[0] {
			if e := firstPresent(rec.Attrs, "err", "error", "reason"); e != "" {
				row.Err = clipNetwork(e, 400, uses)
			}
		}
		if rec.Message == "request" || rec.Message == "hls request" {
			row.Method = boundToken(attrFold(rec.Attrs, "method"))
			row.Route = redactNetworkText(requestRoute(attrFold(rec.Attrs, "path")))
			row.Dur = boundToken(attrFold(rec.Attrs, "dur"))
			row.PageStart = digitsOnly(attrFold(rec.Attrs, "pageStart"))
			row.PageSize = digitsOnly(attrFold(rec.Attrs, "pageSize"))
		}
		if flags[1] {
			if u := firstPresent(rec.Attrs, "user", "username"); u != "" {
				u = clipNetwork(u, 80, uses)
				row.Username = u
				ident.users = append(ident.users, u)
			}
		}
		if flags[2] {
			if p := firstPresent(rec.Attrs, "peer", "ip", "remote", "addr"); p != "" {
				p = redactString(p, uses)
				row.Peer = p
				ident.peers = append(ident.peers, p)
			}
		}
		if flags[0] {
			if title := firstPresent(rec.Attrs, "title", "release", "source"); title != "" {
				title = clipNetwork(title, maxString, uses)
				row.Title = title
				ident.titles = append(ident.titles, title)
			}
		}
		line, err := marshalChecked(row, uses)
		if err != nil {
			gaps = append(gaps, Gap{Collector: "logs/capture.log", Status: "omitted", Detail: "A log record was omitted because the redaction check did not accept it."})
			continue
		}
		if b.Len()+len(line)+1 > maxLogBytes {
			truncated = true
			break
		}
		b.Write(line)
		b.WriteByte('\n')
		n++
	}
	if truncated {
		gaps = append(gaps, Gap{Collector: "logs", Status: "truncated", Detail: "The log extract was shortened to the line limit. No line was cut in the middle."})
	}
	return []byte(b.String()), ident, truncated, gaps
}

// playbackWarningPhrase deliberately accepts only exact, code-owned messages.
// Uncorrelated warnings may belong to another viewer: never export their
// attributes, stderr, paths, or dynamically constructed error text.
func playbackWarningPhrase(message string) string {
	switch message {
	case "ffmpeg failed",
		"unreadable DASH init segment",
		"assemble DASH segment",
		"DASH run output unusable",
		"HLS run output unusable",
		"using growing DASH manifest",
		"hardware encoder failed; falling back to CPU",
		"live hardware encoder failed; falling back to CPU",
		"copy packet conversion failed; encoding video",
		"transcode graph rejected",
		"stalled encoder; restarting",
		"timed out waiting for segment",
		"segment unavailable after empty restarts",
		"segment unavailable after restarts",
		"start DASH playback failed":
		return message
	default:
		return "server warning or error (details omitted)"
	}
}

// plainMessage keeps a log message that reads as a fixed phrase: no paths,
// addresses, key=value pairs or long runs.
func plainMessage(m string) string {
	if m == "" || len(m) > 120 || strings.ContainsAny(m, "/\\@=?&\n") {
		return ""
	}
	return m
}

// requestRoute keeps the shape of an HTTP request path, such as
// /hubs/promoted or /library/metadata/123/children. A segment is kept only
// when each of its dot, colon, dash or underscore separated parts is a
// plain word or all digits; anything else (GUIDs, tokens, encoded addon
// configuration, file names) becomes "*".
func requestRoute(p string) string {
	if !strings.HasPrefix(p, "/") || len(p) > 512 {
		return ""
	}
	segs := strings.Split(p[1:], "/")
	for i, seg := range segs {
		if !plainSegment(seg) {
			segs[i] = "*"
		}
	}
	return "/" + strings.Join(segs, "/")
}

func plainSegment(seg string) bool {
	if seg == "" || len(seg) > 64 {
		return seg == ""
	}
	for _, part := range strings.FieldsFunc(seg, func(r rune) bool { return r == '.' || r == ':' || r == '-' || r == '_' }) {
		letters, upper, digits := 0, 0, 0
		for _, r := range part {
			switch {
			case r >= 'a' && r <= 'z':
				letters++
			case r >= 'A' && r <= 'Z':
				letters++
				upper++
			case r >= '0' && r <= '9':
				digits++
			default:
				return false
			}
		}
		// Words and camelCase names pass; encoded blobs (many capitals,
		// long runs) and mixed letter-digit ids do not.
		if letters > 0 && (digits > 0 || upper > 3 || letters > 32) {
			return false
		}
	}
	return true
}

func digitsOnly(s string) string {
	if s == "" || len(s) > 12 || strings.Trim(s, "0123456789") != "" {
		return ""
	}
	return s
}

func boundToken(s string) string {
	if s == "" || len(s) > 40 || strings.ContainsAny(s, " /\\\t\"'") {
		return ""
	}
	return s
}

func exactValue(attrs map[string]string, names []string, want string) string {
	if exactAttr(attrs, names, want) {
		return want
	}
	return ""
}

func firstExact(attrs map[string]string, names []string, wants []string) string {
	for _, want := range wants {
		if exactAttr(attrs, names, want) {
			return want
		}
	}
	return ""
}

func firstPresent(attrs map[string]string, names ...string) string {
	for _, name := range names {
		if v := attrFold(attrs, name); v != "" {
			return v
		}
	}
	return ""
}

func logQuery(snap finishSnap) LogQuery {
	q := LogQuery{
		From:            snap.started.Add(-preRoll),
		Until:           snap.finished,
		DeviceKey:       snap.deviceKey,
		MaxLines:        maxLogLines,
		IncludeWarnings: true,
		WarningFrom:     snap.started,
	}
	seenS := map[string]struct{}{}
	seenA := map[string]struct{}{}
	for _, e := range snap.events {
		if e.SessionKey != "" {
			if _, ok := seenS[e.SessionKey]; !ok {
				seenS[e.SessionKey] = struct{}{}
				q.SessionKeys = append(q.SessionKeys, e.SessionKey)
			}
		}
		if e.AttemptKey != "" {
			if _, ok := seenA[e.AttemptKey]; !ok {
				seenA[e.AttemptKey] = struct{}{}
				q.AttemptKeys = append(q.AttemptKeys, e.AttemptKey)
			}
		}
	}
	return q
}
