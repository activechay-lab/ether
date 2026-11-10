// Package listcache keeps whole-library listings encoded on disk, so a
// protocol whose clients download a library in one response (Xtream's
// get_vod_streams) streams a file instead of reading, authorizing, signing
// and encoding the library again.
//
// A listing fills a slot (what it lists, for whom, under which origin) and
// is stamped with its State: an exact stamp of every input but catalogue
// content, the catalogue generation it was read at, and whether the
// viewer's policy judges titles on their facts. A listing is served only to
// a request with the same stamp. Catalogue writes are bounded staleness: a
// listing read before one is served for up to Stale, and rebuilt behind the
// response, unless its policy is Judged; then the write retires it at once.
//
// With no servable listing a request follows a build as it is written: the
// one in flight for its slot when that build reads what the request must
// see, otherwise one it starts. A follower receives bytes as the build
// writes them, so it never waits longer than encoding the library itself.
//
// Listings are stamped with a process's generations and none outlives it:
// the directory is emptied when a store opens, and its size is capped.
package listcache

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend/kit/reqmemo"
)

const (
	// Stale is the longest a listing is served after it was read.
	Stale = 30 * time.Minute
	// Revalidate is the age, or a catalogue write, after which a served
	// listing is rebuilt behind the response.
	Revalidate = 2 * time.Minute
	// rebuildFloor paces write-driven revalidation per slot: the first
	// catalogue write after a listing rebuilds it behind the response,
	// but further writes within the floor coalesce into one rebuild at
	// the floor. Without it a catalogue scan, whose writes move the
	// generation under every poll, queues another whole-library build
	// behind every response for as long as the scan runs.
	rebuildFloor = 30 * time.Second
	// maxBuilds bounds listings encoding at once across every slot, so a
	// burst of cold slots cannot fan out into unbounded parallel builds.
	maxBuilds = 2
	// RenewEvery is how often a listing's artwork is re-armed for one
	// credential. Image grants live ten minutes.
	RenewEvery = 4 * time.Minute
	// Idle drops a listing nobody fetched for this long.
	Idle = 24 * time.Hour
	// buildLimit bounds one build.
	buildLimit = 15 * time.Minute
	// diskCap bounds the listings kept on disk; one listing may use half.
	diskCap = 1 << 30
	// diskPause stops building listings after a cache write fails.
	diskPause = 10 * time.Minute
)

// State is what a listing was read under, or what a request needs.
type State struct {
	// Stamp covers every input but catalogue content: equal stamps mean
	// the same policy, library and encoding.
	Stamp string
	// Catalog is the catalogue generation; Judged makes it exact.
	Catalog uint64
	Judged  bool
	At      time.Time
}

// Request is one list request.
type Request struct {
	Slot  string
	State State
	// Owner names the requesting credential (its device). A listing's
	// artwork is renewed for each owner at most once per RenewEvery.
	Owner string
	// Build encodes the listing as this request would, writing its body to
	// the entry and its artwork with KeepImages. It runs at most once,
	// after the request may have returned, under a context of its own.
	Build func(context.Context, *Entry) error
	// Renew re-arms a listing's artwork for Owner. Nil skips it.
	Renew func(context.Context, *Entry) error
}

// Entry is one listing: written by its build, then served until it is
// retired. Its files are removed once it is retired and nobody reads them.
type Entry struct {
	s        *Store
	slot     string
	state    State
	id       string
	owner    string
	build    func(context.Context, *Entry) error
	openDone chan struct{}
	openOK   bool
	started  atomic.Bool
	body     *os.File
	side     *os.File
	images   *bufio.Writer
	keepMu   sync.Mutex

	mu      sync.Mutex
	wake    chan struct{}
	written int64
	done    bool
	err     error

	// Under s.mu.
	refs     int
	retired  bool
	ready    bool
	gz       bool
	usage    int64
	finished time.Time
	lastUsed time.Time
	renewed  map[string]time.Time
}

// use remembers whether a slot's last speculative build was fetched, so a
// client that signs in often but never lists does not rebuild it.
type use struct{ served, prepared time.Time }

// Store is one frontend's listings.
type Store struct {
	dir    string
	now    func() time.Time
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	busy   atomic.Int64
	// spec runs one speculative build or compression at a time.
	spec chan struct{}
	// builds bounds every encoding build, foreground included.
	builds chan struct{}

	mu       sync.Mutex
	ready    map[string]*Entry
	building map[string]*Entry
	uses     map[string]*use
	used     int64
	paused   time.Time
	swept    time.Time
	closed   bool
}

