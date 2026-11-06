package hlsplay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This handshake must hold HTTP admission without holding the playback clock.
// A MediaBrowser session differs from the requested client session identifier.
func TestSegmentBarrierBindingAndClock(t *testing.T) {
	for _, mode := range []string{"release", "wrong-session", "wrong-index", "wrong-proof", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			events, err := os.Create(filepath.Join(root, "events"))
			if err != nil {
				t.Fatal(err)
			}
			defer events.Close()
			p := player{c: Config{SegmentBarrierDir: filepath.Join(root, "barrier"), PauseBeforeIndex: 1}, playSession: "admitted-session", start: time.Now(), events: events}
			r := Report{Session: "client-session", InitialPlaylistSHA256: "initial", InitialTimeline: []time.Duration{0, 2 * time.Second, 4 * time.Second}, Segments: []Segment{{Index: 0}}}
			b := Buffer{Started: true, Level: .005}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { defer close(done); done <- p.waitForSegment(ctx, &b, &r, 1) }()
			defer func() {
				cancel()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Error("barrier worker leaked")
				}
			}()
			readyPath := filepath.Join(p.c.SegmentBarrierDir, "ready.json")
			for {
				_, err = os.Stat(readyPath)
				if err == nil {
					break
				}
				if ctx.Err() != nil {
					t.Fatal("ready not published")
				}
				time.Sleep(time.Millisecond)
			}
			var ready SegmentBarrier
			data, err := os.ReadFile(readyPath)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(data, &ready); err != nil {
				t.Fatal(err)
			}
			if ready.Session != "admitted-session" || ready.Index != 1 || ready.LastReceivedIndex != 0 {
				t.Fatalf("binding: %+v", ready)
			}
			// Deliberately spend real time behind the barrier with an already-playing
			// client. Even a successful release must expose the exhausted buffer.
			time.Sleep(15 * time.Millisecond)
			if mode == "cancel" {
				cancel()
			} else {
				proof := []byte("exact original task absent\n")
				hash := sha256.Sum256(proof)
				release := map[string]any{"session": ready.Session, "index": 1, "playlistSHA256": "initial", "faultVerified": true, "proofSHA256": hex.EncodeToString(hash[:])}
				switch mode {
				case "wrong-session":
					release["session"] = "stale-session"
				case "wrong-index":
					release["index"] = 2
				case "wrong-proof":
					proof = []byte("changed proof")
				}
				if err = os.WriteFile(filepath.Join(p.c.SegmentBarrierDir, "fault-proof.json"), proof, 0600); err != nil {
					t.Fatal(err)
				}
				data, _ = json.Marshal(release)
				temp := filepath.Join(p.c.SegmentBarrierDir, "release.tmp")
				if err = os.WriteFile(temp, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err = os.Rename(temp, filepath.Join(p.c.SegmentBarrierDir, "release.json")); err != nil {
					t.Fatal(err)
				}
			}
			result := <-done
			if (result == nil) != (mode == "release") {
				t.Fatalf("%s error=%v", mode, result)
			}
			if mode == "release" && (len(b.Stalls) != 1 || b.Stalls[0].DurationSeconds <= 0 || math.Abs(b.Played-.005) > 1e-9) {
				t.Fatalf("barrier froze buffer: %+v", b)
			}
		})
	}
}
