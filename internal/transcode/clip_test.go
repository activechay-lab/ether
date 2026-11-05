package transcode

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrustedClipContract(t *testing.T) {
	root := t.TempDir()
	master := filepath.Join(root, "master.mp4")
	if err := os.WriteFile(master, []byte("fake master"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.mp4")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape.mp4")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{outside, link, root} {
		if _, err := trustedClipPath(root, bad); err == nil {
			t.Fatal("accepted outside/directory", bad)
		}
	}
	for _, loop := range []bool{false, true} {
		fr := newFakeRunner()
		m := newTestManager(t, fr, config.Transcode{}, WithClipCacheDir(root), WithoutHWDetection())
		info := core.MediaInfo{Duration: 16 * time.Second, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Width: 1280, Height: 720, FrameRate: 25}, {Index: 1, Type: "audio", Codec: "ac3", Channels: 6, SampleRate: 48000}}}
		hs, err := m.StartHLS(context.Background(), core.HLSRequest{SessionID: "clip", ClipPath: master, ClipLoop: loop, Info: info, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, AudioStream: 1, SubtitleStream: -1}, SegmentDuration: 4 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		s := hs.(*Session)
		canonical, _ := filepath.EvalSymlinks(master)
		if s.loopInput != loop || s.input != canonical || s.decision.VideoCopy || s.decision.AudioCopy || s.decision.VideoCodec != "hevc" || s.decision.AudioCodec != "ac3" || s.decision.AudioChannels != 6 {
			t.Fatalf("clip contract: %+v", s.sessionInput)
		}
		if s.contract == nil || s.contract.width != 1280 || s.contract.height != 720 || s.contract.frameRate != 25 || s.info.Video().Width != 1920 {
			t.Fatal("master/target picture contract lost")
		}
		if s.info.Streams[1].Channels != 2 {
			t.Fatal("master audio metadata lost")
		}
		hs.Close()
	}
}