// New opens a store in dir, emptying it. It returns nil, and callers
// encode every listing, when dir is empty or unusable.
func New(dir string) *Store {
	if dir == "" {
		return nil
	}
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Store{dir: dir, now: time.Now, ctx: ctx, cancel: cancel, spec: make(chan struct{}, 1), builds: make(chan struct{}, maxBuilds),
		ready: map[string]*Entry{}, building: map[string]*Entry{}, uses: map[string]*use{}}
}

// Close stops builds and waits for the store's background work.
func (s *Store) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

// Now is the store's clock, for request states.
func (s *Store) Now() time.Time { return s.now() }

// Busy counts builds, compressions and Go work in flight.
func (s *Store) Busy() int {
	if s == nil {
		return 0
	}
	return int(s.busy.Load())
}

// Go runs fn behind a response, counted in Busy and stopped by Close.
func (s *Store) Go(fn func(context.Context)) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.busy.Add(1)
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer s.busy.Add(-1)
		ctx, cancel := context.WithTimeout(s.ctx, time.Minute)
		defer cancel()
		fn(reqmemo.With(ctx))
	}()
}

func (s *Store) path(id, suffix string) string { return filepath.Join(s.dir, id+suffix) }

// servable reports whether e may answer a request made under now.
func servable(e *Entry, now State) bool {
	return e.ready && !e.retired && now.At.Sub(e.state.At) <= Stale && followable(e, now)
}

// followable reports whether a request made under now may take e's bytes:
// e reads the same policy and every catalogue write the request must see.
func followable(e *Entry, now State) bool {
	return e.state.Stamp == now.Stamp && (e.state.Catalog == now.Catalog || !e.state.Judged && !now.Judged)
}

// Serve answers req from a listing, or by following its build. It reports
// false, having written nothing, when the store cannot; the caller then
// encodes the listing itself.
func (s *Store) Serve(w http.ResponseWriter, r *http.Request, req Request) bool {
	if s == nil {
		return false
	}
	ready, follow, created, stale := s.acquire(req)
	if ready != nil {
		defer s.release(ready)
		if stale {
			s.Prepare(req)
		}
		s.renew(r.Context(), req, ready)
		return s.serveFile(w, r, ready)
	}
	if follow == nil {
		return false
	}
	defer s.release(follow)
	if created {
		if !s.open(follow) {
			return false
		}
	} else {
		<-follow.openDone
		if !follow.openOK {
			return false
		}
	}
	s.start(follow)
	s.follow(w, r, req, follow)
	return true
}

// Prepare builds req's listing behind the response when no fresh listing
// or matching build exists and the slot's last speculative build was
// fetched (or is a day old).
func (s *Store) Prepare(req Request) {
	if s == nil {
		return
	}
	e := s.beginSpeculative(req)
	if e == nil {
		return
	}
	defer s.release(e)
	if s.open(e) {
		s.queue(e)
	}
}

// rebuildDueLocked reports whether listing e should be rebuilt behind a
// response made under now: it aged out, or the catalogue moved and no
// rebuild for this listing was triggered within rebuildFloor. The first
// write after a listing is due at once; a write stream coalesces.
// s.mu is held.
func (s *Store) rebuildDueLocked(e *Entry, now State) bool {
	if now.At.Sub(e.state.At) >= Revalidate {
		return true
	}
	if e.state.Catalog == now.Catalog {
		return false
	}
	u := s.uses[e.slot]
	return u == nil || u.prepared.Before(e.state.At) || !now.At.Before(u.prepared.Add(rebuildFloor))
}

// acquire returns a servable listing or a build to follow (registering one
// atomically under s.mu when none is in flight), each with a reference the
// caller releases. created reports that the caller owns opening follow's
// files; stale reports a ready listing to rebuild behind the response.
func (s *Store) acquire(req Request) (ready, follow *Entry, created, stale bool) {
	now := req.State
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.ready[req.Slot]; e != nil && servable(e, now) {
		e.refs++
		e.lastUsed = now.At
		s.servedLocked(req.Slot, now.At)
		b := s.building[req.Slot]
		stale = s.rebuildDueLocked(e, now) && (b == nil || !followable(b, now))
		return e, nil, false, stale
	}
	if b := s.building[req.Slot]; b != nil && followable(b, now) {
		b.refs++
		s.servedLocked(req.Slot, now.At)
		return nil, b, false, false
	}
	e := s.registerLocked(req, false)
	if e != nil {
		s.servedLocked(req.Slot, now.At)
	}
	return nil, e, e != nil, false
}

