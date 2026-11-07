package livetv

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// An HLS provider keeps a backlog of segments in its live playlist,
// typically 12–30 s. Steady starts its decoder in that backlog
// (live_start_index 0 on the window the relay serves), so the reserve costs
// no tune time: the decoder reads at real time behind the live edge, and a
// provider stall plays out of segments the provider still lists.
//
// For a Steady grant the relay trims each media playlist it serves so its
// first segment sits at the reserve depth: the preset's target, or the
// provider's backlog less its oldest segment (which may expire before the
// decoder reaches it), whichever is smaller. After the decoder starts, the
// window follows one segment behind the last segment it fetched, so a
// decoder restarted on the same grant resumes where the old one stopped.
//
// hlsInput also measures the provider's health: whether the playlist keeps
// advancing on schedule. For a Steady grant it polls the playlist itself
// when the decoder is busy in the backlog and not reloading, so a stall is
// seen as soon as it happens, while the reserve still plays.

// hlsSegment is one media segment of a provider playlist.
type hlsSegment struct {
	seq int64
	dur time.Duration
	url string // absolute upstream URL
}

// hlsMedia is a parsed live media playlist, kept as blocks of lines so a
// window can be cut from it without disturbing anything else.
type hlsMedia struct {
	header []string   // lines before the first segment's tags
	blocks [][]string // each segment's tag lines followed by its URI line
	segs   []hlsSegment
	target time.Duration
	seq    int64 // EXT-X-MEDIA-SEQUENCE
	disc   int64 // EXT-X-DISCONTINUITY-SEQUENCE
	ended  bool
	master bool
	tail   []string // lines after the last URI
}

// hlsSegmentTag reports tags that belong to the segment that follows them.
func hlsSegmentTag(line string) bool {
	for _, p := range []string{"#EXTINF", "#EXT-X-BYTERANGE", "#EXT-X-DISCONTINUITY", "#EXT-X-KEY", "#EXT-X-MAP",
		"#EXT-X-PROGRAM-DATE-TIME", "#EXT-X-GAP", "#EXT-X-BITRATE", "#EXT-X-DATERANGE", "#EXT-X-CUE", "#EXT-OATCLS", "#EXT-X-SCTE35"} {
		if strings.HasPrefix(line, p) && !strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE") {
			return true
		}
	}
	return false
}

func parseHLSMedia(b []byte, base *url.URL) *hlsMedia {
	m := &hlsMedia{}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	var cur []string
	inSegs := false
	var dur time.Duration
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			if inSegs {
				cur = append(cur, raw)
			} else {
				m.header = append(m.header, raw)
			}
			continue
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF"):
			m.master = true
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			if n, err := strconv.ParseFloat(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"), 64); err == nil && n > 0 {
				m.target = time.Duration(n * float64(time.Second))
			}
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			m.seq, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			m.disc, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"), 10, 64)
		case line == "#EXT-X-ENDLIST":
			m.ended = true
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.SplitN(strings.TrimPrefix(line, "#EXTINF:"), ",", 2)[0]
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f > 0 && f < 600 {
				dur = time.Duration(f * float64(time.Second))
			}
		}
		if !strings.HasPrefix(line, "#") {
			cur = append(cur, raw)
			u, err := url.Parse(line)
			abs := line
			if err == nil && base != nil {
				abs = base.ResolveReference(u).String()
			}
			m.segs = append(m.segs, hlsSegment{seq: m.seq + int64(len(m.blocks)), dur: dur, url: abs})
			m.blocks = append(m.blocks, cur)
			cur, dur = nil, 0
			inSegs = true
			continue
		}
		if !inSegs && !hlsSegmentTag(line) {
			m.header = append(m.header, raw)
			continue
		}
		inSegs = true
		cur = append(cur, raw)
	}
	m.tail = cur
	return m
}

