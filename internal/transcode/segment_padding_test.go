package transcode

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// A segment that takes longer than a second keeps bytes flowing: the
// response commits early with container padding, and its body is that
// padding followed by exactly the segment a later request gets.
func TestIntegrationSlowSegmentIsPadded(t *testing.T) {
	e := newRealEnv(t, WithoutHWDetection())
	e.gen(t, "pad.mkv", 8, "-c:v", "libx264", "-preset", "ultrafast", "-g", "24", "-c:a", "aac")
	info, err := e.m.Probe(t.Context(), e.url("pad.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	// The source holds every request until the player has seen padding, so
	// segment 0 cannot be ready before padding starts, however fast the
	// host. StartHLS's own bounded preflight reads give up on their
	// deadlines and keep the copy.
	var release atomic.Pointer[chan struct{}]
	files := http.FileServer(http.Dir(e.dir))
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if held := release.Load(); held != nil {
			select {
			case <-*held:
			case <-r.Context().Done():
				return
			}
		}
		files.ServeHTTP(w, r)
	}))
	defer source.Close()
	for _, c := range []struct {
		container string
		unit      []byte
	}{{ContainerMPEGTS, tsNullPacket}, {ContainerFMP4, fmp4FreeBox}} {
		t.Run(c.container, func(t *testing.T) {
			held := make(chan struct{})
			release.Store(&held)
			var once sync.Once
			unblock := func() { once.Do(func() { close(held) }) }
			defer unblock()
			d := core.Decision{Kind: core.DecisionRemux, Container: c.container, VideoCopy: true, AudioCopy: true, AudioStream: 1}
			hs, err := e.m.StartHLS(context.Background(), core.HLSRequest{SessionID: "pad-" + c.container, InputURL: source.URL + "/pad.mkv", Info: *info, Decision: d})
			if err != nil {
				t.Fatal(err)
			}
			defer hs.Close()
			client := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hs.ServeSegment(w, r, 0) }))
			defer client.Close()
			resp, err := http.Get(client.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			first := make([]byte, len(c.unit))
			if _, err := io.ReadFull(resp.Body, first); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, c.unit) {
				t.Fatalf("first bytes while the segment is held: % x", first[:8])
			}
			// The player has padding; now let the segment be produced.
			unblock()
			release.Store(nil)
			if resp.StatusCode != 200 || resp.ContentLength != -1 || resp.Header.Get("Accept-Ranges") != "none" {
				t.Fatalf("padded response: %d length %d ranges %q", resp.StatusCode, resp.ContentLength, resp.Header.Get("Accept-Ranges"))
			}
			rest, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			body := append(first, rest...)
			pads := 0
			for bytes.HasPrefix(body, c.unit) {
				body = body[len(c.unit):]
				pads++
			}
			if again := fetch(t, hs, 0); len(body) == 0 || !bytes.Equal(body, again) {
				t.Fatalf("after %d padding units the body (%d bytes) is not the segment (%d bytes)", pads, len(body), len(again))
			}
			var init0 []byte
			if hasInitSegment(c.container) {
				init0 = fetch(t, hs, InitSegment)
			}
			// Padding and all, the response decodes.
			e.decodesAlone(t, c.container, init0, append(first, rest...), "padded-"+c.container)
		})
	}
}

// Once padding has committed a 200, only the segment's every byte may end
// the response cleanly. An error, a short body or an early return must
// abort the connection (no chunked terminator), so the player fails the
// request and retries instead of accepting an empty segment.
func TestPaddedSegmentAbortsUnlessComplete(t *testing.T) {
	seg := filepath.Join(t.TempDir(), "seg.ts")
	if err := os.WriteFile(seg, bytes.Repeat(tsNullPacket, 4), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Session{container: ContainerMPEGTS}
	for _, c := range []struct {
		name  string
		end   func(w http.ResponseWriter, pw *paddedSegmentWriter)
		clean bool
	}{
		{"segment delivered", func(w http.ResponseWriter, pw *paddedSegmentWriter) {
			b, _ := os.ReadFile(seg)
			_, _ = w.Write(b)
			pw.delivered([]string{seg})
		}, true},
		{"error after padding", func(w http.ResponseWriter, _ *paddedSegmentWriter) {
			http.Error(w, "superseded by a newer request", http.StatusServiceUnavailable)
		}, false},
		{"short body", func(w http.ResponseWriter, pw *paddedSegmentWriter) {
			_, _ = w.Write(tsNullPacket)
			pw.delivered([]string{seg})
		}, false},
		{"returned without media", func(http.ResponseWriter, *paddedSegmentWriter) {}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				pw := &paddedSegmentWriter{ResponseWriter: w, unit: tsNullPacket}
				defer pw.finish()
				pw.pad(s)
				pw.pad(s)
				c.end(pw, pw)
			}))
			defer srv.Close()
			resp, err := http.Get(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			_, err = io.ReadAll(resp.Body)
			if c.clean != (err == nil) {
				t.Fatalf("read ended with %v, want clean=%v", err, c.clean)
			}
		})
	}
}

// A segment that never becomes ready ends in an aborted response, after
// the padding limit or the segment timeout, never in a finished 200.
func TestIntegrationPaddedSegmentThatNeverArrivesAborts(t *testing.T) {
	for _, c := range []struct {
		name string
		opts []Option
	}{
		{"padding limit", []Option{WithSegmentPadLimit(3 * time.Second)}},
		{"segment timeout", []Option{WithSegmentTimeout(3 * time.Second)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newRealEnv(t, append([]Option{WithoutHWDetection()}, c.opts...)...)
			e.gen(t, "never.mkv", 4, "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac")
			info, err := e.m.Probe(t.Context(), e.url("never.mkv"))
			if err != nil {
				t.Fatal(err)
			}
			release := make(chan struct{})
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select { // the input never answers
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer source.Close()
			defer close(release)
			d := core.Decision{Kind: core.DecisionRemux, Container: ContainerMPEGTS, VideoCopy: true, AudioCopy: true, AudioStream: 1}
			hs, err := e.m.StartHLS(context.Background(), core.HLSRequest{SessionID: "never", InputURL: source.URL + "/never.mkv", Info: *info, Decision: d})
			if err != nil {
				t.Fatal(err)
			}
			defer hs.Close()
			client := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hs.ServeSegment(w, r, 0) }))
			defer client.Close()
			start := time.Now()
			resp, err := http.Get(client.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err == nil {
				t.Fatalf("a segment that never arrived ended as a finished %d with %d bytes", resp.StatusCode, len(body))
			}
			if len(body) == 0 || !bytes.Equal(bytes.ReplaceAll(body, tsNullPacket, nil), nil) {
				t.Fatalf("aborted body: %d bytes, not only padding", len(body))
			}
			if waited := time.Since(start); waited > 10*time.Second {
				t.Fatalf("abort after %v", waited)
			}
		})
	}
}
