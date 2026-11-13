package transcode

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/mediaproc"
)

// A real ranged Matroska request distinguishes first media byte from whole
// response transfer time. Copy video plus TrueHD->EAC3 matches the device path.
func TestIntegrationResumeSegmentFirstByte(t *testing.T) {
	e := newRealEnv(t, withPollInterval(150*time.Millisecond))
	e.gen(t, "short.mkv", 6, "-c:v", "libx264", "-preset", "ultrafast", "-crf", "40", "-g", "24", "-threads", "2", "-c:a", "truehd", "-strict", "-2", "-ac", "2")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out, err := mediaproc.CommandContext(ctx, time.Minute, e.ffmpeg, "-v", "error", "-y", "-stream_loop", "-1", "-i", filepath.Join(e.dir, "short.mkv"), "-t", "300", "-c", "copy", filepath.Join(e.dir, "long.mkv")).CombinedOutput()
	if err != nil {
		t.Fatalf("loop fixture: %v %s", err, out)
	}
	info, err := e.m.Probe(ctx, e.url("long.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	caps := appleTV
	caps.HLSContainers = []string{"mkv"}
	opts := allow()
	opts.DirectPlay = false
	d := e.m.Decide(*info, caps, opts)
	d.AudioCodec = "eac3"
	if !d.VideoCopy || d.AudioCopy || d.Container != ContainerMatroska {
		t.Fatalf("wrong path %+v", d)
	}
	hs, err := e.m.StartHLS(ctx, core.HLSRequest{SessionID: "resume-firstbyte", InputURL: e.url("long.mkv"), Info: *info, Decision: d, SegmentDuration: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hs.ServeSegment(w, r, 133) }))
	defer server.Close()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	req.Header.Set("Range", "bytes=0-")
	start := time.Now()
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, err = io.ReadFull(response.Body, make([]byte, 1))
	if err != nil {
		t.Fatal(err)
	}
	first := time.Since(start)
	n, err := io.Copy(io.Discard, response.Body)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if response.StatusCode != http.StatusPartialContent {
		t.Fatalf("segment status %d", response.StatusCode)
	}
	s := hs.(*Session)
	s.mu.Lock()
	run := s.cur
	seq := s.runSeq
	s.mu.Unlock()
	if run == nil || run.startSeg != 133 || seq != 2 {
		t.Fatalf("far first fetch did not restart once: run=%+v count=%d", run, seq)
	}
	t.Logf("segment133 video-copy/audio-transcode firstByte=%s complete=%s bytes=%d runs=%d", first, elapsed, n+1, seq)
}