// live reports a live media playlist the window applies to.
func (m *hlsMedia) live() bool { return !m.master && !m.ended && len(m.segs) > 0 }

// startFor picks the first segment for a reader that should play d behind
// the live edge.
func (m *hlsMedia) startFor(d time.Duration) int {
	n := len(m.segs)
	if n < 2 {
		return 0
	}
	// Never start on the provider's oldest segment when it can be avoided:
	// it is the next to leave the playlist and may 404 before it is read.
	lowest := 0
	if n >= 3 {
		lowest = 1
	}
	var sum time.Duration
	for i := n - 1; i > lowest; i-- {
		sum += m.segs[i].dur
		if sum >= d {
			return i
		}
	}
	return lowest
}

// window renders the playlist from the first segment with seq >= floor.
// Dropped segments' discontinuities move into the discontinuity sequence,
// and the latest key and map tags they carried stay in force.
func (m *hlsMedia) window(floor int64) []byte {
	drop := 0
	for drop < len(m.segs) && m.segs[drop].seq < floor {
		drop++
	}
	if drop == 0 || drop >= len(m.segs) {
		return nil
	}
	var key, mapTag string
	disc := m.disc
	for _, block := range m.blocks[:drop] {
		for _, raw := range block {
			line := strings.TrimSpace(raw)
			switch {
			case line == "#EXT-X-DISCONTINUITY":
				disc++
			case strings.HasPrefix(line, "#EXT-X-KEY"):
				key = raw
			case strings.HasPrefix(line, "#EXT-X-MAP"):
				mapTag = raw
			}
		}
	}
	first := m.blocks[drop]
	for _, raw := range first {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#EXT-X-KEY") {
			key = ""
		}
		if strings.HasPrefix(line, "#EXT-X-MAP") {
			mapTag = ""
		}
	}
	var out []string
	sawDisc := false
	for _, raw := range m.header {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			raw = "#EXT-X-MEDIA-SEQUENCE:" + strconv.FormatInt(m.segs[drop].seq, 10)
		case strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			raw = "#EXT-X-DISCONTINUITY-SEQUENCE:" + strconv.FormatInt(disc, 10)
			sawDisc = true
		}
		out = append(out, raw)
	}
	if !sawDisc && disc != 0 {
		out = append(out, "#EXT-X-DISCONTINUITY-SEQUENCE:"+strconv.FormatInt(disc, 10))
	}
	if !hasMediaSequence(m.header) {
		out = append(out, "#EXT-X-MEDIA-SEQUENCE:"+strconv.FormatInt(m.segs[drop].seq, 10))
	}
	if key != "" {
		out = append(out, key)
	}
	if mapTag != "" {
		out = append(out, mapTag)
	}
	for _, block := range m.blocks[drop:] {
		out = append(out, block...)
	}
	out = append(out, m.tail...)
	return []byte(strings.Join(out, "\n"))
}

func hasMediaSequence(lines []string) bool {
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "#EXT-X-MEDIA-SEQUENCE:") {
			return true
		}
	}
	return false
}

// hlsInput is one grant's view of its provider playlists.
type hlsInput struct {
	g       *inputGrant
	mu      sync.Mutex
	lists   map[string]*hlsList // by upstream playlist URL as requested
	segs    map[string]int64    // upstream segment URL -> sequence
	polling map[string]bool
	// Steady prefetch (reserve_hls_prefetch.go).
	fetches map[string]*hlsFetch
	slots   chan struct{}
	extra   int // fetches running on an extra account slot
	held    int
}

// hlsList is the state of one provider media playlist.
type hlsList struct {
	target      time.Duration
	last        []hlsSegment
	newest      int64 // highest sequence seen
	advancedAt  time.Time
	fetchedAt   time.Time
	failures    int // consecutive failed fetches
	floor       int64
	served      int64 // highest sequence the reader fetched
	stalled     bool
	failed      bool
	initialized bool
}

