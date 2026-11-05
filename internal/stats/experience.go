package stats

import (
	"cmp"
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Activity › Experience: what each viewer actually saw. Aggregation reads
// one bounded window of stats_plays; nothing runs on a playback path.

const (
	measuredSinceKind = "experience-measured-since"
	// experienceRowCap bounds one window read (a household's plays in 30
	// days are far fewer).
	experienceRowCap = 20000
	// abandonWindow: a play left within it counts as abandoned.
	abandonWindow  = 2 * time.Minute
	maxReleaseName = 300
)

// playMeasure is the experience columns of one play record.
type playMeasure struct {
	played, pictured, startFailed int
	ttffMS                        int64
	bufferEvents                  int
	bufferMS                      int64
	sourceKbps                    int
	uhd, hdr, remote              int
	release                       string
	reasonKind, reasonDetail      string
	startFailure                  string
}

func measure(p *core.PlaySession, release string) playMeasure {
	q := playMeasure{ttffMS: -1, remote: store.BoolInt(p.Client.Remote)}
	if e := p.Experience; e != nil {
		q.played = store.BoolInt(e.Requested)
		q.pictured = store.BoolInt(e.Pictured)
		if e.Pictured && e.StartTime >= 0 {
			q.ttffMS = e.StartTime.Milliseconds()
		}
		if e.StartFailure != "" {
			q.startFailed, q.startFailure = 1, e.StartFailure
		}
		q.bufferEvents, q.bufferMS = e.BufferEvents, e.Buffering.Milliseconds()
	}
	if len(release) > maxReleaseName {
		release = release[:maxReleaseName]
	}
	q.release = release
	q.sourceKbps = p.Info.BitrateKbps
	if q.sourceKbps <= 0 && p.Info.Size > 0 && p.Info.Duration > 0 {
		q.sourceKbps = int(float64(p.Info.Size) * 8 / 1000 / p.Info.Duration.Seconds())
	}
	if q.sourceKbps <= 0 {
		return q
	}
	videoCopy := p.Decision == core.DecisionDirectPlay || p.DecisionVideoCopy
	if v := p.Info.Video(); v != nil && videoCopy {
		q.uhd = store.BoolInt(v.Height >= 1600 || v.Width >= 3200)
		q.hdr = store.BoolInt(v.DolbyVision || v.ColorTransfer == "smpte2084" || v.ColorTransfer == "arib-std-b67")
	}
	if p.Decision == core.DecisionRemux || p.Decision == core.DecisionTranscode {
		q.reasonKind, q.reasonDetail = classifyReason(p.DecisionReason)
	}
	return q
}

// classifyReason turns a transcode decision's explanation into a stable
// kind and its subject (a codec, container or limit) for translated copy.
// The decision text lists its causes after the action, comma separated.
func classifyReason(reason string) (kind, detail string) {
	parts := strings.Split(reason, "; ")
	if len(parts) < 2 {
		return "", ""
	}
	for _, why := range strings.Split(parts[1], ", ") {
		f := strings.Fields(why)
		word := func(i int) string {
			if i < len(f) {
				return f[i]
			}
			return ""
		}
		switch {
		case strings.HasPrefix(why, "audio codec "):
			return "audio_codec", word(2)
		case strings.HasPrefix(why, "audio processing"):
			return "audio_processing", ""
		case strings.HasPrefix(why, "audio."):
			return "audio_limit", strings.TrimPrefix(word(0), "audio.")
		case strings.HasPrefix(why, "video codec "):
			return "video_codec", word(2)
		case strings.HasPrefix(why, "video profile ") || strings.HasPrefix(why, "video level "):
			return "video_profile", word(2)
		case strings.HasPrefix(why, "bitrate "):
			return "bitrate", ""
		case strings.HasSuffix(why, "subtitles must be burned in"):
			return "subtitles", word(0)
		case strings.HasPrefix(why, "container "):
			return "container", word(1)
		case strings.HasPrefix(why, "client lacks"), strings.Contains(why, "Dolby Vision"):
			return "hdr", ""
		case strings.HasSuffix(why, "not allowed by client"):
			return "client_setting", ""
		case len(f) >= 3 && f[1] == "exceeds" && strings.HasSuffix(f[0], "p"):
			return "resolution", strings.TrimSuffix(word(2), "p")
		}
	}
	return "other", ""
}

// ExperienceMetrics summarises one viewer's (or the household's) plays.
type ExperienceMetrics struct {
	Plays               int              `json:"plays"`
	MeasuredPlays       int              `json:"measuredPlays"`
	StartMedianMS       *int64           `json:"startMedianMs"`
	StartP90MS          *int64           `json:"startP90Ms"`
	BufferEventsPerHour *float64         `json:"bufferEventsPerHour"`
	BufferMS            int64            `json:"bufferMs"`
	MeasuredWatchedMS   int64            `json:"measuredWatchedMs"`
	FailedStarts        int              `json:"failedStarts"`
	FailedReasons       []ReasonCount    `json:"failedReasons"`
	Decisions           DecisionCounts   `json:"decisions"`
	TopTranscodeReason  *TranscodeReason `json:"topTranscodeReason"`
	SourceKbps          *int             `json:"sourceKbps"`
	DeliveredKbps       *int             `json:"deliveredKbps"`
	UHDPct              *float64         `json:"uhdPct"`
	HDRPct              *float64         `json:"hdrPct"`
	Switches            int              `json:"switches"`
	DownSwitches        int              `json:"downSwitches"`
	Finished            int              `json:"finished"`
	Abandoned           int              `json:"abandoned"`
	RemotePct           *float64         `json:"remotePct"`
	WatchedMS           int64            `json:"watchedMs"`
	// TopDevice is the device with the most plays, as recorded.
	TopDevice *DeviceCount `json:"topDevice"`
}
type DeviceCount struct {
	Name  string `json:"name"`
	Plays int    `json:"plays"`
}
type ReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}
type DecisionCounts struct {
	DirectPlay int `json:"directplay"`
	Remux      int `json:"copy"`
	Transcode  int `json:"transcode"`
}
type TranscodeReason struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Count  int    `json:"count"`
}

