package livetv

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// An upstream tap is the one provider connection behind a live input.
//
// A tune reads its input several times: the sniff that tells HLS from a
// transport stream, ffprobe's codec analysis, then ffmpeg for as long as
// the viewer watches. Each read used to be its own provider connection, and
// so was every other viewer of the same channel. IPTV accounts are sold by
// connection (one, two, five) and strict panels refuse the overlap or ban
// the account for it. Every relay read of a media input now joins a tap
// keyed by the input URL and provider headers: one upstream GET whose bytes
// are kept in a ring and fanned out to every reader.
//
// A reader that joins while the start of the stream is still in the ring
// reads it from the first byte, so ffmpeg receives exactly what the probe
// saw. A later reader (another viewer) joins near the live edge. A reader
// that falls a whole ring behind skips forward to the live edge instead of
// stalling everyone else.
//
// A tap lives as long as a grant (a tune or a probe) references it, and
// closes synchronously when the last one is revoked, so a viewer changing
// channels on a one-connection account never overlaps the old and the new
// stream. It also closes after tapIdle without any reader, so an abandoned
// grant cannot hold the provider connection.
//
// Manifests (HLS, DASH) are not tapped: their reads are short requests.

var (
	// tapRing is the per-connection buffer: about 30 s at 4K bitrates.
	tapRing = 24 << 20
	// tapJoinBacklog is how far behind the live edge a late reader starts,
	// enough for ffmpeg to find a keyframe without waiting for one.
	tapJoinBacklog = 2 << 20
	tapIdle        = 20 * time.Second
)

// tsPacket is the MPEG-TS packet size; join points stay packet aligned.
const tsPacket = 188

// tapBusyHeader marks a relay refusal because the provider's connections
// are all in use, so the tune can say so.
const tapBusyHeader = "X-Ether-Provider-Busy"

// ErrProviderBusy reports that every connection the provider allows is in
// use by other channels.
var ErrProviderBusy = fmt.Errorf("every connection this IPTV provider allows is in use: %w", core.ErrStreamLimit)

// ErrProviderPaced reports that the source's hourly allowance of new
// provider streams is spent.
var ErrProviderPaced = fmt.Errorf("this IPTV source has started as many streams this hour as its owner allows: %w", core.ErrStreamLimit)

type tapRegistry struct {
	mu       sync.Mutex
	byKey    map[string]*upstreamTap
	bySource map[string]map[*upstreamTap]struct{}
	pace     map[string]*sourcePace
}

// sourcePace spaces a source's new provider streams.
type sourcePace struct {
	next  time.Time   // earliest start of the next stream
	opens []time.Time // starts in the last hour
}