func (s *Store) servedLocked(slot string, at time.Time) {
	u := s.uses[slot]
	if u == nil {
		u = &use{}
		s.uses[slot] = u
	}
	u.served = at
}

func (s *Store) beginSpeculative(req Request) *Entry {
	now := req.State
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || now.At.Before(s.paused) || req.Build == nil {
		return nil
	}
	if b := s.building[req.Slot]; b != nil && followable(b, now) {
		return nil
	}
	if e := s.ready[req.Slot]; e != nil && servable(e, now) && !s.rebuildDueLocked(e, now) {
		return nil
	}
	u := s.uses[req.Slot]
	if u != nil && !u.prepared.IsZero() && u.served.Before(u.prepared) && now.At.Sub(u.prepared) < Idle {
		return nil
	}
	return s.registerLocked(req, true)
}

// registerLocked claims req.Slot's in-flight build under s.mu before its
// files are opened, so concurrent callers follow or skip it instead of
// racing into separate whole-library builds. The caller holds one reference
// and the build another.
func (s *Store) registerLocked(req Request, speculative bool) *Entry {
	if req.Build == nil || s.closed || req.State.At.Before(s.paused) {
		return nil
	}
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil
	}
	if speculative {
		u := s.uses[req.Slot]
		if u == nil {
			u = &use{}
			s.uses[req.Slot] = u
		}
		u.prepared = req.State.At
	}
	e := &Entry{
		s: s, slot: req.Slot, state: req.State, id: hex.EncodeToString(raw[:]),
		owner: req.Owner, build: req.Build,
		openDone: make(chan struct{}), wake: make(chan struct{}),
		refs: 2, renewed: map[string]time.Time{},
	}
	s.building[req.Slot] = e
	s.busy.Add(1)
	s.wg.Add(1)
	return e
}

