package playbackcontinuity

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/store"
)

// These fakes model externally authenticated assertions, never process metrics.
type verifier struct{ delivery func() }

func (v verifier) VerifyDelivery(_ context.Context, _ State, d DeliverySample) error {
	if v.delivery != nil {
		v.delivery()
	}
	if d.Proof != "final-http" {
		return ErrProof
	}
	return nil
}
func (verifier) VerifyRetirement(_ context.Context, _ SourceBinding, r Retirement) error {
	if r.HostFenceProof == "external-kill" || (r.SourceProof == "source-reaped" && r.EncoderProof == "encoder-reaped") {
		return nil
	}
	return ErrProof
}
func (verifier) VerifyBudget(_ context.Context, _ SourceBinding, b Budget) error {
	if b.Proof != "lab-bounds" {
		return ErrProof
	}
	return nil
}
func (verifier) VerifyRestart(_ context.Context, _ Reservation, _ placement.Fence, f RestartFacts) error {
	if f.Proof != "mux-all-tracks" {
		return ErrProof
	}
	return nil
}

type reopenVerifier struct{ verifier }

func (reopenVerifier) VerifyReopen(_ context.Context, old, new SourceBinding, p string) error {
	if old.Revision != new.Revision || p != "immutable-timeline" {
		return ErrProof
	}
	return nil
}

func fixture(t *testing.T) (*Service, State, RecoveryRequest) {
	t.Helper()
	s, err := New(store.OpenTest(t), verifier{})
	if err != nil {
		t.Fatal(err)
	}
	f := placement.Fence{Session: "public-session", Generation: 3, Target: placement.Target{NodeID: "encoder", Boot: "boot1"}, Controller: placement.Controller{ID: "controller", Boot: "c1"}}
	b := SourceBinding{Owner: placement.Target{NodeID: "source", Boot: "s1"}, PlayID: "play", Revision: "immutable-content-and-timeline", Fence: f}
	st, err := s.Open(context.Background(), b, PublicIdentity{"/sessions/media/public/grant/manifest", "/sessions/media/public/grant/"})
	if err != nil {
		t.Fatal(err)
	}
	r := RecoveryRequest{Operation: "recover-1", ExpectedFence: f, Source: b, Final: DeliverySample{Fence: f, Floor: Floor{7, 28 * time.Second}, SourceEnd: 28 * time.Second, Buffered: 20 * time.Second, Proof: "final-http"}, Retirement: Retirement{SourceProof: "source-reaped", EncoderProof: "encoder-reaped"}, Budget: Budget{time.Second, time.Second, time.Second, time.Second, time.Second, time.Second, "lab-bounds"}}
	return s, st, r
}
func restart(r Reservation) (placement.Fence, RestartFacts) {
	f := r.Request.ExpectedFence
	f.Generation++
	f.Target.Boot = "boot2"
	b := r.Request.Source
	b.Fence = f
	return f, RestartFacts{Source: b, Sequence: r.Floor.NextSequence, SourceStart: r.SourceStart, OutputStart: r.Floor.Timestamp, OutputEnd: r.Floor.Timestamp + 4*time.Second, Independent: true, Proof: "mux-all-tracks"}
}
func mustReserve(t *testing.T, s *Service, r RecoveryRequest) Reservation {
	t.Helper()
	got, err := s.ReserveRecovery(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRecoveryReplaySeekAndGenerationFloors(t *testing.T) {
	ctx := context.Background()
	s, initial, r := fixture(t)
	seek := 8 * time.Second
	r.SeekTo = &seek
	reserved := mustReserve(t, s, r)
	if reserved.Epoch != 1 || reserved.SourceStart != seek || reserved.Floor.NextSequence != 7 || reserved.RequiredBuffer != 6*time.Second {
		t.Fatal(reserved)
	}
	// A coordinator restart reads the durable immutable reservation.
	s2, _ := New(s.db, verifier{})
	replay := mustReserve(t, s2, r)
	if digest(replay) != digest(reserved) {
		t.Fatal("reservation replay changed")
	}
	if _, err := s.RecordDelivery(ctx, initial.Session, r.Final); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	next, facts := restart(reserved)
	m, err := s2.CommitRestart(ctx, initial.Session, reserved.Token, next, facts)
	if err != nil {
		t.Fatal(err)
	}
	if m.TimestampOffset != 20*time.Second || m.FirstSequence != 7 || m.Public != initial.Public || !m.HLSDiscontinuity || !m.DASHNewPeriod {
		t.Fatal(m)
	}
	m2, err := s.CommitRestart(ctx, initial.Session, reserved.Token, next, facts)
	if err != nil || m2 != m {
		t.Fatal(m2, err)
	}
	// Replaying Open cannot reset floors or the seek epoch.
	st, err := s.Open(ctx, initial.Source, initial.Public)
	if err != nil || st.Floor != reserved.Floor || st.Epoch != 1 || st.Fence != next {
		t.Fatal(st, err)
	}
	d := DeliverySample{Fence: next, Epoch: 1, Floor: Floor{8, 32 * time.Second}, SourceEnd: 12 * time.Second, Proof: "final-http"}
	st, err = s.RecordDelivery(ctx, initial.Session, d)
	if err != nil || st.Floor.NextSequence != 8 {
		t.Fatal(st, err)
	}
	if _, err = s.RecordDelivery(ctx, initial.Session, r.Final); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	facts.Sequence++
	if _, err = s.CommitRestart(ctx, initial.Session, reserved.Token, next, facts); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
}

func TestProofGatesLeaveStateUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*RecoveryRequest)
	}{
		{"timeout", func(r *RecoveryRequest) { r.Retirement = Retirement{HostFenceProof: "timeout"} }},
		{"source-only", func(r *RecoveryRequest) { r.Retirement.EncoderProof = "" }},
		{"encoder-only", func(r *RecoveryRequest) { r.Retirement.SourceProof = "" }},
		{"worker-observe", func(r *RecoveryRequest) { r.Final.Proof = "worker-observe" }},
		{"budget-unmeasured", func(r *RecoveryRequest) { r.Budget.Proof = "guess" }},
		{"buffer-insufficient", func(r *RecoveryRequest) { r.Final.Buffered = time.Second }},
		{"stale-epoch", func(r *RecoveryRequest) { r.ExpectedEpoch = 1 }},
		{"wrong-source", func(r *RecoveryRequest) { r.Source.Revision = "replacement-release" }},
		{"sequence-overflow", func(r *RecoveryRequest) { r.Final.Floor.NextSequence = math.MaxInt64 }},
		{"budget-overflow", func(r *RecoveryRequest) { r.Budget.Encode = time.Duration(math.MaxInt64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, st, r := fixture(t)
			tc.mutate(&r)
			if _, err := s.ReserveRecovery(context.Background(), r); err == nil {
				t.Fatal("accepted")
			}
			after, _ := s.Lookup(context.Background(), st.Session)
			if digest(st) != digest(after) {
				t.Fatal("failed proof mutated state")
			}
		})
	}
}

