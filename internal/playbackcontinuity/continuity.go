// Package playbackcontinuity allocates durable output floors after authenticated
// retirement. It does not start encoders, release capacity, or serve manifests.
package playbackcontinuity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/store"
)

var (
	ErrInvalid        = errors.New("playbackcontinuity: invalid contract")
	ErrStale          = errors.New("playbackcontinuity: stale fence, epoch, phase or revision")
	ErrProof          = errors.New("playbackcontinuity: authenticated evidence required")
	ErrUnsupported    = errors.New("playbackcontinuity: media continuity or buffer budget unsupported")
	ErrUnknownOutcome = errors.New("playbackcontinuity: write outcome unknown; reconcile with Lookup")
)

func init() {
	store.Register("playbackcontinuity", 1, `CREATE TABLE playbackcontinuity_sessions (
 session TEXT PRIMARY KEY, revision BIGINT NOT NULL, payload TEXT NOT NULL);`)
}

// SourceBinding pins the exact immutable source, including its owning process.
// Fence is the associated encoder execution, not a separate source placement.
type SourceBinding struct {
	Owner            placement.Target
	PlayID, Revision string
	Fence            placement.Fence
}

// PublicIdentity contains stable paths, never bearer secrets. The delivery layer
// must continue authenticating the original public session/grant on every use.
type PublicIdentity struct{ SessionURI, GrantURI string }

// Floor is the next unused public sequence and the exclusive media timestamp
// end in nanoseconds. Timestamp is an output clock, not the seekable title clock.
type Floor struct {
	NextSequence int64
	Timestamp    time.Duration
}

type Phase string

const (
	Active   Phase = "active"
	Reserved Phase = "reserved"
)

// DeliverySample must come from the final delivery authority, after packaging
// and URI mapping. Worker-to-controller Observe telemetry is never this proof.
// Buffered is surviving, contiguous downstream playable output measured at the
// sample, not merely files on the failed host. Proof binds every field, fence,
// epoch and authenticated viewer/buffer freshness; verifier must reject staleness.
type DeliverySample struct {
	Fence     placement.Fence
	Epoch     int64
	Floor     Floor
	SourceEnd time.Duration
	Buffered  time.Duration
	Proof     string
}

// Budget consists of externally measured conservative upper bounds for this
// deployment and source. It is a feasibility requirement, not a seamless claim.
type Budget struct {
	Detection, Fencing, Reopen, Encode, Publish, Safety time.Duration
	Proof                                               string
}

func (b Budget) Required() (time.Duration, error) {
	var total time.Duration
	for _, v := range []time.Duration{b.Detection, b.Fencing, b.Reopen, b.Encode, b.Publish, b.Safety} {
		if v <= 0 || v > time.Duration(math.MaxInt64)-total {
			return 0, ErrInvalid
		}
		total += v
	}
	if !text(b.Proof) {
		return 0, ErrProof
	}
	return total, nil
}

// Retirement proofs must authenticate BOTH source and encoder termination for
// the old exact binding/fence, or trusted external host fencing that covers both
// hosts and boots. Absence, timeout, lease expiry and heartbeats cannot qualify.
type Retirement struct{ SourceProof, EncoderProof, HostFenceProof string }

type RecoveryRequest struct {
	Operation     string
	ExpectedFence placement.Fence
	ExpectedEpoch int64
	Source        SourceBinding
	Final         DeliverySample
	Retirement    Retirement
	Budget        Budget
	// SeekTo nil resumes Final.SourceEnd. A seek increments the epoch and may
	// move the title clock backwards, but never either output floor.
	SeekTo *time.Duration
}

type Reservation struct {
	Token, Fingerprint          string
	Request                     RecoveryRequest
	Epoch                       int64
	Floor                       Floor
	SourceStart, RequiredBuffer time.Duration
}

