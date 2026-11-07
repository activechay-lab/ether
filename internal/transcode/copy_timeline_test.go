package transcode

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/splicecheck"
)

// Opt-in pinned offline fixture: no generation or provider access in the test.
func TestCopyTimelineRealSplice(t *testing.T) {
	source, ff, fp := os.Getenv("ETHER_COPY_SOURCE"), os.Getenv("ETHER_COPY_FFMPEG"), os.Getenv("ETHER_COPY_FFPROBE")
	if source == "" || ff == "" || fp == "" {
		t.Skip("requires pinned ETHER_COPY_SOURCE/FFMPEG/FFPROBE")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != os.Getenv("ETHER_COPY_SHA256") {
		t.Fatal("fixture hash mismatch")
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(source))))
	defer srv.Close()
	input := srv.URL + "/" + filepath.Base(source)
	p, err := NewSourceProber(SourceProbeOptions{FFmpegPath: ff, FFprobePath: fp})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	info, err := p.Probe(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	sourcePackets := copyProofPackets(t, fp, source)
	for _, corrupt := range []func([]copyPacket){
		func(p []copyPacket) { p[0].DTS = nil },
		func(p []copyPacket) {
			for i := range p {
				if p[i].Stream == 0 {
					p[i].Duration++
					break
				}
			}
		},
		func(p []copyPacket) {
			for i := range p {
				if p[i].Stream == 1 {
					p[i].Data = ""
					break
				}
			}
		},
		func(p []copyPacket) {
			for i := range p {
				if p[i].Stream == 0 {
					p[i].Data = "\n00000000: 0000 0001 4100 0000  "
					break
				}
			}
		},
	} {
		packets := append([]copyPacket(nil), sourcePackets...)
		corrupt(packets)
		raw, _ := json.Marshal(struct {
			Packets []copyPacket `json:"packets"`
		}{packets})
		if proof, _ := parseCopyPackets(raw, 0, 1); proof != nil {
			t.Fatal("incomplete/irregular source proved")
		}
	}

	if info.CopyTimeline == nil {
		t.Fatalf("source not proven: %+v", info)
	}
	q := AuthorCopyTimeline(core.HLSRequest{Info: *info, Protocol: core.ProtocolHLS, InputURL: input, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioCodec: "aac", Container: "mpegts", AudioStream: 1, SubtitleStream: -1}})
	if !ValidCopyTimeline(q) {
		t.Fatal("proven request rejected")
	}
	// Mutating any clock/profile must close admission, without normalizing copy to encode.
	for _, mutate := range []func(*core.HLSRequest){
		func(r *core.HLSRequest) { r.Info.CopyTimeline = nil },
		func(r *core.HLSRequest) { r.Info.CopyTimeline.Step++ },
		func(r *core.HLSRequest) { r.Info.Keyframes[1].Time++ },
		func(r *core.HLSRequest) { r.Decision.AudioCopy = false },
		func(r *core.HLSRequest) { r.Ramp.Segments = 1 },
	} {
		bad := q
		bad.Info = core.CloneMediaInfo(q.Info)
		mutate(&bad)
		if ValidCopyTimeline(bad) {
			t.Fatal("invalid copy admitted")
		}
	}
	cfg := config.Transcode{FFmpegPath: ff, FFprobePath: fp, HWAccel: "none", TempDir: t.TempDir(), FastStart: true}
	m, err := New(cfg, testLogger(t), WithSegmentTimeout(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	q.SessionID = "copy-reference"
	ref, err := m.StartHLS(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	count := int(info.CopyTimeline.End / info.CopyTimeline.Step)
	reference := make([]splicecheck.Segment, count)
	joined := make([]splicecheck.Segment, count)
	for i := 0; i < count; i++ {
		name := filepath.Join(dir, "ref"+string(rune('0'+i))+".ts")
		if err := os.WriteFile(name, fetch(t, ref, i), 0600); err != nil {
			t.Fatal(err)
		}
		reference[i] = splicecheck.Segment{Index: i, Path: name, Generation: 1}
		joined[i] = reference[i]
	}
	m.Stop(q.SessionID)
	for _, cut := range []int{1, 2, 3} {
		q.SessionID = "copy-resume"
		q.Resume = &core.HLSResume{FirstSegment: cut, Generation: 2}
		resumed, err := m.StartHLS(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		copy(joined, reference)
		for i := cut; i < count; i++ {
			name := filepath.Join(dir, "next"+string(rune('0'+i))+".ts")
			if err := os.WriteFile(name, fetch(t, resumed, i), 0600); err != nil {
				t.Fatal(err)
			}
			joined[i] = splicecheck.Segment{Index: i, Path: name, Generation: 2}
		}
		m.Stop(q.SessionID)
		verifyCopySourceAUs(t, fp, sourcePackets, joined)
		timeline := make([]time.Duration, count+1)
		for i := range timeline {
			timeline[i] = time.Duration(i) * 1920 * time.Millisecond
		}
		report, err := splicecheck.Check(t.Context(), joined, splicecheck.Options{Timeline: timeline, Reference: reference, FFmpeg: ff, FFprobe: fp})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(report)
		t.Log(string(b))
		for _, r := range []splicecheck.Result{report.IDRStart, report.ClosedGOP, report.PTSContinuity, report.FrameCountVsReference, report.CleanDecode, report.AudioAUExact, report.AudioContinuity, report.GridDurations} {
			if !r.Pass {
				t.Fatalf("cut %d failed: %s", cut, r.Details)
			}
		}
	}
}

func copyProofPackets(t *testing.T, ffprobe, path string) []copyPacket {
	t.Helper()
	var out, diag bytes.Buffer
	err := (ExecRunner{}).Run(t.Context(), Command{Path: ffprobe, Args: []string{"-v", "error", "-show_packets", "-show_data", "-show_entries", "packet=stream_index,pts,dts,duration,pos,data", "-of", "json", path}, Stdout: &out, Stderr: &diag})
	if err != nil || diag.Len() != 0 {
		t.Fatalf("packet proof: %v %s", err, diag.String())
	}
	var doc struct {
		Packets []copyPacket `json:"packets"`
	}
	if json.Unmarshal(out.Bytes(), &doc) != nil {
		t.Fatal("bad packet probe")
	}
	return doc.Packets
}
func verifyCopySourceAUs(t *testing.T, fp string, source []copyPacket, segments []splicecheck.Segment) {
	t.Helper()
	var actual []copyPacket
	for _, seg := range segments {
		actual = append(actual, copyProofPackets(t, fp, seg.Path)...)
	}
	canonical := func(packets []copyPacket) [2][]string {
		var result [2][]string
		var origin [2]int64
		var seen [2]bool
		for _, p := range packets {
			if p.Stream < 0 || p.Stream > 1 || p.PTS == nil || p.DTS == nil {
				t.Fatal("unexpected stream/clock")
			}
			if !seen[p.Stream] {
				origin[p.Stream] = *p.PTS
				seen[p.Stream] = true
			}
			hash := sha256.Sum256(probeExtradata(p.Data))
			result[p.Stream] = append(result[p.Stream], fmt.Sprintf("%d/%d/%d/%x", *p.PTS-origin[p.Stream], *p.DTS-origin[p.Stream], p.Duration, hash))
		}
		return result
	}
	if !reflect.DeepEqual(canonical(source), canonical(actual)) {
		t.Fatal("copied source AU payload/PTS/DTS mapping changed")
	}
}