// reserveOpen books the source's next stream start and returns how long the
// caller waits for it. A spent hourly allowance refuses the stream.
func (r *tapRegistry) reserveOpen(source string, gap time.Duration, perHour int, now time.Time) (time.Duration, error) {
	if source == "" {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pace == nil {
		r.pace = map[string]*sourcePace{}
	}
	p := r.pace[source]
	if p == nil {
		p = &sourcePace{}
		r.pace[source] = p
	}
	kept := p.opens[:0]
	for _, at := range p.opens {
		if now.Sub(at) < time.Hour {
			kept = append(kept, at)
		}
	}
	p.opens = kept
	if perHour > 0 && len(p.opens) >= perHour {
		return 0, ErrProviderPaced
	}
	start := now
	if p.next.After(start) {
		start = p.next
	}
	p.next = start.Add(gap)
	p.opens = append(p.opens, start)
	return start.Sub(now), nil
}

type upstreamTap struct {
	key, source string
	reg         *tapRegistry

	ready chan struct{} // closed once the first response is known
	// Set before ready closes and immutable after.
	status     int
	header     http.Header
	base       *url.URL
	isManifest bool
	manifest   []byte // a manifest body, handed to the first reader only
	hls        bool
	ctype      string
	openErr    error

	mu      sync.Mutex
	cond    *sync.Cond
	ring    []byte
	written int64 // bytes received from upstream
	err     error // set when upstream ends
	closed  bool
	refs    map[*inputGrant]struct{}
	readers int
	idle    *time.Timer
	cancel  context.CancelFunc
	body    io.Closer
}

func tapKey(raw string, h http.Header) string {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sort.Strings(names)
	sum := sha256.New()
	io.WriteString(sum, raw)
	for _, k := range names {
		fmt.Fprintf(sum, "\x00%s\x00%s", k, strings.Join(h[k], "\x01"))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// acquire joins the tap for key, creating it when none is open. created
// reports that the caller must open it. A capped source refuses a new tap
// once its limit of open taps is reached.
func (r *tapRegistry) acquire(key, source string, limit int, g *inputGrant) (t *upstreamTap, created bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byKey == nil {
		r.byKey, r.bySource = map[string]*upstreamTap{}, map[string]map[*upstreamTap]struct{}{}
	}
	if t = r.byKey[key]; t != nil {
		t.mu.Lock()
		closed := t.closed
		if !closed {
			t.refs[g] = struct{}{}
		}
		t.mu.Unlock()
		if !closed {
			return t, false, nil
		}
	}
	if source != "" && limit > 0 && len(r.bySource[source]) >= limit {
		return nil, false, ErrProviderBusy
	}
	t = &upstreamTap{key: key, source: source, reg: r, ready: make(chan struct{}), refs: map[*inputGrant]struct{}{g: {}}}
	t.cond = sync.NewCond(&t.mu)
	r.byKey[key] = t
	if source != "" {
		if r.bySource[source] == nil {
			r.bySource[source] = map[*upstreamTap]struct{}{}
		}
		r.bySource[source][t] = struct{}{}
	}
	return t, true, nil
}

// release drops grant g from every tap; a tap without grants closes now.
func (r *tapRegistry) release(g *inputGrant) {
	r.mu.Lock()
	var done []*upstreamTap
	for _, t := range r.byKey {
		t.mu.Lock()
		if _, ok := t.refs[g]; ok {
			delete(t.refs, g)
			if len(t.refs) == 0 {
				done = append(done, t)
			}
		}
		t.mu.Unlock()
	}
	r.mu.Unlock()
	for _, t := range done {
		t.close(nil)
	}
}

// open reports how many taps a source has open (tests and status).
func (r *tapRegistry) open(source string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bySource[source])
}

func (r *tapRegistry) forget(t *upstreamTap) {
	r.mu.Lock()
	if r.byKey[t.key] == t {
		delete(r.byKey, t.key)
	}
	if set := r.bySource[t.source]; set != nil {
		delete(set, t)
		if len(set) == 0 {
			delete(r.bySource, t.source)
		}
	}
	r.mu.Unlock()
}

// close ends the upstream connection and wakes every reader. It runs
// synchronously: when it returns the provider connection is closed.
func (t *upstreamTap) close(err error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	if t.err == nil {
		t.err = err
		if t.err == nil {
			t.err = io.EOF
		}
	}
	body, cancel, idle := t.body, t.cancel, t.idle
	t.body = nil
	t.cond.Broadcast()
	t.mu.Unlock()
	if idle != nil {
		idle.Stop()
	}
	if cancel != nil {
		cancel()
	}
	if body != nil {
		body.Close()
	}
	t.reg.forget(t)
}

// fill copies the upstream body into the ring until it ends.
func (t *upstreamTap) fill(body io.Reader) {
	buf := make([]byte, 64<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			t.mu.Lock()
			if t.closed {
				t.mu.Unlock()
				return
			}
			t.writeLocked(buf[:n])
			t.cond.Broadcast()
			t.mu.Unlock()
		}
		if err != nil {
			t.close(err)
			return
		}
	}
}

func (t *upstreamTap) writeLocked(p []byte) {
	if t.ring == nil {
		t.ring = make([]byte, tapRing)
	}
	for len(p) > 0 {
		at := int(t.written % int64(len(t.ring)))
		n := copy(t.ring[at:], p)
		t.written += int64(n)
		p = p[n:]
	}
}

// joinOffset is where a new reader starts: the first byte while it is still
// buffered, otherwise a packet-aligned point tapJoinBacklog behind the edge.
func (t *upstreamTap) joinOffsetLocked() int64 {
	if t.written <= int64(tapRing) {
		return 0
	}
	at := t.written - int64(tapJoinBacklog)
	return at - at%tsPacket
}

// read copies bytes at offset into p, waiting for data. It returns the new
// offset. A reader a full ring behind skips forward.
func (t *upstreamTap) read(ctx context.Context, off int64, p []byte) (int, int64, error) {
	stop := context.AfterFunc(ctx, func() { t.mu.Lock(); t.cond.Broadcast(); t.mu.Unlock() })
	defer stop()
	t.mu.Lock()
	defer t.mu.Unlock()
	for off >= t.written && !t.closed && ctx.Err() == nil {
		t.cond.Wait()
	}
	if ctx.Err() != nil {
		return 0, off, ctx.Err()
	}
	if off >= t.written {
		return 0, off, t.err
	}
	if t.written-off > int64(len(t.ring)) {
		off = t.joinOffsetLocked()
	}
	at := int(off % int64(len(t.ring)))
	n := min(int(t.written-off), len(p), len(t.ring)-at)
	copy(p, t.ring[at:at+n])
	return n, off + int64(n), nil
}

func (t *upstreamTap) attach() {
	t.mu.Lock()
	t.readers++
	if t.idle != nil {
		t.idle.Stop()
	}
	t.mu.Unlock()
}

func (t *upstreamTap) detach() {
	t.mu.Lock()
	t.readers--
	if t.readers == 0 && !t.closed {
		if t.idle == nil {
			t.idle = time.AfterFunc(tapIdle, func() {
				t.mu.Lock()
				idle := t.readers == 0
				t.mu.Unlock()
				if idle {
					t.close(nil)
				}
			})
		} else {
			t.idle.Reset(tapIdle)
		}
	}
	t.mu.Unlock()
}

// tapSource names the provider account behind a grant and its connection
// limit; "" when the input does not belong to a capped source.
func (s *Service) tapSource(g *inputGrant) (string, int) {
	if g.source == "" {
		return "", 0
	}
	return g.source, s.sourceConnectionLimit(g.source)
}

// serveTapped serves a media input through its tap. It reports false when
// the input turned out to be a manifest this reader must fetch itself.
func (s *Service) serveTapped(w http.ResponseWriter, r *http.Request, g *inputGrant, raw string) bool {
	// A byte range is a seek into a file input, not a live read.
	if rng := r.Header.Get("Range"); rng != "" && rng != "bytes=0-" {
		return false
	}
	source, limit := s.tapSource(g)
	t, created, err := s.inputRelay.taps.acquire(tapKey(raw+"\x00"+tapKey("", g.mediaHeaders), g.headers), source, limit, g)
	if err != nil {
		w.Header().Set(tapBusyHeader, "1")
		http.Error(w, "provider connections in use", http.StatusServiceUnavailable)
		return true
	}
	if created {
		hdr := r.Header.Clone()
		hdr.Del("Range")
		hdr.Del("If-Range")
		s.openTap(t, g, raw, hdr, r.Context())
	}
	select {
	case <-t.ready:
	case <-r.Context().Done():
		return true
	}
	if t.openErr != nil || t.status != http.StatusOK && t.status != http.StatusPartialContent {
		code := t.status
		if code < 400 || code > 599 {
			code = http.StatusBadGateway
		}
		if errors.Is(t.openErr, context.DeadlineExceeded) {
			code = http.StatusGatewayTimeout
		}
		http.Error(w, "upstream unavailable", code)
		return true
	}
	if t.isManifest {
		if body := t.takeManifest(); body != nil {
			s.serveRelayManifest(w, g, t.base, bytes.NewReader(body), t.hls)
			return true
		}
		return false
	}
	if mt, params, err := mime.ParseMediaType(t.header.Get("Content-Type")); err == nil {
		w.Header().Set("Content-Type", mime.FormatMediaType(mt, params))
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	t.attach()
	defer t.detach()
	t.mu.Lock()
	off := t.joinOffsetLocked()
	t.mu.Unlock()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer context.AfterFunc(g.ctx, cancel)()
	// Wake this reader's wait when its request ends.
	defer context.AfterFunc(ctx, func() {
		t.mu.Lock()
		t.cond.Broadcast()
		t.mu.Unlock()
	})()
	buf := make([]byte, 64<<10)
	for {
		n, next, err := t.read(ctx, off, buf)
		off = next
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return true
			}
			_ = rc.Flush()
		}
		if err != nil {
			return true
		}
	}
}