func (g *inputGrant) hlsIfAny() *hlsInput {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hls
}

func (g *inputGrant) hlsState() *hlsInput {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hls == nil {
		g.hls = &hlsInput{g: g, lists: map[string]*hlsList{}, segs: map[string]int64{}, polling: map[string]bool{}}
	}
	return g.hls
}

// media handles one fetched media playlist for the grant: it records health
// and returns the playlist to serve (trimmed to the reserve window for a
// Steady grant).
func (h *hlsInput) media(raw string, body []byte, base *url.URL) ([]byte, []UpstreamEvent) {
	m := parseHLSMedia(body, base)
	if !m.live() {
		return body, nil
	}
	now := time.Now()
	h.mu.Lock()
	l := h.lists[raw]
	if l == nil {
		l = &hlsList{served: -1, floor: -1}
		h.lists[raw] = l
	}
	l.target = m.target
	if l.target <= 0 {
		l.target = 6 * time.Second
	}
	l.fetchedAt = now
	l.failures = 0
	newest := m.segs[len(m.segs)-1].seq
	if !l.initialized || newest > l.newest {
		l.newest, l.advancedAt = newest, now
	}
	l.last = m.segs
	for _, sg := range m.segs {
		h.segs[sg.url] = sg.seq
	}
	if len(h.segs) > 4*relayResourceLimit {
		h.segs = map[string]int64{}
		for _, sg := range m.segs {
			h.segs[sg.url] = sg.seq
		}
	}
	reserve := h.g.reserve
	if reserve > 0 && !l.initialized {
		l.floor = m.segs[m.startFor(h.g.joinDepth())].seq
	}
	if l.served >= 0 && l.served-1 > l.floor {
		l.floor = l.served - 1
	}
	l.initialized = true
	events := h.transitionsLocked(l, now)
	h.prefetchLocked(l)
	h.publishLocked()
	floor := l.floor
	startPoll := reserve > 0 && !h.polling[raw]
	if startPoll {
		h.polling[raw] = true
	}
	h.mu.Unlock()
	if startPoll {
		go h.poll(raw)
	}
	if reserve > 0 && floor >= 0 {
		if out := m.window(floor); out != nil {
			return out, events
		}
	}
	return body, events
}

// failedFetch records a playlist fetch that failed.
func (h *hlsInput) failedFetch(raw string) []UpstreamEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.lists[raw]
	if l == nil {
		return nil
	}
	l.failures++
	events := h.transitionsLocked(l, time.Now())
	h.publishLocked()
	return events
}

// isSegment reports a media segment named by one of the grant's live
// playlists.
func (h *hlsInput) isSegment(raw string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.segs[raw]
	return ok
}

// servedSegment records a segment fetch by the reader.
func (h *hlsInput) servedSegment(raw string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	seq, ok := h.segs[raw]
	if !ok {
		return
	}
	for _, l := range h.lists {
		for _, sg := range l.last {
			if sg.url == raw && seq > l.served {
				l.served = seq
				h.evictLocked(seq)
				h.prefetchLocked(l)
			}
		}
	}
	h.publishLocked()
}

// lateness is how far past schedule the playlist is: zero while a new
// segment arrives within a target duration of the last.
func (l *hlsList) lateness(now time.Time) time.Duration {
	if l.advancedAt.IsZero() {
		return 0
	}
	return max(0, now.Sub(l.advancedAt)-l.target)
}

func (h *hlsInput) transitionsLocked(l *hlsList, now time.Time) []UpstreamEvent {
	var out []UpstreamEvent
	stalled := l.lateness(now) > l.target/2 || l.failures > 0
	if stalled != l.stalled {
		l.stalled = stalled
		kind := InputRecovered
		if stalled {
			kind = InputStalled
		}
		out = append(out, UpstreamEvent{Kind: kind, At: now})
	}
	failed := l.failures >= tapFailAfter
	if failed && !l.failed {
		out = append(out, UpstreamEvent{Kind: InputFailed, At: now})
	}
	l.failed = failed
	return out
}

