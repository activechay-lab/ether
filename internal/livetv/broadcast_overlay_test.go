package livetv

import (
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/onair/families"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/transcode"
)

func BenchmarkBroadcastOverlays(b *testing.B) {
	for _, height := range []int{720, 1080} {
		b.Run(fmt.Sprint(height), func(b *testing.B) {
			f := transcode.RawFormat{Width: height * 16 / 9, Height: height}
			overlays := &programmeOverlays{}
			bindings := raster.Bindings{Bindings: families.Defaults("clean")}
			bindings.Slots["channel.name"] = "MERIDIAN"
			bindings.Slots["now.title"] = "The Evening Programme"
			bindings.Slots["now.meta"] = "A shared continuous channel"
			for _, moment := range []string{"bug", "lowerthird"} {
				doc, err := families.Get("clean", moment)
				if err != nil {
					b.Fatal(err)
				}
				overlay := compactOverlay(raster.Render(doc, bindings, doc.Poster, f.Width, f.Height), f)
				if moment == "bug" {
					overlays.bug = overlay
				} else {
					overlays.lower = overlay
				}
			}
			raw := blackFrame(f, 0).Video
			video := make([]byte, len(raw))
			start := time.Now()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(video, raw)
				overlays.blend(video, f, start.Add(2*time.Second), start, start.Add(time.Hour))
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)*30/1e9, "CPU-cores@30fps")
		})
	}
}