// open creates e's files outside s.mu and releases any followers waiting on
// openDone. On failure it retires the reservation and drops the build's
// reference so callers fall back to encoding directly.
func (s *Store) open(e *Entry) bool {
	var err error
	e.body, err = os.OpenFile(s.path(e.id, ".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		e.side, err = os.OpenFile(s.path(e.id, ".img"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}
	if err != nil {
		e.closeFiles()
		e.removeFiles()
		s.pause()
		s.abortOpen(e)
		return false
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		e.closeFiles()
		e.removeFiles()
		s.abortOpen(e)
		return false
	}
	e.images = bufio.NewWriterSize(e.side, 64<<10)
	e.openOK = true
	close(e.openDone)
	return true
}

func (s *Store) abortOpen(e *Entry) {
	s.mu.Lock()
	if s.building[e.slot] == e {
		delete(s.building, e.slot)
	}
	e.retired = true
	s.releaseLocked(e)
	s.mu.Unlock()
	close(e.openDone)
	s.busy.Add(-1)
	s.wg.Done()
}

func (s *Store) pause() {
	s.mu.Lock()
	s.paused = s.now().Add(diskPause)
	s.mu.Unlock()
}

// start runs e's build now, unless something already started it.
func (s *Store) start(e *Entry) {
	if e.started.CompareAndSwap(false, true) {
		go s.run(e)
	}
}

// queue runs e's build when no other speculative work is running, unless a
// request starts it first.
func (s *Store) queue(e *Entry) {
	go func() {
		select {
		case s.spec <- struct{}{}:
			defer func() { <-s.spec }()
		case <-s.ctx.Done():
		}
		if e.started.CompareAndSwap(false, true) {
			s.run(e)
		}
	}()
}

func (s *Store) run(e *Entry) {
	defer s.wg.Done()
	defer s.busy.Add(-1)
	ctx, cancel := context.WithTimeout(s.ctx, buildLimit)
	err := ctx.Err()
	if err == nil {
		// Bound encoding across every slot: followers wait on this
		// build either way, so queuing here only delays their first
		// byte when the machine is already saturated.
		select {
		case s.builds <- struct{}{}:
			err = e.safeBuild(reqmemo.With(ctx))
			<-s.builds
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	cancel()
	if err == nil {
		if err = e.images.Flush(); err != nil {
			err = &diskError{err}
		}
	}
	e.closeFiles()
	var size int64
	if err == nil {
		size = e.fileSize(".json") + e.fileSize(".img")
	}
	e.mu.Lock()
	e.done, e.err = true, err
	close(e.wake)
	e.mu.Unlock()
	s.mu.Lock()
	if s.building[e.slot] == e {
		delete(s.building, e.slot)
	}
	e.usage = size
	s.used += size
	committed := false
	if err == nil && !s.closed && size <= diskCap/2 {
		if old := s.ready[e.slot]; old == nil || !old.state.At.After(e.state.At) {
			if old != nil {
				s.retireLocked(old)
			}
			e.ready, e.finished, e.lastUsed = true, s.now(), s.now()
			// The build signed the artwork under its own credential.
			e.renewed[e.owner] = e.state.At
			s.ready[e.slot] = e
			committed = true
		}
	}
	if !committed {
		s.retireLocked(e)
	}
	s.sweepLocked()
	if committed {
		e.refs++ // the compression's
		s.busy.Add(1)
		s.wg.Add(1)
	}
	s.releaseLocked(e)
	s.mu.Unlock()
	if errors.As(err, new(*diskError)) {
		s.pause()
	}
	if committed {
		go s.compress(e)
	}
}

// safeBuild runs the build. Outside a request goroutine nothing recovers a
// panic, so one fails this listing as it would have failed the request.
func (e *Entry) safeBuild(ctx context.Context) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("listing cache: build panicked: %v", p)
		}
	}()
	return e.build(ctx, e)
}

// compress writes e's gzip encoding behind it, for clients that accept one.
func (s *Store) compress(e *Entry) {
	defer s.wg.Done()
	defer s.busy.Add(-1)
	ok := false
	select {
	case s.spec <- struct{}{}:
		ok = e.writeGzip(s.ctx) == nil
		<-s.spec
	case <-s.ctx.Done():
	}
	size := e.fileSize(".json.gz")
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok && !e.retired {
		e.gz = true
		e.usage += size
		s.used += size
		s.sweepLocked()
	} else {
		_ = os.Remove(s.path(e.id, ".json.gz"))
	}
	s.releaseLocked(e)
}

func (e *Entry) writeGzip(ctx context.Context) error {
	in, err := os.Open(e.s.path(e.id, ".json"))
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(e.s.path(e.id, ".json.gz"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	zw, _ := gzip.NewWriterLevel(out, gzip.BestSpeed)
	buf := make([]byte, 256<<10)
	for err == nil {
		if err = ctx.Err(); err != nil {
			break
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err = zw.Write(buf[:n]); err != nil {
				break
			}
		}
		if rerr == io.EOF {
			err = zw.Close()
			break
		}
		err = rerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

func (e *Entry) fileSize(suffix string) int64 {
	fi, err := os.Stat(e.s.path(e.id, suffix))
	if err != nil {
		return 0
	}
	return fi.Size()
}

func (e *Entry) closeFiles() {
	if e.body != nil {
		e.body.Close()
	}
	if e.side != nil {
		e.side.Close()
	}
}

func (e *Entry) removeFiles() {
	for _, suffix := range []string{".json", ".img", ".json.gz"} {
		_ = os.Remove(e.s.path(e.id, suffix))
	}
}

func (s *Store) release(e *Entry) {
	s.mu.Lock()
	s.releaseLocked(e)
	s.mu.Unlock()
}

func (s *Store) releaseLocked(e *Entry) {
	e.refs--
	if e.refs == 0 && e.retired {
		e.removeFiles()
		s.used -= e.usage
		e.usage = 0
	}
}

// retireLocked stops serving e; its files go with its last reader.
func (s *Store) retireLocked(e *Entry) {
	if e.retired {
		return
	}
	e.retired = true
	if s.ready[e.slot] == e {
		delete(s.ready, e.slot)
	}
	if e.refs == 0 {
		e.removeFiles()
		s.used -= e.usage
		e.usage = 0
	}
}

// sweepLocked retires idle listings, then the least recently fetched while
// the store is over its cap, and hourly forgets old slots and stray files.
func (s *Store) sweepLocked() {
	now := s.now()
	for _, e := range s.ready {
		if now.Sub(e.lastUsed) >= Idle {
			s.retireLocked(e)
		}
	}
	for s.used > diskCap {
		var oldest *Entry
		for _, e := range s.ready {
			if oldest == nil || e.lastUsed.Before(oldest.lastUsed) {
				oldest = e
			}
		}
		if oldest == nil {
			break
		}
		s.retireLocked(oldest)
	}
	if now.Sub(s.swept) < time.Hour {
		return
	}
	s.swept = now
	for slot, u := range s.uses {
		if now.Sub(u.served) >= 2*Idle && now.Sub(u.prepared) >= 2*Idle {
			delete(s.uses, slot)
		}
	}
	// Files a reader held open when they were retired, on systems that
	// cannot remove an open file.
	known := map[string]bool{}
	for _, m := range []map[string]*Entry{s.ready, s.building} {
		for _, e := range m {
			known[e.id] = true
		}
	}
	files, _ := os.ReadDir(s.dir)
	for _, d := range files {
		id, _, _ := strings.Cut(d.Name(), ".")
		if info, err := d.Info(); err == nil && !known[id] && now.Sub(info.ModTime()) >= Idle {
			_ = os.Remove(filepath.Join(s.dir, d.Name()))
		}
	}
}

// diskError is a failed write of a listing file, not a failed read.
type diskError struct{ err error }

func (e *diskError) Error() string { return "listing cache: " + e.err.Error() }
func (e *diskError) Unwrap() error { return e.err }

// Write appends to the listing body and wakes its followers.
func (e *Entry) Write(p []byte) (int, error) {
	n, err := e.body.Write(p)
	if n > 0 {
		e.mu.Lock()
		e.written += int64(n)
		close(e.wake)
		e.wake = make(chan struct{})
		e.mu.Unlock()
	}
	if err != nil {
		return n, &diskError{err}
	}
	return n, nil
}

// renew runs req.Renew for its owner at most once per RenewEvery.
func (s *Store) renew(ctx context.Context, req Request, e *Entry) {
	if req.Renew == nil {
		return
	}
	now := s.now()
	s.mu.Lock()
	if at, ok := e.renewed[req.Owner]; ok && now.Sub(at) < RenewEvery {
		s.mu.Unlock()
		return
	}
	if len(e.renewed) >= 256 {
		clear(e.renewed)
	}
	e.renewed[req.Owner] = now
	s.mu.Unlock()
	if err := req.Renew(ctx, e); err != nil {
		s.mu.Lock()
		delete(e.renewed, req.Owner)
		s.mu.Unlock()
	}
}

func headers(w http.ResponseWriter, etag string) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	// Clients may keep a listing and revalidate it by its tag.
	h.Set("Cache-Control", "private, no-cache")
	h.Set("Vary", "Accept-Encoding")
	h.Set("ETag", etag)
}

// serveFile writes a finished listing, gzip-encoded when the client
// accepts it, honouring conditional and range requests.
func (s *Store) serveFile(w http.ResponseWriter, r *http.Request, e *Entry) bool {
	s.mu.Lock()
	gz := e.gz && acceptsGzip(r.Header.Get("Accept-Encoding"))
	s.mu.Unlock()
	suffix, etag := ".json", `"`+e.id+`"`
	if gz {
		suffix, etag = ".json.gz", `"`+e.id+`-gz"`
	}
	f, err := os.Open(s.path(e.id, suffix))
	if err != nil {
		return false
	}
	defer f.Close()
	headers(w, etag)
	if gz {
		w.Header().Set("Content-Encoding", "gzip")
	}
	http.ServeContent(w, r, "", e.finished, f)
	return true
}

// follow streams b's body as its build writes it. A build that fails before
// its first byte answers 503; after it, the response is cut short, as an
// encoding request's would be.
func (s *Store) follow(w http.ResponseWriter, r *http.Request, req Request, b *Entry) {
	f, err := os.Open(s.path(b.id, ".json"))
	if err != nil {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return
	}
	defer f.Close()
	buf := make([]byte, 256<<10)
	var sent int64
	for {
		b.mu.Lock()
		written, done, berr, wake := b.written, b.done, b.err, b.wake
		b.mu.Unlock()
		if sent < written {
			n, rerr := f.ReadAt(buf[:min(int64(len(buf)), written-sent)], sent)
			if n > 0 {
				if sent == 0 {
					headers(w, `"`+b.id+`"`)
				}
				if _, err := w.Write(buf[:n]); err != nil {
					return
				}
				sent += int64(n)
				continue
			}
			if rerr != nil && rerr != io.EOF {
				berr, done = rerr, true
			}
		}
		if done {
			if berr != nil {
				if sent == 0 {
					http.Error(w, "Unavailable", http.StatusServiceUnavailable)
					return
				}
				panic(http.ErrAbortHandler)
			}
			if sent < written {
				continue
			}
			if req.Owner != b.owner {
				s.renew(r.Context(), req, b)
			}
			return
		}
		select {
		case <-wake:
		case <-r.Context().Done():
			return
		}
	}
}

// acceptsGzip reports an Accept-Encoding that admits gzip.
func acceptsGzip(header string) bool {
	wildcard := false
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				q = f
			}
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "gzip", "x-gzip":
			return q > 0
		case "*":
			wildcard = q > 0
		}
	}
	return wildcard
}
