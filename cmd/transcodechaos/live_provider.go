package main

import (
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// liveFault is one scripted provider event. At is relative to the provider's
// start. Content faults apply to segments published inside [At, At+For);
// connection faults apply to requests made inside it. For zero means "until
// the end of the run" for state changes and "once" for one-shot events.
type liveFault struct {
	Kind string        `json:"kind"`
	At   time.Duration `json:"at"`
	For  time.Duration `json:"for,omitempty"`
	Code int           `json:"code,omitempty"` // HTTP status for refusals/errors
}

// Fault kinds.
const (
	faultPlaylistStall = "playlist-stall"   // HLS playlist stops advancing, then catches up
	faultSegmentError  = "segment-error"    // segments published in the window return Code
	faultSegmentSlow   = "segment-slow"     // segment responses in the window wait For first
	faultRST           = "rst"              // raw TS: abort the connection at At; HLS: abort requests in window
	faultEOF           = "eof"              // raw TS: clean end of body at At
	faultTSStall       = "ts-stall"         // raw TS: connection stays open, no bytes for For, then live edge
	faultRefuse        = "refuse"           // new connections in window get Code (403/458/509)
	faultDown          = "down"             // everything fails with a TCP reset in the window
	faultCodecAC3      = "codec-ac3"        // audio switches AAC->AC3 on the same PID, PMT version bumped
	faultCodecAC3Same  = "codec-ac3-nobump" // same switch without a PMT version bump
	faultResolution    = "resolution"       // 640x360 -> 1280x720
	faultClockJump     = "clock-jump"       // timestamps jump +5000 s
	faultClockReset    = "clock-reset"      // timestamps restart near zero
	faultKeyframeLoss  = "keyframe-loss"    // keyframe PES packets are dropped
	faultCorrupt       = "corrupt"          // 1% of packets get payload garbage
	faultPacketLoss    = "packet-loss"      // 2% of packets are dropped
)

type providerConfig struct {
	Name string `json:"name"`
	// HLS serves a live playlist; otherwise a real-time raw MPEG-TS body.
	HLS     bool   `json:"hls"`
	Fixture string `json:"fixture,omitempty"` // base fixture variant (default base)
	// Backlog is the number of segments the playlist window holds and how
	// many are already published at start.
	Backlog int `json:"backlog,omitempty"`
	// Discontinuities makes the provider mark its own splices.
	Discontinuities bool `json:"discontinuities,omitempty"`
	// ConnLimit refuses concurrent raw TS bodies above this count with LimitCode.
	ConnLimit int         `json:"connLimit,omitempty"`
	LimitCode int         `json:"limitCode,omitempty"`
	Faults    []liveFault `json:"faults,omitempty"`
	// Absent means the URL points at a closed port.
	Absent bool `json:"absent,omitempty"`
	// Burst is how far behind the live edge a raw TS connection starts, on
	// every connect, including the first one right after start (the history
	// before start is Backlog segments): that backlog is sent at once, then
	// real time follows, like panels that flush their recent buffer. Zero
	// starts at the newest segment (0-4 s already published, sent at once).
	Burst time.Duration `json:"burst,omitempty"`
}

type providerStats struct {
	Opens, Refusals, MaxConcurrent, Requests int
}

type liveProvider struct {
	cfg      providerConfig
	server   *httptest.Server
	url      string
	started  time.Time
	fixtures map[string]*liveFixture
	base     *liveFixture

	mu       sync.Mutex
	produced map[int64][]byte
	cc       ccCounter
	lastN    int64
	open     int
	stats    providerStats
}

func newLiveProvider(cfg providerConfig, fixtures map[string]*liveFixture, start time.Time) (*liveProvider, error) {
	if cfg.Fixture == "" {
		cfg.Fixture = "base"
	}
	if cfg.Backlog <= 0 {
		cfg.Backlog = 6
	}
	if cfg.LimitCode == 0 {
		cfg.LimitCode = 458
	}
	p := &liveProvider{cfg: cfg, fixtures: fixtures, base: fixtures[cfg.Fixture], started: start, produced: map[int64][]byte{}, cc: ccCounter{}, lastN: -1}
	if p.base == nil {
		return nil, fmt.Errorf("fixture %q not loaded", cfg.Fixture)
	}
	path := "/" + cfg.Name + "/live.ts"
	if cfg.HLS {
		path = "/" + cfg.Name + "/live.m3u8"
	}
	if cfg.Absent {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		addr := l.Addr().String()
		_ = l.Close()
		p.url = "http://" + addr + path
		return p, nil
	}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	p.url = p.server.URL + path
	return p, nil
}

func (p *liveProvider) Close() {
	if p.server != nil {
		p.server.CloseClientConnections()
		p.server.Close()
	}
}

func (p *liveProvider) Stats() providerStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

func (p *liveProvider) segDur() time.Duration {
	return time.Duration(p.base.SegTicks) * time.Second / 90000
}

// published is when segment n became available, relative to start.
func (p *liveProvider) published(n int64) time.Duration {
	return time.Duration(n-int64(p.cfg.Backlog)+1) * p.segDur()
}

// liveEdge is the number of segments published at elapsed. The channel has
// been live since before start: Backlog segments already exist then, and a
// negative elapsed reaches back into that history (a raw TS Burst on the first
// connect, which happens at about tune time), down to segment 0.
func (p *liveProvider) liveEdge(elapsed time.Duration) int64 {
	d := p.segDur()
	n := int64(elapsed / d)
	if elapsed < 0 && elapsed%d != 0 {
		n-- // floor, not truncation, for times before start
	}
	return max(n+int64(p.cfg.Backlog), 1)
}

func (p *liveProvider) active(kind string, at time.Duration) (liveFault, bool) {
	for _, f := range p.cfg.Faults {
		if f.Kind != kind || at < f.At {
			continue
		}
		if f.For == 0 || at < f.At+f.For {
			return f, true
		}
	}
	return liveFault{}, false
}

// variantFor is the fixture a segment uses and whether its splice differs from
// the previous segment's (format or timeline change).
func (p *liveProvider) variantFor(n int64) *liveFixture {
	at := p.published(n)
	if _, ok := p.active(faultCodecAC3, at); ok {
		return p.fixtures["ac3"]
	}
	if _, ok := p.active(faultCodecAC3Same, at); ok {
		return p.fixtures["ac3"]
	}
	if _, ok := p.active(faultResolution, at); ok {
		return p.fixtures["hd"]
	}
	return p.base
}

// timeline is segment n's position on the provider's output clock.
func (p *liveProvider) timeline(n int64) int64 {
	at := p.published(n)
	v := n
	for _, f := range p.cfg.Faults {
		if f.Kind == faultClockReset && at >= f.At {
			v = n - p.firstAt(f.At)
		}
	}
	if _, ok := p.active(faultClockJump, at); ok {
		v += 5000 * 90000 / p.base.SegTicks
	}
	return v
}

func (p *liveProvider) firstAt(at time.Duration) int64 {
	n := int64(0)
	for p.published(n) < at {
		n++
	}
	return n
}

// splice reports whether segment n starts a provider discontinuity.
func (p *liveProvider) splice(n int64) bool {
	if n <= 0 {
		return false
	}
	return p.variantFor(n) != p.variantFor(n-1) || p.timeline(n) != p.timeline(n-1)+1
}

func (p *liveProvider) spliceCount(below int64) int {
	c := 0
	for i := int64(1); i < below; i++ {
		if p.splice(i) {
			c++
		}
	}
	return c
}

// segment produces segment n once, in order, so continuity counters run on.
func (p *liveProvider) segment(n int64) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if b, ok := p.produced[n]; ok {
		return b
	}
	if p.lastN >= 0 && n < p.lastN {
		// Older than anything produced: produce standalone.
		return p.produceLocked(n, ccCounter{})
	}
	start := p.lastN + 1
	if p.lastN < 0 || n-p.lastN > 64 {
		start = n
	}
	var out []byte
	for i := start; i <= n; i++ {
		out = p.produceLocked(i, p.cc)
		p.produced[i] = out
	}
	p.lastN = n
	for k := range p.produced {
		if k < n-64 {
			delete(p.produced, k)
		}
	}
	return out
}

