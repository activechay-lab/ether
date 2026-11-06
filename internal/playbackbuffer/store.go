// Package playbackbuffer retains completed encoder output for a surviving delivery
// controller. It owns neither encoder lifecycle nor client playback state.
package playbackbuffer

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"
)

type Kind uint8

const (
	Segment Kind = iota
	Init
)

type Format uint8

const (
	MPEGTS Format = iota
	FMP4
)

// Key identifies bytes permanently within a session lifetime. Generations must
// increase; callers must use a fresh session identity when reopening a session.
type Key struct {
	Session               string
	Generation, SeekEpoch uint64
	Sequence              int64
	Kind                  Kind
}
type Budget struct {
	Bytes           int64
	Segments, Inits int
}
type Limits struct {
	Global, PerSession Budget
	Sessions           int
}
type State struct {
	Generation, SeekEpoch uint64
	PublishedFloor        int64
}
type Metadata struct {
	Key       Key
	Format    Format
	Size      int64
	Published bool
	Facts     Facts
}
type Snapshot struct {
	// FirstPublishedSequence through State.PublishedFloor is the retained,
	// contiguous public sequence range; -1 denotes an empty range.
	FirstPublishedSequence int64
	State                  State
	Frozen                 bool
	Usage                  Budget
	Entries                []Metadata
}
type entry struct {
	meta    Metadata
	data    []byte
	readers int
}
type session struct {
	state   State
	used    Budget
	entries map[Key]*entry
	pending map[Key]bool
	frozen  *FreezeToken
}
type Store struct {
	mu       sync.Mutex
	limits   Limits
	used     Budget
	sessions map[string]*session
}

// FreezeToken is opaque, store-specific and single-use. It grants no start or
// retirement authority. Its target generation is fixed at freeze time.
type FreezeToken struct {
	owner   *Store
	session string
	old     State
	next    State
}

func New(l Limits) (*Store, error) {
	valid := func(b Budget) bool { return b.Bytes > 0 && b.Segments > 0 && b.Inits > 0 }
	if !valid(l.Global) || !valid(l.PerSession) || l.Sessions <= 0 {
		return nil, errors.New("invalid limits")
	}
	return &Store{limits: l, sessions: make(map[string]*session)}, nil
}
func (s *Store) OpenSession(id string, initial State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || initial.Generation == 0 || initial.PublishedFloor != -1 || s.sessions[id] != nil || len(s.sessions) >= s.limits.Sessions {
		return errors.New("invalid or duplicate session, or session limit")
	}
	s.sessions[id] = &session{state: initial, entries: make(map[Key]*entry), pending: make(map[Key]bool)}
	return nil
}
func delta(k Key, n int64) Budget {
	b := Budget{Bytes: n}
	if k.Kind == Init {
		b.Inits = 1
	} else {
		b.Segments = 1
	}
	return b
}
func add(a, b Budget) Budget {
	return Budget{a.Bytes + b.Bytes, a.Segments + b.Segments, a.Inits + b.Inits}
}
func sub(a, b Budget) Budget {
	return Budget{a.Bytes - b.Bytes, a.Segments - b.Segments, a.Inits - b.Inits}
}
func fits(a, b, cap Budget) bool {
	return b.Bytes <= cap.Bytes-a.Bytes && b.Segments <= cap.Segments-a.Segments && b.Inits <= cap.Inits-a.Inits
}