func TestActualMuxAndImmutableReopenGates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*placement.Fence, *RestartFacts)
	}{
		{"old-generation", func(n *placement.Fence, f *RestartFacts) { n.Generation--; f.Source.Fence = *n }},
		{"dependent-gop", func(_ *placement.Fence, f *RestartFacts) { f.Independent = false }},
		{"manifest-only", func(_ *placement.Fence, f *RestartFacts) { f.Proof = "xml-renumber" }},
		{"wrong-revision", func(_ *placement.Fence, f *RestartFacts) { f.Source.Revision = "other" }},
		{"unattested-owner", func(_ *placement.Fence, f *RestartFacts) {
			f.Source.Owner.Boot = "different"
			f.ReopenProof = "immutable-timeline"
		}},
		{"wrong-sequence", func(_ *placement.Fence, f *RestartFacts) { f.Sequence-- }},
		{"gap", func(_ *placement.Fence, f *RestartFacts) { f.OutputStart++ }},
		{"overlap", func(_ *placement.Fence, f *RestartFacts) { f.OutputStart-- }},
		{"wrong-title-position", func(_ *placement.Fence, f *RestartFacts) { f.SourceStart-- }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, st, r := fixture(t)
			reserved := mustReserve(t, s, r)
			n, f := restart(reserved)
			tc.mutate(&n, &f)
			if _, err := s.CommitRestart(context.Background(), st.Session, reserved.Token, n, f); err == nil {
				t.Fatal("accepted")
			}
			after, _ := s.Lookup(context.Background(), st.Session)
			if after.Phase != Reserved || after.Floor != reserved.Floor {
				t.Fatal(after)
			}
		})
	}
	s, st, r := fixture(t)
	r.Retirement = Retirement{HostFenceProof: "external-kill"}
	reserved := mustReserve(t, s, r)
	s.verifier = reopenVerifier{}
	n, f := restart(reserved)
	f.Source.Owner = placement.Target{NodeID: "source2", Boot: "s2"}
	f.Source.PlayID = "reopened"
	f.ReopenProof = "immutable-timeline"
	if _, err := s.CommitRestart(context.Background(), st.Session, reserved.Token, n, f); err != nil {
		t.Fatal(err)
	}
}

