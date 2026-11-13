package plex

import (
	"net/http/httptest"
	"testing"
	"time"
)

// Issue #410: a Plex client that went silent (app backgrounded) kept its
// encoder and source sessions because now-playing expiry only dropped the
// display entry. Expiring a playing entry must end playback like a stopped
// timeline; a paused entry is left to its retention clocks.
func TestExpiredPlayingEntryEndsPlayback(t *testing.T) {
	for _, state := range []string{"playing", "paused"} {
		t.Run(state, func(t *testing.T) {
			f, m := issue94Fixture(t, true, "open")
			tr := f.srv.transcoder().(*issue94Transcoder)
			h := []string{"X-Plex-Client-Identifier", "phone", "X-Plex-Session-Identifier", "play-410", "Accept", "application/json"}
			for _, path := range []string{"decision?", "start.m3u8?"} {
				if r := f.get(t, "/video/:/transcode/universal/"+path+issue94Query("trans-410", 3000), h...); r.Code != 200 {
					t.Fatalf("%s %d", path, r.Code)
				}
			}
			if r := f.get(t, "/:/timeline?ratingKey=1000000000000550&state="+state+"&time=1000", h...); r.Code != 200 {
				t.Fatalf("timeline %d", r.Code)
			}
			entries := f.srv.sessions.list()
			if len(entries) != 1 {
				t.Fatalf("entries = %d, want 1", len(entries))
			}
			e := entries[0]
			f.srv.sessions.remove(e.id) // as the janitor's expire did
			f.srv.endExpiredEntry(e)
			_, encoderAlive := tr.Session("trans-410")
			_, sourceAlive := m.SessionFor(ownerUser.ID, "phone", fightClub.Key)
			if state == "paused" {
				if !encoderAlive || !sourceAlive {
					t.Fatal("paused entry expiry ended a retained session")
				}
				return
			}
			if encoderAlive {
				t.Fatal("expired playing entry left the encoder running")
			}
			if sourceAlive {
				t.Fatal("expired playing entry left the source session running")
			}
		})
	}
}

// TestSegmentFetchRetainsSilentTimeline also proves expiry after fetching stops.
func TestSegmentFetchRetainsSilentTimeline(t *testing.T) {
	f, _ := issue94Fixture(t, true, "open")
	h := []string{"X-Plex-Client-Identifier", "phone", "X-Plex-Session-Identifier", "play-456"}
	for _, path := range []string{"decision?", "start.m3u8?"} {
		if r := f.get(t, "/video/:/transcode/universal/"+path+issue94Query("trans-456", 3000), h...); r.Code != 200 {
			t.Fatalf("%s %d", path, r.Code)
		}
	}
	if r := f.get(t, "/:/timeline?ratingKey=1000000000000550&state=playing&time=1000", h...); r.Code != 200 {
		t.Fatal(r.Code)
	}
	encoder, ok := f.srv.plexHLSSession("trans-456")
	if !ok {
		t.Fatal("missing encoder")
	}
	fake := encoder.(*fakeHLS)
	for i := 1; i <= 6; i++ {
		now := fixedNow.Add(time.Duration(i) * time.Minute)
		fake.ServeSegment(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), i)
		fake.lastAccess = now
		if expired := f.srv.expirePlaybackActivities(now, 3*time.Minute); len(expired) != 0 {
			t.Fatal("active media fetch expired silent timeline")
		}
	}
	expired := f.srv.expirePlaybackActivities(fake.lastAccess.Add(3*time.Minute+time.Second), 3*time.Minute)
	if len(expired) != 1 {
		t.Fatalf("idle entries = %d", len(expired))
	}
}
