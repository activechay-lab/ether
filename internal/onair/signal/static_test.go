package signal

import (
	"image"
	"math"
	"testing"
)

func BenchmarkStatic1080p30(b *testing.B) {
	video := make([]byte, 1920*1080*3/2)
	audio := make([]int16, 3200)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		StaticVideo(video, 1920, 1080, 7, int64(n))
		StaticAudio(audio, int64(n), 1600, 7)
	}
}

func TestStaticContinuity(t *testing.T) {
	a, b := make([]byte, 320*180*3/2), make([]byte, 320*180*3/2)
	StaticVideo(a, 320, 180, 7, 0)
	StaticVideo(b, 320, 180, 7, 0)
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("non-deterministic video")
		}
		if a[i] < 16 || a[i] > 235 {
			t.Fatal("invalid studio-range sample")
		}
	}
	StaticVideo(b, 320, 180, 7, 120)
	equal := true
	for i := range a {
		if a[i] != b[i] {
			equal = false
			break
		}
	}
	if equal {
		t.Fatal("four-second static repeat")
	}
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
	e := NewStatic(1920, 1080, "classic", 7)
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