// ExperiencePlay is one recorded play with its experience.
type ExperiencePlay struct {
	ID              string `json:"id"`
	UserID          int64  `json:"userId"`
	Username        string `json:"-"`
	Title           string `json:"title"`
	Device          string `json:"device"`
	Platform        string `json:"platform"`
	Started         int64  `json:"started"`
	Ended           int64  `json:"-"`
	Measured        bool   `json:"measured"`
	StartMS         *int64 `json:"startMs"`
	BufferEvents    *int   `json:"bufferEvents"`
	BufferMS        *int64 `json:"bufferMs"`
	Failed          bool   `json:"failed"`
	Error           string `json:"error"`
	Decision        string `json:"decision"`
	ReasonKind      string `json:"reasonKind"`
	ReasonDetail    string `json:"reasonDetail"`
	QualityFallback string `json:"qualityFallback,omitempty"`
	Origin          string `json:"origin"`
	Release         string `json:"release"`
	Remote          bool   `json:"remote"`
	WatchedMS       int64  `json:"watchedMs"`
	DurationMS      int64  `json:"durationMs"`
	Switches        int    `json:"switches"`
	Completed       bool   `json:"-"`

	downSwitches, sourceKbps, deliveredKbps int // delivered: bytes over watched time
	uhd, hdr, pictured                      bool
}

type ExperienceViewerRow struct {
	UserID   int64             `json:"userId"`
	Username string            `json:"username"`
	Metrics  ExperienceMetrics `json:"metrics"`
}
type ExperienceOverview struct {
	Days          int                   `json:"days"`
	Since         int64                 `json:"since"`
	MeasuredSince int64                 `json:"measuredSince"`
	Household     ExperienceMetrics     `json:"household"`
	Viewers       []ExperienceViewerRow `json:"viewers"`
}
type ExperienceDay struct {
	Day                 string   `json:"day"`
	Plays               int      `json:"plays"`
	StartMedianMS       *int64   `json:"startMedianMs"`
	BufferEventsPerHour *float64 `json:"bufferEventsPerHour"`
}

