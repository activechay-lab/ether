package signal

import (
	"image"
	"math"
	"testing"
)

func TestStaticAudio(t *testing.T) {
	pcm := make([]int16, 3200)
	sum := 0.0
	for tick := int64(0); tick < 30; tick++ {
		StaticAudio(pcm, tick, 1600, 7)
		for i := 0; i < 1600; i++ {
			v := float64(pcm[i*2]) / 32767
			sum += v * v
		}
	}
	rms := math.Sqrt(sum / 48000)
	if rms < .026 || rms > .039 {
		t.Fatalf("hiss RMS %f", rms)
	}
}

func BenchmarkLiveWarm1080p30(b *testing.B) {
	e := NewStatic(1920, 1080, 7)
	video := make([]byte, 1920*1080*3/2)
	pcm := make([]int16, 3200)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		e.Render(video, pcm, int64(n))
	}
}

func BenchmarkLivePlate1080p30(b *testing.B) {
	plate := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	e := NewPlate(1920, 1080, "bars", plate, nil, 7)
	video := make([]byte, 1920*1080*3/2)
	pcm := make([]int16, 3200)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		e.Render(video, pcm, int64(n))
	}
}

func TestLiveEngineTicks(t *testing.T) {
	a, b := NewStatic(320, 180, 7), NewStatic(320, 180, 7)
	av, bv := make([]byte, 320*180*3/2), make([]byte, 320*180*3/2)
	ap, bp := make([]int16, 3200), make([]int16, 3200)
	var first uint64
	for tick := int64(0); tick < 121; tick++ {
		a.Render(av, ap, tick)
		b.Render(bv, bp, tick)
		hash := uint64(0)
		sum := 0
		for i, v := range av {
			if v != bv[i] {
				t.Fatal("non-deterministic engine")
			}
			if i < 320*180 {
				sum += int(v)
				hash = hash*131 + uint64(v)
			}
		}
		for i, v := range ap {
			if v != bp[i] {
				t.Fatal("non-deterministic audio")
			}
		}
		if sum/(320*180) < 32 {
			t.Fatal("black live engine")
		}
		if tick == 0 {
			first = hash
		}
		if tick == 120 && hash == first {
			t.Fatal("four-second repeat")
		}
	}
}