// RestartFacts describes a held, packaged first segment, NOT permission to
// start an encoder. Proof must cover every required audio/video representation,
// keyframe independence (including open GOP leading pictures), init/codec
// compatibility, actual PTS/DTS/tfdt and timescales, and exact immutable reopen.
// Only an actual muxed clock can satisfy OutputStart; changing XML is insufficient.
type RestartFacts struct {
	Source                              SourceBinding
	Sequence                            int64
	SourceStart, OutputStart, OutputEnd time.Duration
	Independent                         bool
	Proof                               string
	// ReopenProof is required when affinity changes; a release ID or matching
	// filename is not an immutable source identity attestation.
	ReopenProof string
}

type Mapping struct {
	Public                                    PublicIdentity
	Epoch, Generation, FirstSequence          int64
	SourceStart, OutputStart, TimestampOffset time.Duration
	// Delivery must add HLS discontinuity + init mapping, or a new DASH Period
	// with a correct presentationTimeOffset. This package does not render either.
	HLSDiscontinuity, DASHNewPeriod bool
}

// EvidenceVerifier is supplied by a trusted control/delivery adapter. It must
// authenticate evidence, not trust booleans or opaque strings from workers.
// Calls run outside SQL; CAS afterwards prevents a stale result taking effect.
// VerifyRestart also validates the proposed new placement fence's authority.
// It must reject an elapsed/exhausted downstream budget at publication: HTTP
// completion alone does not establish retained client buffer or decoded output.
type EvidenceVerifier interface {
	VerifyDelivery(context.Context, State, DeliverySample) error
	VerifyRetirement(context.Context, SourceBinding, Retirement) error
	VerifyBudget(context.Context, SourceBinding, Budget) error
	VerifyRestart(context.Context, Reservation, placement.Fence, RestartFacts) error
}

// SourceReopenVerifier is an optional trusted capability. Without it, changing
// source owner or PlayID is unsupported. Providers must explicitly authenticate
// the old/new binding and identical immutable revision, including new affinity.
type SourceReopenVerifier interface {
	VerifyReopen(context.Context, SourceBinding, SourceBinding, string) error
}

type State struct {
	Session            string
	Version            int64
	Phase              Phase
	Fence              placement.Fence
	Source             SourceBinding
	Public             PublicIdentity
	Epoch              int64
	Floor              Floor
	SourceEnd          time.Duration
	InitialFingerprint string
	Reservation        *Reservation
	Mapping            *Mapping
	RestartFingerprint string
}

type Service struct {
	db       *sql.DB
	verifier EvidenceVerifier
}

