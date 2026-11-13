package transcode

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// A remembered language must map to the selected file's own absolute index.
func TestAlternateVersionSubtitleMap(t *testing.T) {

	alternate := core.MediaInfo{Container: "mkv", Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
		{Index: 1, Type: "audio", Codec: "aac", Channels: 2},
		{Index: 2, Type: "subtitle", Codec: "srt", Language: "fra"},
		{Index: 7, Type: "subtitle", Codec: "srt", Language: "eng"},
	}}
	// The playback invariant test resolves the remembered English choice to index 7.
	pos := 3
	opts := allow()
	opts.SubtitleStream = alternate.Streams[pos].Index
	caps := appleTV
	caps.Containers = []string{"mkv"}
	d := Decide(alternate, caps, opts, Policy{})
	d.Kind = core.DecisionRemux
	d.Container = "mkv"
	d.SubtitleStream = opts.SubtitleStream
	args := strings.Join(buildArgs(baseSpec(alternate, d)), " ")
	if !strings.Contains(args, "-map 0:7") || strings.Contains(args, "-map 0:2") {
		t.Fatalf("alternate subtitle map: %s", args)
	}
}