// Retain reserves five times the declared size (bytes plus bounded parsing
// workspace) before reading, reduced to exact retained bytes on success. It and requires EOF immediately
// after it. Reader must honor ctx itself if its Read can block; no hidden reader
// goroutine is created. All failed reads release their reservation. Successful
// bytes are staged and cannot be acquired until Publish or PublishTransition.
func (s *Store) Retain(ctx context.Context, k Key, f Format, n int64, r io.Reader) error {
	if ctx == nil || r == nil || n <= 0 || n > int64(int(^uint(0)>>1)) || k.Sequence < 0 || k.Kind > Init || f > FMP4 {
		return errors.New("invalid retain")
	}
	s.mu.Lock()
	ss := s.sessions[k.Session]
	d := delta(k, n)
	// Charge a conservative parser/read workspace allowance while staging.
	if n > (int64(^uint64(0)>>1))/5 {
		s.mu.Unlock()
		return errors.New("size overflow")
	}
	d.Bytes = 5 * n
	if ss == nil || !ss.accepts(k) || ss.entries[k] != nil || ss.pending[k] || !fits(s.used, d, s.limits.Global) || !fits(ss.used, d, s.limits.PerSession) {
		s.mu.Unlock()
		return errors.New("stale, duplicate, or capacity exceeded")
	}
	s.used = add(s.used, d)
	ss.used = add(ss.used, d)
	ss.pending[k] = true
	s.mu.Unlock()
	committed := false
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(ss.pending, k)
		if !committed {
			s.used = sub(s.used, d)
			ss.used = sub(ss.used, d)
		}
	}()
	b := make([]byte, int(n))
	off := 0
	for off < len(b) {
		if err := ctx.Err(); err != nil {
			return err
		}
		m, err := r.Read(b[off:])
		if m < 0 || m > len(b)-off {
			return errors.New("invalid reader count")
		}
		off += m
		if err != nil {
			if err == io.EOF && off == len(b) {
				break
			}
			return err
		}
		if m == 0 {
			return io.ErrNoProgress
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var extra [1]byte
	m, err := r.Read(extra[:])
	if m != 0 || err != io.EOF {
		return errors.New("size mismatch or missing EOF")
	}
	facts := inspect(f, k.Kind, b)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ss.accepts(k) {
		return errors.New("generation changed during read")
	}
	ss.entries[k] = &entry{meta: Metadata{Key: k, Format: f, Size: n, Facts: facts}, data: b}
	s.used.Bytes -= 4 * n
	ss.used.Bytes -= 4 * n
	committed = true
	return nil
}
func cloneMeta(m Metadata) Metadata {
	m.Facts.Tracks = append([]TrackFacts(nil), m.Facts.Tracks...)
	return m
}
func (s *Store) Snapshot(id string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[id]
	if ss == nil {
		return Snapshot{}, errors.New("unknown session")
	}
	v := Snapshot{State: ss.state, Frozen: ss.frozen != nil, Usage: ss.used, FirstPublishedSequence: -1}
	for _, e := range ss.entries {
		v.Entries = append(v.Entries, cloneMeta(e.meta))
		if e.meta.Published && e.meta.Key.Kind == Segment && (v.FirstPublishedSequence < 0 || e.meta.Key.Sequence < v.FirstPublishedSequence) {
			v.FirstPublishedSequence = e.meta.Key.Sequence
		}
	}
	sort.Slice(v.Entries, func(i, j int) bool {
		a, b := v.Entries[i].Key, v.Entries[j].Key
		if a.Generation != b.Generation {
			return a.Generation < b.Generation
		}
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		return a.Kind < b.Kind
	})
	return v, nil
}

// Publish atomically exposes a contiguous batch from the current generation.
// Ordinary publication can expose unverified media; a recovery transition cannot.
func (s *Store) Publish(id string, expected State, keys []Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[id]
	if ss == nil || ss.state != expected || ss.frozen != nil {
		return errors.New("publication CAS failed")
	}
	floor, err := s.batch(ss, id, expected.Generation, expected.SeekEpoch, expected.PublishedFloor, keys)
	if err != nil {
		return err
	}
	for _, k := range keys {
		ss.entries[k].meta.Published = true
	}
	ss.state.PublishedFloor = floor
	return nil
}
func (s *Store) batch(ss *session, id string, g, epoch uint64, floor int64, keys []Key) (int64, error) {
	if len(keys) == 0 {
		return floor, errors.New("empty publication")
	}
	seen := make(map[Key]bool)
	for _, k := range keys {
		e := ss.entries[k]
		if k.Session != id || k.Generation != g || k.SeekEpoch != epoch || seen[k] || e == nil || e.meta.Published {
			return floor, errors.New("invalid staged key")
		}
		seen[k] = true
		if k.Kind == Segment {
			if floor == int64(^uint64(0)>>1) || k.Sequence != floor+1 {
				return floor, errors.New("noncontiguous sequence")
			}
			floor = k.Sequence
		}
	}
	return floor, nil
}
func (s *Store) FreezeGeneration(id string, expected State, next State) (*FreezeToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[id]
	if ss == nil || ss.state != expected || ss.frozen != nil || next.Generation <= expected.Generation || next.SeekEpoch < expected.SeekEpoch || next.PublishedFloor <= expected.PublishedFloor {
		return nil, errors.New("freeze CAS failed")
	}
	t := &FreezeToken{owner: s, session: id, old: expected, next: next}
	ss.frozen = t
	return t, nil
}
func (s *Store) AbortTransition(t *FreezeToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, err := s.token(t)
	if err != nil {
		return err
	}
	ss.frozen = nil
	return nil
}
func (s *Store) token(t *FreezeToken) (*session, error) {
	if t == nil || t.owner != s {
		return nil, errors.New("foreign token")
	}
	ss := s.sessions[t.session]
	if ss == nil || ss.frozen != t || ss.state != t.old {
		return nil, errors.New("transition CAS failed")
	}
	return ss, nil
}

// PublishTransition verifies advancing observed PES timestamps and independent
// starts on every track. It does NOT prove a gap-free sample-duration boundary,
// decodability, delivery, or seamless playback. Old publications remain readable.
func (s *Store) PublishTransition(t *FreezeToken, keys []Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, err := s.token(t)
	if err != nil {
		return err
	}
	floor, err := s.batch(ss, t.session, t.next.Generation, t.next.SeekEpoch, t.old.PublishedFloor, keys)
	if err != nil {
		return err
	}
	if floor != t.next.PublishedFloor {
		return errors.New("proposed publication floor mismatch")
	}
	old := ss.entries[Key{t.session, t.old.Generation, t.old.SeekEpoch, t.old.PublishedFloor, Segment}]
	if old == nil || !old.meta.Published {
		return errors.New("missing boundary bytes")
	}
	prev := old.meta.Facts
	segments := 0
	for _, k := range keys {
		e := ss.entries[k]
		if k.Kind == Init {
			return errors.New("init transition unsupported")
		}
		if !follows(prev, e.meta.Facts) {
			return errors.New("unverified timestamp or independence boundary")
		}
		prev = e.meta.Facts
		segments++
	}
	if segments == 0 {
		return errors.New("no segment")
	}
	for _, k := range keys {
		ss.entries[k].meta.Published = true
	}
	ss.state = t.next
	ss.frozen = nil
	return nil
}

// Lease exposes copy-out reads only. Close waits for concurrent ReadAt calls and
// releases the pin. No method returns the store's mutable backing slice.
type Lease struct {
	mu     sync.Mutex
	s      *Store
	e      *entry
	closed bool
}

func (s *Store) Acquire(k Key) (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[k.Session]
	if ss == nil {
		return nil, errors.New("unknown session")
	}
	e := ss.entries[k]
	if e == nil || !e.meta.Published {
		return nil, errors.New("not published")
	}
	e.readers++
	return &Lease{s: s, e: e}, nil
}
func (l *Lease) ReadAt(p []byte, off int64) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, errors.New("closed lease")
	}
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= int64(len(l.e.data)) {
		return 0, io.EOF
	}
	n := copy(p, l.e.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (l *Lease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.s.mu.Lock()
		l.e.readers--
		l.s.mu.Unlock()
		l.closed = true
	}
	return nil
}

