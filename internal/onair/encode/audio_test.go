package encode

import (
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"math"
	"testing"
)

func TestAudio(t *testing.T) {
	for kind, target := range map[string]float64{"tone": -30, "static": -35.2, "bed": -35.4} {
		pcm, e := PCM(scene.Audio{Generator: kind, Seed: 7}, 4)
		if e != nil {
			t.Fatal(e)
		}
		if kind != "static" && (pcm[0] != 0 || pcm[len(pcm)-1] != 0) {
			t.Fatal("nonzero seam")
		}
		sum := 0.0
		for i := 0; i < len(pcm); i += 2 {
			if pcm[i] != pcm[i+1] {
				t.Fatal("stereo disagreement")
			}
			v := float64(pcm[i]) / 32767
			sum += v * v
		}
		rms := 20 * math.Log10(math.Sqrt(sum/float64(len(pcm)/2)))
		if math.Abs(rms-target) > 1 {
			t.Fatalf("%s RMS %f dBFS", kind, rms)
		}
	}
}
