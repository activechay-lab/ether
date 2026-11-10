//go:build transcodechaos

package plex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Six original DASH-IF 1a byte-range fragments, replayed only over loopback.
// The local manifest trims the 654-second vector to its first 30 seconds.
func chaosDASHIF(t *testing.T, dir string) {
	t.Helper()
	var body strings.Builder
	body.WriteString(`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT30S" minBufferTime="PT1.5S" profiles="urn:mpeg:dash:profile:isoff-on-demand:2011"><Period>`)
	for _, track := range []struct {
		name, codec, kind            string
		init, start, scale, duration int
		sizes                        []int
	}{
		{"video", "avc1.4d401f", "video", 885, 2489, 12288, 61440, []int{596337, 792752, 542402, 656760, 577930, 678627}},
		{"audio", "mp4a.40.29", "audio", 820, 2436, 48000, 239616, []int{20835, 20324, 20527, 20644, 20551, 20470}},
	} {
		name := "dashif-" + track.name + ".mp4"
		data, err := os.ReadFile(filepath.Join(os.Getenv("CHAOS_CORPUS"), name+".relay"))
		if os.IsNotExist(err) {
			t.Skip("DASH-IF public sample unavailable")
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&body, `<AdaptationSet mimeType="%s/mp4"><Representation id="%s" codecs="%s" bandwidth="2000000"><BaseURL>%s</BaseURL><SegmentList timescale="%d" duration="%d"><Initialization range="0-%d"/>`, track.kind, track.name, track.codec, name, track.scale, track.duration, track.init-1)
		start := track.start
		for _, size := range track.sizes {
			fmt.Fprintf(&body, `<SegmentURL mediaRange="%d-%d"/>`, start, start+size-1)
			start += size
		}
		body.WriteString(`</SegmentList></Representation></AdaptationSet>`)
	}
	body.WriteString(`</Period></MPD>`)
	if err := os.WriteFile(filepath.Join(dir, "live.mpd"), []byte(body.String()), 0600); err != nil {
		t.Fatal(err)
	}
}