func (t *upstreamTap) takeManifest() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.manifest
	t.manifest = nil
	return b
}

// openContext is the tap's own context with the opening reader's deadline:
// dialling and the wait for response headers honour the tune's startup
// budget, while the stream that follows outlives it.
type openContext struct {
	context.Context
	deadline time.Time
}

func (c openContext) Deadline() (time.Time, bool) { return c.deadline, true }

// openTap makes the tap's one upstream request and starts filling the ring.
// opener is the first reader's context: its deadline bounds the connect,
// and the open is abandoned if it ends first.
func (s *Service) openTap(t *upstreamTap, g *inputGrant, raw string, hdr http.Header, opener context.Context) {
	started := time.Now()
	ctx, cancel := context.WithCancel(s.ctx)
	t.mu.Lock()
	t.cancel = cancel
	t.mu.Unlock()
	reqCtx := ctx
	if dl, ok := opener.Deadline(); ok {
		reqCtx = openContext{Context: ctx, deadline: dl}
	}
	stop := context.AfterFunc(opener, cancel)
	defer stop() // initial body inspection is part of opening the stream
	var res *http.Response
	var err error
	if g.source != "" {
		e, _ := s.sourceEntry(ctx, g.source)
		var wait time.Duration
		if wait, err = t.reg.reserveOpen(g.source, e.gap, e.perHour, time.Now()); err == nil && wait > 0 {
			if g.log != nil {
				g.log.Info("live provider tune gap", "wait", wait)
			}
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				err = context.Cause(ctx)
			}
		}
	}
	if err == nil {
		res, err = s.relayFetch(reqCtx, cancel, g, raw, &http.Request{Header: hdr})
	}
	if opener.Err() != nil && err == nil {
		// The opener left while headers arrived; nobody asked for this stream.
		res.Body.Close()
		err = opener.Err()
	}
	if err != nil {
		if g.log != nil {
			g.log.Debug("live upstream open failed", append(Diagnose("upstream_headers", err).LogAttrs(), "elapsed", time.Since(started))...)
		}
		t.openErr = err
		close(t.ready)
		t.close(err)
		return
	}
	t.status, t.header, t.base = res.StatusCode, res.Header.Clone(), res.Request.URL
	t.ctype = res.Header.Get("Content-Type")
	if g.log != nil {
		g.log.Debug("live upstream headers", "status", res.StatusCode, "elapsed", time.Since(started))
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent {
		res.Body.Close()
		close(t.ready)
		t.close(nil)
		return
	}
	idle := time.AfterFunc(relayIOTimeout, cancel)
	body := bufio.NewReaderSize(&idleReader{r: res.Body, t: idle}, 32<<10)
	head := sniffHead(body)
	ctype := strings.ToLower(res.Header.Get("Content-Type"))
	t.hls = bytes.HasPrefix(head, []byte("#EXTM3U"))
	mpd := bytes.HasPrefix(head, []byte("<?xml")) || bytes.HasPrefix(head, []byte("<MPD")) ||
		(bytes.HasPrefix(head, []byte("<")) && strings.Contains(ctype, "xml"))
	if t.hls || mpd {
		t.isManifest = true
		b, err := io.ReadAll(io.LimitReader(body, relayManifestLimit+1))
		idle.Stop()
		res.Body.Close()
		if err != nil || len(b) > relayManifestLimit {
			t.openErr = errRelayUnsupported
		} else {
			t.manifest = b
		}
		t.header = nil
		close(t.ready)
		t.close(nil)
		return
	}
	t.mu.Lock()
	t.body = res.Body
	t.ring = make([]byte, tapRing)
	t.mu.Unlock()
	close(t.ready)
	go func() { defer idle.Stop(); t.fill(body) }()
}