// health aggregates the grant's playlists, from the reader's point of view.
func (h *hlsInput) health() UpstreamHealth {
	h.mu.Lock()
	defer h.mu.Unlock()
	out, _ := h.healthLocked(time.Now())
	return out
}

// publishLocked refreshes the grant's lock-free health snapshot.
func (h *hlsInput) publishLocked() {
	out, due := h.healthLocked(time.Now())
	h.g.snap.Store(&healthSnap{h: out, due: due})
}

// healthLocked also returns when the most overdue playlist's next segment
// was due, from which DataAge (lateness) is derived at read time.
func (h *hlsInput) healthLocked(now time.Time) (UpstreamHealth, time.Time) {
	out := UpstreamHealth{Kind: "hls", Connected: true}
	var due time.Time
	first := true
	for _, l := range h.lists {
		if now.Sub(l.fetchedAt) > 4*l.target && !first {
			continue // a variant the reader no longer loads
		}
		late := l.lateness(now)
		var depth time.Duration
		for _, sg := range l.last {
			if sg.seq > l.served {
				depth += sg.dur
			}
		}
		if first || late > out.DataAge {
			out.DataAge = late
		}
		if !l.advancedAt.IsZero() {
			if at := l.advancedAt.Add(l.target); due.IsZero() || at.Before(due) {
				due = at
			}
		}
		if first || depth < out.ReserveDepth {
			out.ReserveDepth = depth
		}
		out.Stalled = out.Stalled || l.stalled
		out.Failed = out.Failed || l.failed
		out.Connected = out.Connected && l.failures == 0 && !l.stalled
		if h.g.reserve > 0 {
			var backlog time.Duration
			for _, sg := range l.last[min(1, len(l.last)):] {
				backlog += sg.dur
			}
			out.Target = min(h.g.reserve, backlog)
		}
		first = false
	}
	return out, due
}

// poll fetches the playlist on schedule while the reader is busy in the
// backlog and not reloading it, so a provider stall is noticed at once.
// It never fetches segments, and skips a turn whenever the reader itself
// reloaded recently.
func (h *hlsInput) poll(raw string) {
	s := h.g.service
	for {
		h.mu.Lock()
		l := h.lists[raw]
		wait := 6 * time.Second
		if l != nil {
			wait = max(time.Second, l.target/2)
			if since := time.Since(l.fetchedAt); since < wait {
				wait -= since
			}
		}
		h.mu.Unlock()
		select {
		case <-h.g.ctx.Done():
			return
		case <-time.After(wait):
		}
		h.mu.Lock()
		due := l == nil || time.Since(l.fetchedAt) >= max(time.Second, l.target/2)
		h.mu.Unlock()
		if !due || s == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(h.g.ctx, max(2*time.Second, l.target))
		res, err := s.relayFetch(ctx, cancel, h.g, raw, &http.Request{Header: http.Header{}})
		var events []UpstreamEvent
		if err == nil && res.StatusCode == http.StatusOK {
			b, rerr := io.ReadAll(io.LimitReader(res.Body, relayManifestLimit+1))
			base := res.Request.URL
			res.Body.Close()
			if rerr == nil && len(b) <= relayManifestLimit {
				_, events = h.media(raw, b, base)
			} else {
				events = h.failedFetch(raw)
			}
		} else {
			if res != nil {
				res.Body.Close()
			}
			if h.g.ctx.Err() != nil {
				cancel()
				return
			}
			events = h.failedFetch(raw)
		}
		cancel()
		h.deliver(events)
	}
}

func (h *hlsInput) deliver(events []UpstreamEvent) {
	if len(events) == 0 {
		return
	}
	health, _ := h.g.snapHealth()
	for _, ev := range events {
		ev.Health = health
		h.g.notify(ev)
	}
}
