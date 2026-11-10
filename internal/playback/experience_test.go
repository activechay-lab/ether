package playback

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestExperienceAccountsStartAndBufferingFromTimelines pins the start-time
// and buffering accounting Activity › Experience reports: Plex from its
// explicit states, Jellyfin/Emby/Silo inferred from positions, startup
// excluded, seeks and pauses never read as stalls.
func TestExperienceAccountsStartAndBufferingFromTimelines(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Hour)}, []string{"A"})
	exp := func(id string) core.PlayExperience {
		t.Helper()
		ps, ok := h.m.Session(id)
		if !ok || ps.Experience == nil {
			t.Fatalf("session %s has no experience", id)
		}
		return *ps.Experience
	}

	// Plex: the decision is the play request; buffering before the first
	// "playing" is startup.
	plex := h.prepare(owner, movieKey, "plex-tv")
	step := func(client string, key core.RatingKey, d time.Duration, pos time.Duration, state string) {
		h.clock.Advance(d)
		h.m.PlaybackProgress(owner.ID, client, key, pos, state)
	}
	step("plex-tv", movieKey, time.Second, 0, "buffering")
	step("plex-tv", movieKey, 2*time.Second, 0, "playing")
	if e := exp(plex.ID); !e.Pictured || e.StartTime != 3*time.Second || e.BufferEvents != 0 || e.Buffering != 0 {
		t.Fatalf("plex start %+v", e)
	}
	step("plex-tv", movieKey, 10*time.Second, 10*time.Second, "playing")
	step("plex-tv", movieKey, 2*time.Second, 12*time.Second, "buffering")
	step("plex-tv", movieKey, 4*time.Second, 12*time.Second, "playing") // 4 s stall
	step("plex-tv", movieKey, time.Second, 13*time.Second, "buffering") // a 1 s blip
	step("plex-tv", movieKey, time.Second, 13*time.Second, "paused")    // under the floor: not a stall
	step("plex-tv", movieKey, time.Second, 13*time.Second, "buffering") // still open, only 2 s so far
	h.clock.Advance(2 * time.Second)
	if e := exp(plex.ID); e.StartTime != 3*time.Second || e.BufferEvents != 1 || e.Buffering != 4*time.Second {
		t.Fatalf("plex buffering %+v", e)
	}
	step("plex-tv", movieKey, 0, 13*time.Second, "playing")
	// A seek: the player rebuffers at the new position. Not a stall.
	step("plex-tv", movieKey, time.Second, 600*time.Second, "buffering")
	step("plex-tv", movieKey, 6*time.Second, 600*time.Second, "playing")
	if e := exp(plex.ID); e.BufferEvents != 1 || e.Buffering != 4*time.Second {
		t.Fatalf("plex seek counted as a stall %+v", e)
	}
	step("plex-tv", movieKey, time.Hour, 600*time.Second, "buffering") // a vanished client is capped
	step("plex-tv", movieKey, time.Hour, 600*time.Second, "stopped")
	if e := exp(plex.ID); e.BufferEvents != 2 || e.Buffering != 4*time.Second+bufferingCap {
		t.Fatalf("plex capped stall %+v", e)
	}

	// Jellyfin: the plan arrived before the source opened; the earliest
	// play request wins. Positions prove the picture and the stalls.
	caps := defaultCaps("jf")
	caps.Protocol = "jellyfin"
	jf, err := h.m.Prepare(context.Background(), owner, movie2, caps)
	if err != nil {
		t.Fatal(err)
	}
	h.m.NotePlayRequest(jf.ID, h.clock.Now().Add(-2*time.Second))
	step("jf", movie2, time.Second, 0, "playing")                // start report: baseline
	step("jf", movie2, 10*time.Second, 8*time.Second, "playing") // picture 8 s before this report
	if e := exp(jf.ID); !e.Pictured || e.StartTime != 5*time.Second {
		t.Fatalf("jellyfin start %+v", e)
	}
	step("jf", movie2, 10*time.Second, 18*time.Second, "playing") // smooth
	step("jf", movie2, 10*time.Second, 22*time.Second, "playing") // 6 s stall, recovered
	step("jf", movie2, 10*time.Second, 22*time.Second, "playing") // 10 s stall, ongoing
	step("jf", movie2, 10*time.Second, 22*time.Second, "playing") // same stall continues
	step("jf", movie2, 10*time.Second, 30*time.Second, "playing") // 2 s tail: below the minimum
	step("jf", movie2, 10*time.Second, 5*time.Second, "playing")  // seek back
	step("jf", movie2, 5*time.Second, 300*time.Second, "playing") // seek forward
	step("jf", movie2, 10*time.Second, 300*time.Second, "paused")
	step("jf", movie2, time.Minute, 300*time.Second, "playing") // resumed: new baseline
	step("jf", movie2, 10*time.Second, 310*time.Second, "playing")
	if e := exp(jf.ID); e.BufferEvents != 2 || e.Buffering != 26*time.Second || e.StartTime != 5*time.Second {
		t.Fatalf("jellyfin buffering %+v", e)
	}

	// A start the server ended as stalled never reached a picture.
	h.m.NoteFailedStart(plex.ID, "stalled")
	if e := exp(plex.ID); e.Pictured || e.StartTime >= 0 || e.StartFailure != "stalled" {
		t.Fatalf("failed start %+v", e)
	}
}
