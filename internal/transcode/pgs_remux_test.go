package transcode

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/mediaproc"
)

// Exercise sparse PGS: its first presentation is well past the fast input
// analysis window, with a 1080p subtitle canvas on a 4K HEVC video.
func TestRealPGSRemux(t *testing.T) {
	e := newRealEnv(t)
	var sup []byte
	packet := func(pts uint32, kind byte, data []byte) {
		h := make([]byte, 13)
		copy(h, "PG")
		binary.BigEndian.PutUint32(h[2:], pts)
		h[10] = kind
		binary.BigEndian.PutUint16(h[11:], uint16(len(data)))
		sup = append(sup, h...)
		sup = append(sup, data...)
	}
	for _, pts := range []uint32{180000, 360000} {
		// Epoch start, one 2x2 white object at (100,100).
		packet(pts, 0x16, []byte{7, 128, 4, 56, 0x10, 0, 0, 0x80, 0, 0, 1, 0, 0, 0, 0, 0, 100, 0, 100})
		packet(pts, 0x17, []byte{1, 0, 0, 100, 0, 100, 0, 2, 0, 2})
		packet(pts, 0x14, []byte{0, 0, 1, 235, 128, 128, 255})
		packet(pts, 0x15, []byte{0, 0, 0, 0xc0, 0, 0, 12, 0, 2, 0, 2, 1, 1, 0, 0, 1, 1, 0, 0})
		packet(pts, 0x80, nil)
	}
	subPath := filepath.Join(e.dir, "sparse.sup")
	if err := os.WriteFile(subPath, sup, 0600); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(e.dir, "input.mkv")
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "color=size=3840x2160:rate=2:duration=6", "-i", subPath, "-map", "0:v", "-map", "1:s", "-c:v", "libx265", "-preset", "ultrafast", "-x265-params", "pools=1:frame-threads=1:log-level=error:keyint=2", "-threads", "1", "-c:s", "copy", input}
	if out, err := mediaproc.CommandContext(t.Context(), time.Minute, e.ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %.1500s", err, out)
	}
	info := core.MediaInfo{Container: "matroska", Duration: 6 * time.Second, Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, FrameRate: 2},
		{Index: 1, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Width: 1920, Height: 1080, Language: "eng"},
	}}
	for _, start := range []time.Duration{0, time.Second} {
		s := baseSpec(info, core.Decision{VideoCopy: true, VideoCodec: "hevc", Container: ContainerMatroska, SubtitleStream: 1})
		s.Input = input
		s.Start = start
		s.Dir = e.dir
		s.Playlist = "out.m3u8"
		s.InitName = "header.mkv"
		s.Protocol = core.ProtocolHLS
		a := buildArgs(s)
		if argValue(a, "-probesize") != "262144" || argValue(a, "-c:s") != "copy" {
			t.Fatalf("args: %v", a)
		}
		out, err := mediaproc.CommandContext(t.Context(), 30*time.Second, e.ffmpeg, a...).CombinedOutput()
		if err != nil {
			t.Fatalf("remux: %v: %.2000s", err, out)
		}
		header, err := os.ReadFile(filepath.Join(e.dir, "header.mkv"))
		if err != nil {
			t.Fatal(err)
		}
		segments, _ := filepath.Glob(filepath.Join(e.dir, "[0-9]*.mkv"))
		if len(segments) == 0 {
			t.Fatal("no segments")
		}
		var joined []byte
		joined = append(joined, header...)
		for _, f := range segments {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			joined = append(joined, b...)
		}
		path := filepath.Join(e.dir, "joined.mkv")
		if err := os.WriteFile(path, joined, 0600); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, st := range e.probeFile(t, path) {
			if st.CodecName == "hdmv_pgs_subtitle" {
				found = true
			}
		}
		if !found {
			t.Fatal("no PGS track in remux")
		}
		packets, err := mediaproc.CommandContext(t.Context(), 30*time.Second, e.ffprobe, "-v", "error", "-select_streams", "s:0", "-show_entries", "packet=size", "-of", "csv=p=0", path).Output()
		if err != nil || strings.TrimSpace(string(packets)) == "" {
			t.Fatalf("missing PGS packets: %v", err)
		}

	}
}
