package playback

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestAlternateVersionSubtitleTracks(t *testing.T) {
	ctx := context.Background()
	probes := 0
	h := newHarness(t, []*fakeRelease{mkv("A", 10000, time.Minute), mkv("B", 20000, time.Minute)}, []string{"A", "B"},
		withNativeProbe(func(_ context.Context, src core.MediaSource) (*core.MediaInfo, error) {
			probes++
			tracks := []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
				{Index: 1, Type: "audio", Codec: "aac", Language: "eng", Channels: 2},
				{Index: 2, Type: "subtitle", Codec: "srt", Language: "eng"},
			}
			if src.Size() == 20000 {
				tracks[2].Language = "fra"
				tracks = append(tracks, core.StreamInfo{Index: 7, Type: "subtitle", Codec: "srt", Language: "eng"})
			}
			return &core.MediaInfo{Container: "mkv", Size: src.Size(), Duration: time.Minute, Streams: tracks}, nil
		}, nil))
	caps := defaultCaps("version-subs")
	primary, err := h.m.PrepareExactVariant(ctx, owner, movieKey, caps, "A")
	if err != nil {
		t.Fatal(err)
	}
	h.m.waitResumeSaves()
	selected := primary.Info.Streams[2]
	alt, err := h.m.PrepareExactVariant(ctx, owner, movieKey, caps, "B")
	if err != nil {
		t.Fatal(err)
	}
	if alt.Resolution.Release.ID != "B" || len(alt.Info.Streams) != 4 || alt.Info.Streams[2].Language != "fra" {
		t.Fatalf("alternate returned another release's tracks: %+v", alt)
	}
	pos := frontend.ResolveSubtitle(&selected, 2, alt.Info)
	if pos < 0 || alt.Info.Streams[pos].Index != 7 {
		t.Fatalf("language choice did not follow alternate: %d", pos)
	}
	h.m.waitResumeSaves()
	for _, id := range []string{"A", "B"} {
		info, ok := h.m.CachedVariantInfo(ctx, owner.ID, movieKey, id)
		if !ok || len(info.Streams) < 3 {
			t.Fatalf("lost persisted streams for %s: %+v", id, info)
		}
	}
	again, err := h.m.PrepareExactVariant(ctx, owner, movieKey, caps, "A")
	if err != nil || again.Info.Streams[2].Language != "eng" {
		t.Fatalf("reselect primary: %+v %v", again, err)
	}
	if probes != 2 {
		t.Fatalf("expected one probe per selected release, got %d", probes)
	}
}