func TestCASRejectsProofRacingSeek(t *testing.T) {
	s, st, r := fixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	slow, _ := New(s.db, verifier{delivery: func() { close(entered); <-release }})
	var wg sync.WaitGroup
	wg.Add(1)
	var loser error
	go func() { defer wg.Done(); _, loser = slow.RecordDelivery(context.Background(), st.Session, r.Final) }()
	<-entered
	seek := 4 * time.Second
	r.SeekTo = &seek
	mustReserve(t, s, r)
	close(release)
	wg.Wait()
	if !errors.Is(loser, ErrStale) {
		t.Fatal(loser)
	}
	after, _ := s.Lookup(context.Background(), st.Session)
	if after.Phase != Reserved || after.Epoch != 1 || after.SourceEnd != seek {
		t.Fatal(after)
	}
}

func TestMultipleRecoveriesNeverReuseOldTokenOrOutput(t *testing.T) {
	s, initial, r := fixture(t)
	ctx := context.Background()
	var old Reservation
	for cycle := 0; cycle < 3; cycle++ {
		if cycle == 1 {
			seek := 2 * time.Second
			r.SeekTo = &seek
		} else {
			r.SeekTo = nil
		}
		reserved := mustReserve(t, s, r)
		if cycle > 0 {
			n, f := restart(old)
			if _, err := s.CommitRestart(ctx, initial.Session, old.Token, n, f); !errors.Is(err, ErrStale) {
				t.Fatal("obsolete token accepted", err)
			}
		}
		n, f := restart(reserved)
		if _, err := s.CommitRestart(ctx, initial.Session, reserved.Token, n, f); err != nil {
			t.Fatal(err)
		}
		d := DeliverySample{Fence: n, Epoch: reserved.Epoch, Floor: Floor{reserved.Floor.NextSequence + 1, reserved.Floor.Timestamp + 4*time.Second}, SourceEnd: reserved.SourceStart + 4*time.Second, Buffered: 20 * time.Second, Proof: "final-http"}
		st, err := s.RecordDelivery(ctx, initial.Session, d)
		if err != nil {
			t.Fatal(err)
		}
		if st.Floor.NextSequence != int64(8+cycle) || st.Floor.Timestamp != time.Duration(32+4*cycle)*time.Second || st.Fence.Generation != int64(4+cycle) {
			t.Fatal(st)
		}
		old = reserved
		r.Operation += "-next"
		r.ExpectedFence = n
		r.ExpectedEpoch = st.Epoch
		r.Source = st.Source
		r.Final = d
	}
}

func TestConcurrentReservationsChooseOneDurableToken(t *testing.T) {
	s, st, r := fixture(t)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	v := verifier{delivery: func() { entered <- struct{}{}; <-release }}
	one, _ := New(s.db, v)
	two, _ := New(s.db, v)
	type outcome struct {
		token Reservation
		err   error
	}
	results := make(chan outcome, 2)
	for _, service := range []*Service{one, two} {
		go func(svc *Service) {
			got, err := svc.ReserveRecovery(context.Background(), r)
			results <- outcome{got, err}
		}(service)
	}
	<-entered
	<-entered
	close(release)
	a, b := <-results, <-results
	if a.err != nil {
		a, b = b, a
	}
	if a.err != nil || !errors.Is(b.err, ErrStale) {
		t.Fatal(a.err, b.err)
	}
	after, err := s.Lookup(context.Background(), st.Session)
	if err != nil || after.Reservation.Token != a.token.Token || after.Phase != Reserved {
		t.Fatal(after, err)
	}
}
