package livetv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// The ladder reopens a lost source at 0, 0.5 and 1.5 s, then opens the next
// backup at once; health only orders, and the channel never waits out a
// source cooldown while another input remains.
func TestSteadyLadderRungsThenBackup(t *testing.T) {
	l := newSteadyLadder()
	t0 := time.Unix(1000, 0)
	sources := []string{"primary", "backup"}
	pick := func(now time.Time) string {
		id, _, ok, _ := l.choose(sources, now)
		if !ok {
			t.Fatal("no input chosen")
		}
		l.opened(id, now)
		return id
	}
	if id := pick(t0); id != "primary" {
		t.Fatalf("first open %s", id)
	}
	lost := errors.New("decoder stalled")
	at := l.failed("primary", lost, true, false, sources, t0)
	want := []time.Duration{0, 500 * time.Millisecond, 1500 * time.Millisecond}
	for i, offset := range want {
		if !at.Equal(t0.Add(offset)) {
			t.Fatalf("rung %d at %s, want +%s", i, at.Sub(t0), offset)
		}
		if id := pick(at); id != "primary" {
			t.Fatalf("rung %d reopened %s", i, id)
		}
		at = l.failed("primary", lost, false, false, sources, at.Add(100*time.Millisecond))
	}
	if id := pick(at); id != "backup" {
		t.Fatalf("after the rungs the ladder opened %s, want the backup", id)
	}
	if at.Sub(t0) > 1700*time.Millisecond {
		t.Fatalf("backup opened %s after the loss", at.Sub(t0))
	}
	l.delivered("backup")
	if l.episode != (time.Time{}) || l.sources["backup"].fails != 0 {
		t.Fatal("delivery did not end the episode")
	}
}

func TestSteadyLadderFailoverCodecChangeAndRounds(t *testing.T) {
	l := newSteadyLadder()
	t0 := time.Unix(1000, 0)
	sources := []string{"a", "b"}
	l.opened("a", t0)
	// A transport-stream tap that already exhausted its URL skips the rungs.
	if at := l.failed("a", errors.New("eof"), true, true, sources, t0); !at.Equal(t0) || l.next != "b" {
		t.Fatalf("failover chose %q at +%s", l.next, at.Sub(t0))
	}
	l.opened("b", t0)
	// A codec change rejoins the same source at its live edge, spending no rung.
	at := l.failed("b", transcode.ErrInputChanged, false, false, sources, t0)
	if id, edge, _, _ := l.choose(sources, at); id != "b" || !edge || !at.Equal(t0) || l.rung != 0 {
		t.Fatalf("codec change: %s edge=%v rung=%d at +%s", id, edge, l.rung, at.Sub(t0))
	}
	// Every input failing: later rounds back off and keep each source's own
	// attempts at least a second apart.
	now := t0
	for range 12 {
		id, _, ok, _ := l.choose(sources, now)
		if !ok {
			t.Fatal("unparked inputs refused")
		}
		if prev := l.sources[id]; prev != nil && !prev.last.IsZero() && l.round > 0 && now.Sub(prev.last) < steadySpacing {
			t.Fatalf("%s retried %s after its last attempt", id, now.Sub(prev.last))
		}
		l.opened(id, now)
		now = l.failed(id, errors.New("refused"), false, false, sources, now)
	}
	if now.Sub(t0) < 10*time.Second {
		t.Fatalf("twelve failed opens took only %s", now.Sub(t0))
	}
}

