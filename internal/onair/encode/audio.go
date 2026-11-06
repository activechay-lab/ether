package encode

import (
	"encoding/binary"
	"fmt"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"github.com/braedonsaunders/ether/internal/onair/signal"
	"io"
	"math"
)

// PCM returns deterministic stereo 48 kHz signed PCM. Static masters use the
// same band-limited hiss as the live Signal engine; tone and bed remain periodic.
func PCM(a scene.Audio, seconds int) ([]int16, error) {
	if seconds < 1 || seconds > 600 {
		return nil, fmt.Errorf("invalid audio duration")
	}
	if a.Generator != "tone" && a.Generator != "static" && a.Generator != "bed" {
		return nil, fmt.Errorf("unknown generator")
	}
	n := seconds * 48000
	if a.Generator == "static" {
		out := make([]int16, n*2)
		for tick := 0; tick < seconds*30; tick++ {
			signal.StaticAudio(out[tick*3200:(tick+1)*3200], int64(tick), 1600, int64(a.Seed))
		}
		return out, nil
	}
	mono := make([]float64, n)
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
