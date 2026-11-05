package encode

import (
	"encoding/binary"
	"fmt"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"io"
	"math"
)

// PCM returns deterministic stereo 48 kHz signed PCM. Periodic sine components
// meet at zero, including band-limited noise (80–8000 Hz). No random stream seam.
func PCM(a scene.Audio, seconds int) ([]int16, error) {
	if seconds < 1 || seconds > 30 {
		return nil, fmt.Errorf("invalid audio duration")
	}
	if a.Generator != "tone" && a.Generator != "static" && a.Generator != "bed" {
		return nil, fmt.Errorf("unknown generator")
	}
	n := seconds * 48000
	mono := make([]float64, n)
	state := a.Seed + 7
	rand := func() float64 { state = state*1664525 + 1013904223; return float64(state) / 4294967296 }
	oscillator := func(freq, gain float64) {
		step := 2 * math.Pi * freq * float64(seconds) / float64(n-1)
		co, si := math.Cos(step), math.Sin(step)
		x, y := 1.0, 0.0
		for i := range mono {
			mono[i] += y * gain
			x, y = x*co-y*si, y*co+x*si
		}
	}
	switch a.Generator {
	case "tone":
		oscillator(1000, math.Pow(10, -30.0/20)*math.Sqrt2)
	case "bed":
		for _, hz := range []float64{110, 165, 220, 330} {
			oscillator(hz, .012)
		}
	case "static":
		for j := 0; j < 192; j++ {
			hz := float64(320+int(rand()*31680)) / float64(seconds) // quantize to integer loop harmonics
			oscillator(hz, rand()*2-1)
		}
		sum := 0.0
		for _, v := range mono {
			sum += v * v
		}
		gain := math.Pow(10, -30.0/20) / math.Sqrt(sum/float64(n))
		for i := range mono {
			mono[i] *= gain
		}
		oscillator(60, math.Pow(10, -42.0/20)*math.Sqrt2)
	}
	mono[0] = 0
	mono[n-1] = 0
	out := make([]int16, n*2)
	for i, v := range mono {
		q := int16(math.Round(max(-1, min(1, v)) * 32767))
		out[i*2], out[i*2+1] = q, q
	}
	return out, nil
}
func WriteWAV(w io.Writer, a scene.Audio, seconds int) error {
	samples, e := PCM(a, seconds)
	if e != nil {
		return e
	}
	size := uint32(len(samples) * 2)
	head := make([]byte, 44)
	copy(head, "RIFF")
	binary.LittleEndian.PutUint32(head[4:], 36+size)
	copy(head[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(head[16:], 16)
	binary.LittleEndian.PutUint16(head[20:], 1)
	binary.LittleEndian.PutUint16(head[22:], 2)
	binary.LittleEndian.PutUint32(head[24:], 48000)
	binary.LittleEndian.PutUint32(head[28:], 192000)
	binary.LittleEndian.PutUint16(head[32:], 4)
	binary.LittleEndian.PutUint16(head[34:], 16)
	copy(head[36:], "data")
	binary.LittleEndian.PutUint32(head[40:], size)
	if _, e = w.Write(head); e != nil {
		return e
	}
	return binary.Write(w, binary.LittleEndian, samples)
}
