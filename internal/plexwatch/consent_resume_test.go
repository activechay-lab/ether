package plexwatch

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
)

// With the account's "Sync My Watch State and Ratings" off nothing is sent
// or imported, changes stay queued, and the setting is read once per viewer
// per pass. Turning it on in Plex starts sync without reconnecting.
func TestConsentOffHoldsSyncUntilTurnedOn(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, 42)
	h.connect(t, 2, 42, 1)
	a, b, c := core.MovieKey(1), core.MovieKey(2), core.MovieKey(3)
	h.guid.add(a, hexA)
	h.guid.add(b, hexB)
	h.guid.add(c, hexC)
	consentReads := func(want int) {
		t.Helper()
		h.plex.mu.Lock()
		got := h.plex.consentReads
		h.plex.mu.Unlock()
		if got != want {
			t.Fatalf("consent reads %d, want %d", got, want)
		}
	}

	h.plex.setConsent(false)
	if err := h.st.ScrobbleMany(ctx, 2, []core.RatingKey{a, b}); err != nil {
		t.Fatal(err)
	}
	h.svc.deliverAvailable(ctx)
	wantWrites(t, h)
	consentReads(1)
	if st, _ := h.svc.Status(ctx, 2, 42); st.Problem != "consent" || st.Pending != 2 {
		t.Fatalf("consent off: %+v", st)
	}
	// A new change soon after reuses the answer; held rows are not retried.
	h.clock.Add(30 * time.Second)
	if err := h.st.Rate(ctx, 2, a, 8); err != nil {
		t.Fatal(err)
	}
	h.deliverAll(t)
	wantWrites(t, h)
	consentReads(1)

	// The scheduled pass re-reads it for a viewer who only sends; once on,
	// the held changes flow in order with one read for the whole pass.
	h.plex.setConsent(true)
	h.clock.Add(11 * time.Minute)
	h.svc.importDue(ctx)
	consentReads(2)
	h.svc.deliverAvailable(ctx)
	wantWrites(t, h, "scrobble "+hexA+" ", "scrobble "+hexB+" ", "rate "+hexA+" 8")
	consentReads(3)
	if st, _ := h.svc.Status(ctx, 2, 42); st.Problem != "" || st.Pending != 0 {
		t.Fatalf("consent on: %+v", st)
	}

	// Nothing is imported while it is off.
	if _, err := h.svc.SetDirection(ctx, 2, 42, 3, 10); err != nil {
		t.Fatal(err)
	}
	h.plex.watch(hexC, "movie", h.clock.Now())
	h.plex.setConsent(false)
	h.clock.Add(time.Minute)
	if st := h.importNow(t, 2, 42); st.Problem != "consent" || st.LastImport != 0 {
		t.Fatalf("import with consent off: %+v", st)
	}
	if reads := h.plex.takeReads(); len(reads) != 0 || h.local(t, 2, c).ViewCount != 0 {
		t.Fatalf("read Plex with consent off: %v", reads)
	}
	h.plex.setConsent(true)
	h.clock.Add(time.Minute)
	if st := h.importNow(t, 2, 42); st.Problem != "" || h.local(t, 2, c).ViewCount != 1 {
		t.Fatalf("import with consent on: %+v", st)
	}
}

// Resume positions flow both ways: playback ticks coalesce into one send
// per hold or on pause, Plex positions newer than Ether's apply, Ether's
// own writes are not read back, and a stale position never resurrects a
// finished or removed title.
func TestResumePositionsSyncBothWays(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, 42)
	h.connect(t, 2, 42, 3)
	m, e := core.MovieKey(10), core.EpisodeKey(1396, 1, 1)
	h.guid.add(m, hexA)
	h.guid.add(e, hexB)
	dur := 100 * time.Minute
	progress := func(key core.RatingKey, off time.Duration, state string) {
		t.Helper()
		if err := h.st.Progress(ctx, 2, key, off, dur, state); err != nil {
			t.Fatal(err)
		}
	}

	// Heartbeats are held; pause sends the latest position once.
	for _, off := range []time.Duration{10, 11, 12} {
		progress(m, off*time.Minute, "playing")
		h.clock.Add(10 * time.Second)
		h.deliverAll(t)
	}
	wantWrites(t, h)
	progress(m, 13*time.Minute, "paused")
	h.deliverAll(t)
	wantWrites(t, h, "timeline "+hexA+" 780000")
	// Steady playback sends at most once per hold.
	progress(m, 14*time.Minute, "playing")
	h.clock.Add(time.Minute)
	progress(m, 15*time.Minute, "playing")
	h.deliverAll(t)
	wantWrites(t, h)
	h.clock.Add(state.OffsetHold)
	h.deliverAll(t)
	wantWrites(t, h, "timeline "+hexA+" 900000")

	// Ether's own position read back changes nothing.
	before := h.local(t, 2, m)
	h.clock.Add(time.Minute)
	h.importNow(t, 2, 42)
	if after := h.local(t, 2, m); after.ViewOffset != 15*time.Minute || !after.LastViewedAt.Equal(before.LastViewedAt) {
		t.Fatalf("own position imported: %+v -> %+v", before, after)
	}

	// A newer position from a Plex app applies, without echo, and a title
	// the viewer removed from Continue Watching stays removed.
	if err := h.st.HideContinue(ctx, 2, m); err != nil {
		t.Fatal(err)
	}
	h.clock.Add(time.Minute)
	h.plex.play(hexA, "movie", 40*60000, h.clock.Now())
	h.clock.Add(time.Minute)
	h.importNow(t, 2, 42)
	if ws := h.local(t, 2, m); ws.ViewOffset != 40*time.Minute || ws.ViewCount != 0 {
		t.Fatalf("plex position not imported: %+v", ws)
	}
	if hidden, err := h.st.ContinueHidden(ctx, 2, []core.RatingKey{m}); err != nil || !hidden[m] {
		t.Fatalf("removed title came back: %v %v", hidden, err)
	}
	h.deliverAll(t)
	wantWrites(t, h)

	// Finishing in Ether supersedes a held position: only the scrobble goes.
	h.clock.Add(time.Minute)
	progress(m, 50*time.Minute, "playing")
	h.clock.Add(time.Minute)
	progress(m, 95*time.Minute, "playing")
	h.deliverAll(t)
	wantWrites(t, h, "scrobble "+hexA+" ")
	h.clock.Add(state.OffsetHold)
	h.deliverAll(t)
	wantWrites(t, h)

	// Plex still holds the old position, and then an older one: neither
	// resurrects the finished title.
	h.clock.Add(time.Minute)
	h.importNow(t, 2, 42)
	h.plex.play(hexA, "movie", 30*60000, h.clock.Now().Add(-time.Hour))
	h.clock.Add(time.Minute)
	h.importNow(t, 2, 42)
	if ws := h.local(t, 2, m); ws.ViewOffset != 0 || ws.ViewCount != 1 {
		t.Fatalf("stale position resurrected the title: %+v", ws)
	}

	// Episodes too; a rewind to the start clears Plex's position only while
	// Plex has the episode unwatched.
	progress(e, 20*time.Minute, "stopped")
	h.deliverAll(t)
	wantWrites(t, h, "timeline "+hexB+" 1200000")
	h.clock.Add(time.Minute)
	progress(e, 1*time.Minute, "paused")
	h.deliverAll(t)
	wantWrites(t, h, "unscrobble "+hexB+" ")
	h.deliverAll(t)
	wantWrites(t, h)
}