func (p *liveProvider) produceLocked(n int64, cc ccCounter) []byte {
	fx := p.variantFor(n)
	k := n % int64(fx.LoopSegs)
	src := fx.Segments[k]
	at := p.published(n)
	offset := (p.timeline(n) - k) * fx.SegTicks
	rng := rand.New(rand.NewPCG(uint64(n), 0x5eed))
	_, corrupt := p.active(faultCorrupt, at)
	_, loss := p.active(faultPacketLoss, at)
	_, kfLoss := p.active(faultKeyframeLoss, at)
	bump := 0
	if _, ok := p.active(faultCodecAC3, at); ok {
		bump = 1
	}
	out := make([]byte, 0, len(src))
	droppingKey := false
	for i := 0; i < len(src); i += tsPacket {
		pk := append([]byte(nil), src[i:i+tsPacket]...)
		pid := tsPID(pk)
		if kfLoss && pid == fx.VideoPID {
			if tsPUSI(pk) {
				droppingKey = tsRandomAccess(pk)
			}
			if droppingKey {
				continue
			}
		}
		if pid != 0 && pid != fx.Program.PMTPID {
			if loss && rng.IntN(50) == 0 {
				continue
			}
			if corrupt && rng.IntN(100) == 0 {
				start := 4
				if pk[3]&0x20 != 0 {
					start += 1 + int(pk[4])
				}
				for j := max(start, 20); j < tsPacket; j++ {
					pk[j] = byte(rng.IntN(256))
				}
			}
		}
		shiftPacketTime(pk, offset)
		cc.stamp(pk)
		out = append(out, pk...)
	}
	if bump != 0 {
		setPMTVersion(out, fx.Program.PMTPID, bump)
	}
	return out
}