// Provider safety: a source refusing every open is tried at most 28 times
// without delivering, then rests for steadyParked, with one probe after.
func TestSteadyLadderParksARefusingSource(t *testing.T) {
	l := newSteadyLadder()
	now := time.Unix(1000, 0)
	sources := []string{"only"}
	opens := 0
	start := now
	for opens < 100 {
		id, _, ok, _ := l.choose(sources, now)
		if !ok {
			t.Fatal("source parked before its retry time")
		}
		opens++
		l.opened(id, now)
		next := l.failed(id, errors.New("HTTP 509"), false, false, sources, now)
		if next.Sub(now) >= time.Minute {
			if _, _, ok, _ := l.choose(sources, now.Add(time.Second)); ok {
				t.Fatal("parked source still chosen")
			}
			now = next
			break
		}
		now = next
	}
	if opens != steadyRefusalCeiling || now.Sub(start) < 26*time.Second {
		t.Fatalf("%d opens over %s before parking, want %d paced over >= 26 s", opens, now.Sub(start), steadyRefusalCeiling)
	}
	if _, _, ok, _ := l.choose(sources, now); !ok {
		t.Fatal("source never unparked")
	}
	l.opened("only", now)
	l.failed("only", errors.New("HTTP 509"), false, false, sources, now)
	if _, _, ok, _ := l.choose(sources, now.Add(time.Second)); ok {
		t.Fatal("a parked source got a second burst after its probe")
	}
}

type joiningLimiter struct {
	fakeChannelLimiter
	joined int
}

func (f *joiningLimiter) JoinChannel(*core.User, core.ClientCaps, string, string) error {
	f.joined++
	return nil
}

// A tune Steady refuses plays direct on its own viewer reservation, not on
// a share of a channel output that never started.
func TestRefusedSteadyTuneHoldsADirectViewerReservation(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "#EXTM3U\n#EXTINF:4,\npart.ts\n") }))
	defer up.Close()
	pb := &joiningLimiter{fakeChannelLimiter: fakeChannelLimiter{fakePlayback: &fakePlayback{}}}
	// This transcoder runs no continuous outputs, so Steady refuses the tune.
	s := testService(t, Options{Playback: pb, Transcoder: &fakeTranscoder{}, HTTPClient: up.Client()})
	c, err := s.SaveChannel(context.Background(), Channel{Name: "Test", Number: "1", Kind: "iptv", Enabled: true, StreamURL: up.URL})
	if err != nil {
		t.Fatal(err)
	}
	u := &core.User{ID: 1}
	info, err := s.Tune(context.Background(), u, c.ID, core.ClientCaps{})
	if err != nil {
		t.Fatal(err)
	}
	if pb.joined != 1 || pb.begun != 1 || pb.ended != 1 {
		t.Fatalf("joined=%d begun=%d ended=%d, want the shared join swapped for one viewer reservation", pb.joined, pb.begun, pb.ended)
	}
	if err := s.Stop(context.Background(), info.ID, u); err != nil {
		t.Fatal(err)
	}
	if pb.ended != 2 {
		t.Fatal("stop leaked the direct reservation")
	}
}

// Scarce Steady slots go to flaky and unknown sources first; a clean source
// never takes the last one. Joining or keeping a running output is not an
// allocation, so nothing here can pre-empt a viewer.
func TestSteadyAllocationFavoursFlakySources(t *testing.T) {
	flaky := classifySource(SourceHistory{Stalls: 1, MediaSeconds: 600}, true)
	clean := classifySource(SourceHistory{MediaSeconds: 600}, true)
	young := classifySource(SourceHistory{MediaSeconds: 10}, true)
	if flaky != steadyClassFlaky || clean != steadyClassClean || young != steadyClassUnknown || classifySource(SourceHistory{}, false) != steadyClassUnknown {
		t.Fatalf("classes %s %s %s", flaky, clean, young)
	}
	const cost, budget = 1.0, 1.5 // room for exactly one output
	for _, tc := range []struct {
		class string
		load  float64
		want  string
	}{
		{steadyClassFlaky, 0, ""},
		{steadyClassUnknown, 0, ""},
		{steadyClassClean, 0, steadyRefusedClean},
		{steadyClassFlaky, 1, steadyRefusedCapacity},
		{steadyClassClean, 1, steadyRefusedCapacity},
	} {
		if got := allocateSteady(tc.class, tc.load, cost, budget); got != tc.want {
			t.Fatalf("%s at load %.0f: %q, want %q", tc.class, tc.load, got, tc.want)
		}
	}
	if got := allocateSteady(steadyClassClean, 0, cost, 2.5); got != "" {
		t.Fatalf("clean source refused with two free slots: %q", got)
	}
}
