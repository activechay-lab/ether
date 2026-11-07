package livetv

import (
	"errors"
	"testing"
	"time"

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