// Delete only releases staged bytes. Published output is promised until the
// controller explicitly closes the session; there is no automatic eviction.
func (s *Store) Delete(k Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[k.Session]
	if ss == nil {
		return errors.New("unknown session")
	}
	e := ss.entries[k]
	if e == nil {
		return nil
	}
	if e.meta.Published || e.readers != 0 {
		return errors.New("promised bytes")
	}
	d := delta(k, e.meta.Size)
	s.used = sub(s.used, d)
	ss.used = sub(ss.used, d)
	delete(ss.entries, k)
	return nil
}
func (s *Store) CloseSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[id]
	if ss == nil {
		return nil
	}
	if len(ss.pending) > 0 {
		return errors.New("pending reservations")
	}
	for _, e := range ss.entries {
		if e.readers > 0 {
			return errors.New("active readers")
		}
	}
	s.used = sub(s.used, ss.used)
	delete(s.sessions, id)
	return nil
}

func (ss *session) accepts(k Key) bool {
	if ss.frozen != nil && k.Generation == ss.frozen.next.Generation && k.SeekEpoch == ss.frozen.next.SeekEpoch {
		return true
	}
	return k.SeekEpoch == ss.state.SeekEpoch && k.Generation >= ss.state.Generation
}

// AdvanceGeneration is a controller-authorized fixed-grid ownership change.
// The contiguous published prefix remains immutable and readable. Old staged
// bytes cannot publish after this CAS; the next generation begins at floor+1.
// This method does not certify media/audio continuity; the encoder resume
// contract and independent playback acceptance must establish those facts.
func (s *Store) AdvanceGeneration(id string, expected State, generation uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.sessions[id]
	if ss == nil || ss.state != expected || ss.frozen != nil || generation != expected.Generation+1 {
		return errors.New("generation CAS failed")
	}
	ss.state.Generation = generation
	return nil
}