func (p *liveProvider) count(opened bool) {
	p.mu.Lock()
	p.stats.Requests++
	if opened {
		p.stats.Opens++
	}
	p.mu.Unlock()
}

// reset aborts the client connection with a TCP RST.
func reset(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		panic(http.ErrAbortHandler)
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

func (p *liveProvider) serve(w http.ResponseWriter, r *http.Request) {
	elapsed := time.Since(p.started)
	prefix := "/" + p.cfg.Name + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	if _, ok := p.active(faultDown, elapsed); ok {
		p.count(false)
		reset(w)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	switch {
	case p.cfg.HLS && rest == "live.m3u8":
		p.count(false)
		p.servePlaylist(w, elapsed)
	case p.cfg.HLS && strings.HasPrefix(rest, "seg/"):
		p.count(false)
		n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(rest, "seg/"), ".ts"), 10, 64)
		if err != nil || n < 0 || n >= p.liveEdge(elapsed) {
			http.NotFound(w, r)
			return
		}
		if _, ok := p.active(faultRST, elapsed); ok {
			reset(w)
			return
		}
		if f, ok := p.active(faultSegmentError, p.published(n)); ok {
			http.Error(w, http.StatusText(f.Code), f.Code)
			return
		}
		if f, ok := p.active(faultSegmentSlow, elapsed); ok {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(f.At + f.For - elapsed):
			}
		}
		b := p.segment(n)
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		_, _ = w.Write(b)
	case !p.cfg.HLS && rest == "live.ts":
		p.serveTS(w, r, elapsed)
	default:
		http.NotFound(w, r)
	}
}

func (p *liveProvider) servePlaylist(w http.ResponseWriter, elapsed time.Duration) {
	edge := p.liveEdge(elapsed)
	if f, ok := p.active(faultPlaylistStall, elapsed); ok {
		edge = p.liveEdge(f.At)
	}
	first := max(edge-int64(p.cfg.Backlog), 0)
	td := int((p.segDur() + time.Second - 1) / time.Second)
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n", td, first)
	if p.cfg.Discontinuities {
		fmt.Fprintf(&b, "#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", p.spliceCount(first+1))
	}
	for n := first; n < edge; n++ {
		if p.cfg.Discontinuities && n > first && p.splice(n) {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\nseg/%d.ts\n", p.segDur().Seconds(), n)
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(b.String()))
}

func (p *liveProvider) serveTS(w http.ResponseWriter, r *http.Request, elapsed time.Duration) {
	if f, ok := p.active(faultRefuse, elapsed); ok {
		p.mu.Lock()
		p.stats.Requests++
		p.stats.Refusals++
		p.mu.Unlock()
		http.Error(w, "connection limit reached", f.Code)
		return
	}
	p.mu.Lock()
	p.stats.Requests++
	if p.cfg.ConnLimit > 0 && p.open >= p.cfg.ConnLimit {
		p.stats.Refusals++
		p.mu.Unlock()
		http.Error(w, "max connections reached", p.cfg.LimitCode)
		return
	}
	p.open++
	p.stats.Opens++
	p.stats.MaxConcurrent = max(p.stats.MaxConcurrent, p.open)
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.open--; p.mu.Unlock() }()

	w.Header().Set("Content-Type", "video/mp2t")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush() // providers answer at once even when the body then stalls
	}
	connected := elapsed
	// Start Burst behind the newest segment, like a provider's send buffer.
	n := p.liveEdge(elapsed-p.cfg.Burst) - 1
	const chunks = 20
	for {
		b := p.segment(n)
		due := p.published(n + 1)
		for c := 0; c < chunks; c++ {
			at := due - p.segDur() + time.Duration(c)*p.segDur()/chunks
			if wait := at - time.Since(p.started); wait > 0 {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(wait):
				}
			}
			now := time.Since(p.started)
			for _, f := range p.cfg.Faults {
				if f.At > connected && f.At <= now {
					switch f.Kind {
					case faultRST, faultDown:
						reset(w)
						return
					case faultEOF:
						return
					}
				}
			}
			if f, ok := p.active(faultTSStall, now); ok {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(f.At + f.For - now):
				}
				// Resume at the live edge; the stalled span is lost.
				n = p.liveEdge(time.Since(p.started)) - 2
				break
			}
			if _, err := w.Write(b[len(b)*c/chunks/tsPacket*tsPacket : len(b)*(c+1)/chunks/tsPacket*tsPacket]); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		n++
	}
}