// sniffInput reads the first bytes of the session's input through its tap,
// inside the tune's budget, and reports the response's content type.
// It joins (or opens) the same provider connection the probe and ffmpeg
// read from.
func (s *Service) sniffInput(ctx context.Context, ss *session, n int) ([]byte, string, error) {
	r := &s.inputRelay
	r.mu.Lock()
	g := r.bySess[ss]
	r.mu.Unlock()
	if g == nil || g.ctx.Err() != nil {
		return nil, "", core.ErrNotAvailable
	}
	g.mu.Lock()
	raw := g.resources["0"].url
	g.mu.Unlock()
	source, limit := s.tapSource(g)
	t, created, err := r.taps.acquire(tapKey(raw+"\x00"+tapKey("", g.mediaHeaders), g.headers), source, limit, g)
	if err != nil {
		return nil, "", err
	}
	if created {
		s.openTap(t, g, raw, http.Header{"User-Agent": {defaultUserAgent()}}, ctx)
	}
	select {
	case <-t.ready:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	if t.openErr != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", Diagnose("upstream_headers", t.openErr)
	}
	if t.status != http.StatusOK && t.status != http.StatusPartialContent {
		return nil, "", providerResponseError(t.status)
	}
	if t.isManifest {
		t.takeManifest()
		if t.hls {
			return []byte("#EXTM3U"), t.ctype, nil
		}
		return []byte("<?xml"), t.ctype, nil
	}
	t.attach()
	defer t.detach()
	out := make([]byte, 0, n)
	var off int64
	for len(out) < n {
		buf := make([]byte, n-len(out))
		got, next, err := t.read(ctx, off, buf)
		out, off = append(out, buf[:got]...), next
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			break
		}
	}
	return out, t.ctype, nil
}