// ExperienceCause is the most likely cause of a viewer's experience, picked
// by simple rules; the console words it.
type ExperienceCause struct {
	Kind         string   `json:"kind"`
	Reason       string   `json:"reason,omitempty"`
	Count        int      `json:"count,omitempty"`
	RemotePct    float64  `json:"remotePct,omitempty"`
	SourceKbps   int      `json:"sourceKbps,omitempty"`
	Device       string   `json:"device,omitempty"`
	ReasonKind   string   `json:"reasonKind,omitempty"`
	ReasonDetail string   `json:"reasonDetail,omitempty"`
	Share        float64  `json:"share,omitempty"`
	Switches     int      `json:"switches,omitempty"`
	Plays        int      `json:"plays,omitempty"`
	MedianMS     int64    `json:"medianMs,omitempty"`
	PerHour      *float64 `json:"perHour,omitempty"`
}
type ExperienceViewer struct {
	UserID        int64             `json:"userId"`
	Username      string            `json:"username"`
	Days          int               `json:"days"`
	Since         int64             `json:"since"`
	MeasuredSince int64             `json:"measuredSince"`
	Metrics       ExperienceMetrics `json:"metrics"`
	Trend         []ExperienceDay   `json:"trend"`
	Plays         []ExperiencePlay  `json:"plays"`
	Cause         ExperienceCause   `json:"cause"`
}

const experienceColumns = `id,user_id,username,title,platform,device,decision,started,ended,bytes,watched_ms,duration_ms,completed,failovers,error,measured,pictured,ttff_ms,start_failed,buffer_events,buffer_ms,down_failovers,played,source_kbps,uhd,hdr,remote,origin,release_name,reason_kind,reason_detail,quality_fallback`

// realPlay is a play History counts (stats.Summary): one that delivered
// media, plus a measured start that never did (a failed start). Older
// zero-byte rows were probes or duplicate opens.
// played; older rows predate that distinction and count.
const realPlay = `(bytes>0 OR watched_ms>0 OR (measured=1 AND played=1))`

