package transcode

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
	// The source answers its first request late: segment 0 is not ready
	// for about 2.5 s.
	var slow atomic.Bool
	files := http.FileServer(http.Dir(e.dir))
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slow.CompareAndSwap(true, false) {
			time.Sleep(2500 * time.Millisecond)
		}
		files.ServeHTTP(w, r)
	}))
	defer source.Close()
	for _, c := range []struct {
		container string
		unit      []byte
	}{{ContainerMPEGTS, tsNullPacket}, {ContainerFMP4, fmp4FreeBox}} {
		t.Run(c.container, func(t *testing.T) {
			slow.Store(true)
			d := core.Decision{Kind: core.DecisionRemux, Container: c.container, VideoCopy: true, AudioCopy: true, AudioStream: 1}
			hs, err := e.m.StartHLS(context.Background(), core.HLSRequest{SessionID: "pad-" + c.container, InputURL: source.URL + "/pad.mkv", Info: *info, Decision: d})
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
			first := make([]byte, len(c.unit))
			if _, err := io.ReadFull(resp.Body, first); err != nil {
				t.Fatal(err)
			}
			if wait := time.Since(start); wait > 2*time.Second || !bytes.Equal(first, c.unit) {
				t.Fatalf("first bytes after %v: % x", wait, first[:8])
			}
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
