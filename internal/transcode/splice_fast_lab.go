//go:build splicelab

package transcode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type spliceLabRunner struct {
	dir string
	mu  sync.Mutex
	seq int
}

func (r *spliceLabRunner) Run(ctx context.Context, c Command) error {
	return (ExecRunner{}).Run(ctx, c)
}
func (r *spliceLabRunner) Start(c Command) (Process, error) {
	a := append([]string(nil), c.Args...)
	for i, x := range a {
		if x == "-i" {
			a = append(a[:i], append([]string{"-readrate", "8"}, a[i:]...)...)
			break
		}
	}
	c.Args = a
	p, e := (ExecRunner{}).Start(c)
	if e != nil {
		return p, e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	first := 0
	for i, x := range a {
		if x == "-start_number" {
			first, _ = strconv.Atoi(a[i+1])
		}
	}
	b, _ := json.Marshal(map[string]any{"PID": p.(*execProcess).PID(), "First": first, "Run": r.seq, "Time": time.Now().UnixNano(), "Args": a})
	os.WriteFile(filepath.Join(r.dir, fmt.Sprintf("process-%d.json", r.seq)), b, 0600)
	tmp := filepath.Join(r.dir, "process.json.tmp")
	os.WriteFile(tmp, b, 0600)
	os.Rename(tmp, filepath.Join(r.dir, "process.json"))
	return p, nil
}

// SpliceFastLab drives the shipping manager, including automatic ramp-to-steady
// run changes. Only this lab wrapper throttles input and records owned PIDs.
func SpliceFastLab(ctx context.Context, ff, input, dir string, info core.MediaInfo, container string, first int, resume bool, limit int, fastStart, copyAudio bool, o SpliceLabOptions) error {
	scratch, e := os.MkdirTemp("", "splice-fast-manager-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(scratch)
	m, e := New(config.Transcode{FFmpegPath: ff, FFprobePath: filepath.Join(filepath.Dir(ff), "ffprobe"), TempDir: scratch, FastStart: fastStart}, slog.Default(), WithoutHWDetection(), WithRunner(&spliceLabRunner{dir: dir}), WithBuffering(24*time.Second, 0, 0))
	if e != nil {
		return e
	}
	defer m.Close()
	req := core.HLSRequest{SessionID: "fast-lab", InputURL: input, Info: info, Decision: spliceLabDecision(container, false, copyAudio, o), SegmentDuration: 4 * time.Second, Protocol: core.ProtocolHLS}
	if resume {
		admissionFirst := first
		if o.ResumeFirst >= 0 {
			admissionFirst = o.ResumeFirst
		}
		req.Resume = &core.HLSResume{FirstSegment: admissionFirst, Generation: 2}
	}
	started := time.Now()
	hs, e := m.StartHLS(ctx, req)
	if e != nil {
		return e
	}
	defer hs.Close()
	s := hs.(*Session)
	timeline := make([]time.Duration, s.total+1)
	for i := range timeline {
		timeline[i] = s.segStart(i)
	}
	b, _ := json.Marshal(timeline)
	os.WriteFile(filepath.Join(dir, "timeline.json"), b, 0600)
	playlist := httptest.NewRecorder()
	hs.ServePlaylist(playlist, httptest.NewRequestWithContext(ctx, "GET", "/index.m3u8", nil))
	os.WriteFile(filepath.Join(dir, "index.m3u8"), playlist.Body.Bytes(), 0600)
	if container == ContainerFMP4 {
		w := httptest.NewRecorder()
		hs.ServeSegment(w, httptest.NewRequestWithContext(ctx, "GET", "/init.mp4", nil), InitSegment)
		if w.Code != 200 {
			return fmt.Errorf("init HTTP%d", w.Code)
		}
		os.WriteFile(filepath.Join(dir, "init.mp4"), w.Body.Bytes(), 0600)
	}
	for n := first; n < s.total && (limit <= 0 || n < limit); n++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		w := httptest.NewRecorder()
		hs.ServeSegment(w, httptest.NewRequestWithContext(ctx, "GET", "/segment", nil), n)
		if e := ctx.Err(); e != nil {
			return e
		}
		if w.Code != 200 || w.Body.Len() == 0 {
			return fmt.Errorf("segment%d HTTP%d bytes%d", n, w.Code, w.Body.Len())
		}
		if n == 0 {
			b, _ := json.Marshal(map[string]any{"TTFFSeconds": time.Since(started).Seconds()})
			os.WriteFile(filepath.Join(dir, "metrics.json"), b, 0600)
		}
		if e := os.WriteFile(filepath.Join(dir, segmentFile(n, segmentExt(container))), w.Body.Bytes(), 0600); e != nil {
			return e
		}
	}
	return nil
}