func (s *Service) experienceRows(ctx context.Context, where string, limit int, args ...any) ([]ExperiencePlay, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+experienceColumns+` FROM stats_plays WHERE `+where+` AND `+realPlay+` ORDER BY started DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExperiencePlay{}
	for rows.Next() {
		var p ExperiencePlay
		var measured, played, pictured, startFailed, events, uhd, hdr, remote int
		var ttff, bufferMS, bytes int64
		if err := rows.Scan(&p.ID, &p.UserID, &p.Username, &p.Title, &p.Platform, &p.Device, &p.Decision, &p.Started, &p.Ended, &bytes, &p.WatchedMS, &p.DurationMS, &p.Completed, &p.Switches, &p.Error,
			&measured, &pictured, &ttff, &startFailed, &events, &bufferMS, &p.downSwitches, &played, &p.sourceKbps, &uhd, &hdr, &remote, &p.Origin, &p.Release, &p.ReasonKind, &p.ReasonDetail, &p.QualityFallback); err != nil {
			return nil, err
		}
		// Measured: a play request reached a client that reports timelines;
		// older rows and file/DLNA surfaces carry no start or buffering.
		p.Measured, p.uhd, p.hdr, p.Remote = measured == 1 && played == 1, uhd == 1, hdr == 1, remote == 1
		if bytes > 0 && p.WatchedMS >= abandonWindow.Milliseconds() {
			// Delivered bitrate is the viewing's bytes over its watched
			// time; short plays are left out because read-ahead dominates.
			p.deliveredKbps = int(bytes * 8 / p.WatchedMS)
		}
		p.pictured = !p.Measured || (pictured == 1 && startFailed == 0)
		if p.Measured {
			p.BufferEvents, p.BufferMS = &events, &bufferMS
			if p.pictured && ttff >= 0 {
				p.StartMS = &ttff
			}
			p.Failed = p.Ended > 0 && !p.pictured
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) measuredSince(ctx context.Context) int64 {
	var at int64
	_ = s.db.QueryRowContext(ctx, `SELECT at FROM stats_snapshots WHERE kind=?`, measuredSinceKind).Scan(&at)
	return at
}

func percentile(sorted []int64, p float64) *int64 {
	if len(sorted) == 0 {
		return nil
	}
	i := int(float64(len(sorted))*p+0.999999) - 1 // nearest rank
	v := sorted[min(max(i, 0), len(sorted)-1)]
	return &v
}

func pct(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	v := float64(part) * 100 / float64(whole)
	return &v
}

func perHour(events int, watchedMS int64) *float64 {
	if watchedMS < time.Minute.Milliseconds() {
		return nil
	}
	v := float64(events) * 3600000 / float64(watchedMS)
	return &v
}

func metrics(plays []ExperiencePlay) ExperienceMetrics {
	m := ExperienceMetrics{Plays: len(plays), FailedReasons: []ReasonCount{}}
	var starts []int64
	var events int
	var watched, sourceW, sourceSum, deliveredW, deliveredSum, uhdW, hdrW, mediaW, remoteW, measuredW int64
	failed := map[string]int{}
	reasons := map[[2]string]int{}
	devices := map[string]int{}
	for _, p := range plays {
		if device := cmp.Or(p.Device, p.Platform); device != "" {
			devices[device]++
		}
		switch core.DecisionKind(p.Decision) {
		case core.DecisionDirectPlay:
			m.Decisions.DirectPlay++
		case core.DecisionRemux:
			m.Decisions.Remux++
		case core.DecisionTranscode:
			m.Decisions.Transcode++
			if p.ReasonKind != "" {
				reasons[[2]string{p.ReasonKind, p.ReasonDetail}]++
			}
		}
		m.Switches += p.Switches
		m.DownSwitches += p.downSwitches
		watched += p.WatchedMS
		if p.Completed {
			m.Finished++
		} else if p.Ended > 0 && !p.Failed && p.WatchedMS < abandonWindow.Milliseconds() {
			m.Abandoned++
		}
		if p.sourceKbps > 0 && p.WatchedMS > 0 {
			sourceW += p.WatchedMS
			sourceSum += int64(p.sourceKbps) * p.WatchedMS
			mediaW += p.WatchedMS
			if p.uhd {
				uhdW += p.WatchedMS
			}
			if p.hdr {
				hdrW += p.WatchedMS
			}
		}
		if p.deliveredKbps > 0 && p.WatchedMS > 0 {
			deliveredW += p.WatchedMS
			deliveredSum += int64(p.deliveredKbps) * p.WatchedMS
		}
		if !p.Measured {
			continue
		}
		m.MeasuredPlays++
		measuredW += p.WatchedMS
		if p.Remote {
			remoteW += p.WatchedMS
		}
		if p.Failed {
			m.FailedStarts++
			failed[p.Error]++
		}
		if p.StartMS != nil {
			starts = append(starts, *p.StartMS)
		}
		if p.pictured {
			m.MeasuredWatchedMS += p.WatchedMS
			events += *p.BufferEvents
			m.BufferMS += *p.BufferMS
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	m.StartMedianMS, m.StartP90MS = percentile(starts, 0.5), percentile(starts, 0.9)
	m.BufferEventsPerHour = perHour(events, m.MeasuredWatchedMS)
	if sourceW > 0 {
		v := int(sourceSum / sourceW)
		m.SourceKbps = &v
	}
	if deliveredW > 0 {
		v := int(deliveredSum / deliveredW)
		m.DeliveredKbps = &v
	}
	m.UHDPct, m.HDRPct = pct(uhdW, mediaW), pct(hdrW, mediaW)
	m.RemotePct = pct(remoteW, measuredW)
	m.WatchedMS = watched
	for name, n := range devices {
		if top := m.TopDevice; top == nil || n > top.Plays || (n == top.Plays && name < top.Name) {
			m.TopDevice = &DeviceCount{Name: name, Plays: n}
		}
	}
	for reason, n := range failed {
		m.FailedReasons = append(m.FailedReasons, ReasonCount{reason, n})
	}
	sort.Slice(m.FailedReasons, func(i, j int) bool {
		a, b := m.FailedReasons[i], m.FailedReasons[j]
		return a.Count > b.Count || (a.Count == b.Count && a.Reason < b.Reason)
	})
	m.FailedReasons = m.FailedReasons[:min(3, len(m.FailedReasons))]
	for k, n := range reasons {
		top := m.TopTranscodeReason
		if top == nil || n > top.Count || (n == top.Count && k[0]+k[1] < top.Kind+top.Detail) {
			m.TopTranscodeReason = &TranscodeReason{Kind: k[0], Detail: k[1], Count: n}
		}
	}
	return m
}

// badness orders viewers worst first: failed starts, then slow starts,
// then buffering, each against the console's targets.
func badness(m ExperienceMetrics) float64 {
	score := 0.0
	if m.MeasuredPlays > 0 {
		score += 100 * float64(m.FailedStarts) / float64(m.MeasuredPlays)
	}
	if m.StartMedianMS != nil {
		score += float64(*m.StartMedianMS) / 1000
	}
	if m.BufferEventsPerHour != nil {
		score += 5 * *m.BufferEventsPerHour
	}
	return score
}

func experienceDays(r *http.Request) int {
	if r.URL.Query().Get("days") == "30" {
		return 30
	}
	return 7
}

// Experience returns the household and per-viewer experience for the last
// days (7 or 30).
func (s *Service) Experience(ctx context.Context, days int) (ExperienceOverview, error) {
	since := s.opts.Now().UTC().AddDate(0, 0, -days)
	out := ExperienceOverview{Days: days, Since: since.Unix(), MeasuredSince: s.measuredSince(ctx), Viewers: []ExperienceViewerRow{}}
	plays, err := s.experienceRows(ctx, `started>=?`, experienceRowCap, since.Unix())
	if err != nil {
		return out, err
	}
	out.Household = metrics(plays)
	by := map[int64][]ExperiencePlay{}
	names := map[int64]string{}
	for _, p := range plays {
		by[p.UserID] = append(by[p.UserID], p)
		if names[p.UserID] == "" {
			names[p.UserID] = p.Username // newest first
		}
	}
	for id, list := range by {
		out.Viewers = append(out.Viewers, ExperienceViewerRow{UserID: id, Username: names[id], Metrics: metrics(list)})
	}
	sort.Slice(out.Viewers, func(i, j int) bool {
		a, b := badness(out.Viewers[i].Metrics), badness(out.Viewers[j].Metrics)
		return a > b || (a == b && out.Viewers[i].UserID < out.Viewers[j].UserID)
	})
	return out, nil
}

// ExperienceForViewer returns one viewer's metrics, daily trend, last 20
// plays and the most likely cause of a poor experience.
func (s *Service) ExperienceForViewer(ctx context.Context, user int64, days int) (ExperienceViewer, error) {
	now := s.opts.Now().UTC()
	since := now.AddDate(0, 0, -days)
	out := ExperienceViewer{UserID: user, Days: days, Since: since.Unix(), MeasuredSince: s.measuredSince(ctx), Trend: []ExperienceDay{}}
	plays, err := s.experienceRows(ctx, `user_id=? AND started>=?`, experienceRowCap, user, since.Unix())
	if err != nil {
		return out, err
	}
	out.Metrics = metrics(plays)
	recent, err := s.experienceRows(ctx, `user_id=?`, 20, user)
	if err != nil {
		return out, err
	}
	out.Plays = recent
	if len(recent) > 0 {
		out.Username = recent[0].Username
	}
	byDay := map[string][]ExperiencePlay{}
	for _, p := range plays {
		d := time.Unix(p.Started, 0).UTC().Format("2006-01-02")
		byDay[d] = append(byDay[d], p)
	}
	first := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	for d := first; !d.After(now); d = d.AddDate(0, 0, 1) {
		label := d.Format("2006-01-02")
		m := metrics(byDay[label])
		out.Trend = append(out.Trend, ExperienceDay{Day: label, Plays: m.Plays, StartMedianMS: m.StartMedianMS, BufferEventsPerHour: m.BufferEventsPerHour})
	}
	out.Cause = cause(out.Metrics, plays)
	return out, nil
}

// Thresholds behind the cause sentence and the console's colour cues.
const (
	goodStartMS       = 5000
	goodBufferPerHour = 1.0
	highAwayKbps      = 15000
)

func cause(m ExperienceMetrics, plays []ExperiencePlay) ExperienceCause {
	if m.Plays == 0 {
		return ExperienceCause{Kind: "not_enough_data"}
	}
	slow := m.StartMedianMS != nil && *m.StartMedianMS > goodStartMS
	buffering := m.BufferEventsPerHour != nil && *m.BufferEventsPerHour >= goodBufferPerHour
	if m.FailedStarts >= 2 && m.FailedStarts*5 >= m.MeasuredPlays {
		c := ExperienceCause{Kind: "failed_starts", Count: m.FailedStarts}
		if len(m.FailedReasons) > 0 {
			c.Reason = m.FailedReasons[0].Reason
		}
		return c
	}
	if (slow || buffering || m.DownSwitches > 0) && m.RemotePct != nil && *m.RemotePct >= 50 && m.SourceKbps != nil && *m.SourceKbps >= highAwayKbps {
		return ExperienceCause{Kind: "away_bitrate", RemotePct: *m.RemotePct, SourceKbps: *m.SourceKbps}
	}
	if m.Switches >= 2 && m.Switches*10 >= m.Plays*3 {
		return ExperienceCause{Kind: "source_switches", Switches: m.Switches, Plays: m.Plays}
	}
	if m.MeasuredPlays == 0 {
		// Older plays carry no start time or buffering: nothing to judge.
		return ExperienceCause{Kind: "not_enough_data"}
	}
	transcodes := func() *ExperienceCause {
		if m.Decisions.Transcode*2 < m.Plays {
			return nil
		}
		type key struct{ device, kind, detail string }
		groups := map[key]int{}
		var best key
		for _, p := range plays {
			if core.DecisionKind(p.Decision) != core.DecisionTranscode || p.ReasonKind == "" {
				continue
			}
			device := p.Device
			if device == "" {
				device = p.Platform
			}
			k := key{device, p.ReasonKind, p.ReasonDetail}
			groups[k]++
			if groups[k] > groups[best] || (groups[k] == groups[best] && k.device+k.kind+k.detail < best.device+best.kind+best.detail) {
				best = k
			}
		}
		n := groups[best]
		if n < 2 || n*2 < m.Decisions.Transcode {
			return nil
		}
		return &ExperienceCause{Kind: "transcode_device", Device: best.device, ReasonKind: best.kind, ReasonDetail: best.detail, Share: float64(n) * 100 / float64(m.Plays)}
	}
	if slow || buffering {
		if c := transcodes(); c != nil {
			return *c
		}
		if slow {
			return ExperienceCause{Kind: "slow_starts", MedianMS: *m.StartMedianMS}
		}
		return ExperienceCause{Kind: "buffering", PerHour: m.BufferEventsPerHour}
	}
	if c := transcodes(); c != nil {
		return *c
	}
	return ExperienceCause{Kind: "none"}
}

// ExperienceHandler serves Activity › Experience; mount it behind owner
// authentication with /experience stripped.
func (s *Service) ExperienceHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Experience(r.Context(), experienceDays(r))
		if err != nil {
			fail(w, 500, "Experience unavailable")
			return
		}
		reply(w, 200, v)
	})
	m.HandleFunc("GET /viewers/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			fail(w, 400, "Invalid viewer")
			return
		}
		v, err := s.ExperienceForViewer(r.Context(), id, experienceDays(r))
		if err != nil {
			fail(w, 500, "Experience unavailable")
			return
		}
		reply(w, 200, v)
	})
	return m
}

// PlayByID returns one recorded play with its experience fields merged
// into the History play's; nil when unknown.
func (s *Service) PlayByID(ctx context.Context, id string) (map[string]any, error) {
	exp, err := s.experienceRows(ctx, `id=?`, 1, id)
	if err != nil || len(exp) == 0 {
		return nil, err
	}
	var p Play
	err = s.db.QueryRowContext(ctx, `SELECT id,user_id,rating_key,username,title,platform,device,decision,started,updated,ended,bytes,watched_ms,duration_ms,completed,failovers,error,client_ip FROM stats_plays WHERE id=?`, id).
		Scan(&p.ID, &p.UserID, &p.Key, &p.Username, &p.Title, &p.Platform, &p.Device, &p.Decision, &p.Started, &p.Updated, &p.Ended, &p.Bytes, &p.WatchedMS, &p.DurationMS, &p.Completed, &p.Failovers, &p.Error, &p.IP)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, v := range []any{p, exp[0]} {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, err
		}
	}
	var sourceKbps, uhd, hdr int
	var release string
	err = s.db.QueryRowContext(ctx, `SELECT source_kbps,uhd,hdr,release_name FROM stats_plays WHERE id=?`, id).Scan(&sourceKbps, &uhd, &hdr, &release)
	if err != nil {
		return nil, err
	}
	out["sourceKbps"], out["uhd"], out["hdr"], out["releaseName"] = sourceKbps, uhd != 0, hdr != 0, release
	out["ttffMs"] = exp[0].StartMS
	out["avgBitrateKbps"] = float64(0)
	if p.WatchedMS > 0 {
		out["avgBitrateKbps"] = float64(p.Bytes) * 8 / float64(p.WatchedMS)
	}
	return out, nil
}