func New(db *sql.DB, verifier EvidenceVerifier) (*Service, error) {
	if db == nil || verifier == nil {
		return nil, ErrInvalid
	}
	return &Service{db, verifier}, nil
}
func text(s string) bool { return len(s) > 0 && len(s) <= 4096 }
func publicPath(s string) bool {
	u, err := url.ParseRequestURI(s)
	return text(s) && err == nil && strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") && !strings.ContainsAny(s, "?#\\\r\n") && u.Host == "" && !u.IsAbs() && u.RawQuery == "" && u.Fragment == ""
}
func target(t placement.Target) bool { return text(t.NodeID) && text(t.Boot) }
func fence(f placement.Fence) bool {
	return text(f.Session) && f.Generation > 0 && target(f.Target) && text(f.Controller.ID) && text(f.Controller.Boot)
}
func source(s SourceBinding) bool {
	return target(s.Owner) && text(s.PlayID) && text(s.Revision) && fence(s.Fence)
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Open records an initial execution. It is not a placement admission. Replaying
// the identical Open never resets generations, phases, epochs or output floors.
func (s *Service) Open(ctx context.Context, binding SourceBinding, public PublicIdentity) (State, error) {
	if !source(binding) || !publicPath(public.SessionURI) || !publicPath(public.GrantURI) {
		return State{}, ErrInvalid
	}
	initial := digest(struct {
		Source SourceBinding
		Public PublicIdentity
	}{binding, public})
	st := State{Session: binding.Fence.Session, Version: 1, Phase: Active, Fence: binding.Fence, Source: binding, Public: public, InitialFingerprint: initial}
	b, _ := json.Marshal(st)
	_, err := s.db.ExecContext(ctx, `INSERT INTO playbackcontinuity_sessions(session,revision,payload) VALUES(?,?,?) ON CONFLICT(session) DO NOTHING`, st.Session, st.Version, string(b))
	if err != nil {
		return State{}, errors.Join(ErrUnknownOutcome, err)
	}
	st, err = s.Lookup(ctx, st.Session)
	if err == nil && st.InitialFingerprint != initial {
		return State{}, ErrStale
	}
	return st, err
}

func (s *Service) Lookup(ctx context.Context, session string) (State, error) {
	var st State
	var b string
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM playbackcontinuity_sessions WHERE session=?`, session).Scan(&b)
	if err == nil {
		err = json.Unmarshal([]byte(b), &st)
	}
	return st, err
}
func (s *Service) save(ctx context.Context, st State) (State, error) {
	if st.Version == math.MaxInt64 {
		return State{}, ErrInvalid
	}
	old := st.Version
	st.Version++
	b, err := json.Marshal(st)
	if err != nil {
		return State{}, err
	}
	r, err := s.db.ExecContext(ctx, `UPDATE playbackcontinuity_sessions SET revision=?,payload=? WHERE session=? AND revision=?`, st.Version, string(b), st.Session, old)
	if err != nil {
		return State{}, errors.Join(ErrUnknownOutcome, err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return State{}, errors.Join(ErrUnknownOutcome, err)
	}
	if n != 1 {
		return State{}, ErrStale
	}
	return st, nil
}
func sampleValid(st State, d DeliverySample) bool {
	return d.Fence == st.Fence && d.Epoch == st.Epoch && text(d.Proof) && d.Buffered >= 0 && d.SourceEnd >= 0 &&
		d.Floor.NextSequence >= st.Floor.NextSequence && d.Floor.NextSequence < math.MaxInt64 && d.Floor.Timestamp >= st.Floor.Timestamp &&
		((d.Floor == st.Floor && d.SourceEnd == st.SourceEnd) || (d.Floor.NextSequence > st.Floor.NextSequence && d.Floor.Timestamp > st.Floor.Timestamp && d.SourceEnd > st.SourceEnd))
}

// RecordDelivery advances floors only from authenticated final delivery. A
// reserved transition freezes publication floors; authentic late reports fail.
// Delivery can continue serving/replaying already committed buffered media
// without changing these floors. This service never revokes that delivery.
func (s *Service) RecordDelivery(ctx context.Context, session string, d DeliverySample) (State, error) {
	st, err := s.Lookup(ctx, session)
	if err != nil {
		return State{}, err
	}
	if st.Phase != Active || !sampleValid(st, d) {
		return State{}, ErrStale
	}
	if err = s.verifier.VerifyDelivery(ctx, st, d); err != nil {
		return State{}, errors.Join(ErrProof, err)
	}
	st.Floor = d.Floor
	st.SourceEnd = d.SourceEnd
	return s.save(ctx, st)
}

func (s *Service) ReserveRecovery(ctx context.Context, r RecoveryRequest) (Reservation, error) {
	if !text(r.Operation) || !source(r.Source) || r.ExpectedFence != r.Source.Fence || r.ExpectedEpoch < 0 {
		return Reservation{}, ErrInvalid
	}
	required, err := r.Budget.Required()
	if err != nil {
		return Reservation{}, err
	}
	st, err := s.Lookup(ctx, r.ExpectedFence.Session)
	if err != nil {
		return Reservation{}, err
	}
	fp := digest(r)
	if st.Reservation != nil && st.Reservation.Fingerprint == fp {
		return *st.Reservation, nil
	}
	if st.Phase != Active || st.Fence != r.ExpectedFence || st.Epoch != r.ExpectedEpoch || st.Source != r.Source || !sampleValid(st, r.Final) {
		return Reservation{}, ErrStale
	}
	if r.Final.Buffered < required {
		return Reservation{}, ErrUnsupported
	}
	if !text(r.Retirement.HostFenceProof) && (!text(r.Retirement.SourceProof) || !text(r.Retirement.EncoderProof)) {
		return Reservation{}, ErrProof
	}
	if err = s.verifier.VerifyDelivery(ctx, st, r.Final); err != nil {
		return Reservation{}, errors.Join(ErrProof, err)
	}
	if err = s.verifier.VerifyRetirement(ctx, st.Source, r.Retirement); err != nil {
		return Reservation{}, errors.Join(ErrProof, err)
	}
	if err = s.verifier.VerifyBudget(ctx, st.Source, r.Budget); err != nil {
		return Reservation{}, errors.Join(ErrProof, err)
	}
	epoch, start := st.Epoch, r.Final.SourceEnd
	if r.SeekTo != nil {
		if *r.SeekTo < 0 || epoch == math.MaxInt64 {
			return Reservation{}, ErrInvalid
		}
		epoch++
		start = *r.SeekTo
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return Reservation{}, err
	}
	reservation := Reservation{hex.EncodeToString(nonce[:]), fp, r, epoch, r.Final.Floor, start, required}
	st.Phase = Reserved
	st.Floor = r.Final.Floor
	st.SourceEnd = start
	st.Epoch = epoch
	st.Reservation = &reservation
	st.Mapping = nil
	st.RestartFingerprint = ""
	_, err = s.save(ctx, st)
	if err != nil {
		return Reservation{}, err
	}
	return reservation, nil
}

// CommitRestart commits policy for an already held and verified packaged
// segment. Caller must enforce publication ordering and retirement independently.
// A token is never an encoder-start permit; replay never grants fresh authority.
func (s *Service) CommitRestart(ctx context.Context, session, token string, next placement.Fence, f RestartFacts) (Mapping, error) {
	st, err := s.Lookup(ctx, session)
	if err != nil {
		return Mapping{}, err
	}
	if st.Reservation == nil || st.Reservation.Token != token {
		return Mapping{}, ErrStale
	}
	fp := digest(struct {
		Fence placement.Fence
		Facts RestartFacts
	}{next, f})
	if st.Phase == Active && st.RestartFingerprint == fp && st.Mapping != nil {
		return *st.Mapping, nil
	}
	r := *st.Reservation
	if st.Phase != Reserved || !fence(next) || next.Session != session || next.Generation <= st.Fence.Generation || f.Source.Fence != next {
		return Mapping{}, ErrStale
	}
	if !source(f.Source) || f.Source.Revision != st.Source.Revision {
		return Mapping{}, ErrUnsupported
	}
	if f.Source.PlayID != st.Source.PlayID || f.Source.Owner != st.Source.Owner {
		v, ok := s.verifier.(SourceReopenVerifier)
		if !ok || !text(f.ReopenProof) {
			return Mapping{}, ErrUnsupported
		}
		if err = v.VerifyReopen(ctx, st.Source, f.Source, f.ReopenProof); err != nil {
			return Mapping{}, errors.Join(ErrProof, err)
		}
	}
	if !f.Independent || !text(f.Proof) || f.Sequence != r.Floor.NextSequence || f.SourceStart != r.SourceStart || f.OutputStart != r.Floor.Timestamp || f.OutputEnd <= f.OutputStart {
		return Mapping{}, ErrUnsupported
	}
	if err = s.verifier.VerifyRestart(ctx, r, next, f); err != nil {
		return Mapping{}, errors.Join(ErrProof, err)
	}
	m := Mapping{st.Public, r.Epoch, next.Generation, f.Sequence, f.SourceStart, f.OutputStart, f.OutputStart - f.SourceStart, true, true}
	st.Phase = Active
	st.Fence = next
	st.Source = f.Source
	st.Mapping = &m
	st.RestartFingerprint = fp
	_, err = s.save(ctx, st)
	if err != nil {
		return Mapping{}, err
	}
	return m, nil
}
