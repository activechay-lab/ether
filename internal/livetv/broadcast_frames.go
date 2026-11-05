package livetv

import (
	"encoding/binary"
	"fmt"

	"github.com/braedonsaunders/ether/internal/transcode"
)

func validateRawFormat(f transcode.RawFormat) error {
	if f.Width < 2 || f.Height < 2 || f.Width%2 != 0 || f.Height%2 != 0 || f.Width > 3840 || f.Height > 2160 || f.FPSNum < 1 || f.FPSDen < 1 || f.FPSNum > 120000 || f.FPSDen > 1001 || f.SampleRate != 48000 || f.Channels != 2 || f.PixelFormat != "yuv420p" {
		return fmt.Errorf("invalid broadcast working format")
	}
	return nil
}
func tickSamples(f transcode.RawFormat, n int64) int {
	rate := int64(f.SampleRate) * int64(f.FPSDen)
	fps := int64(f.FPSNum)
	return int((n+1)*rate/fps - n*rate/fps)
}
func validRawFrame(f transcode.RawFormat, frame transcode.RawFrame) bool {
	return len(frame.Video) == f.Width*f.Height*3/2 && len(frame.PCM)%(f.Channels*2) == 0
}

func blackFrame(f transcode.RawFormat, samples int) transcode.RawFrame {
	video := make([]byte, f.Width*f.Height*3/2)
	y := f.Width * f.Height
	for i := range video {
		if i < y {
			video[i] = 16
		} else {
			video[i] = 128
		}
	}
	return transcode.RawFrame{Video: video, PCM: make([]byte, samples*f.Channels*2)}
}

// pcmFade carries a 40 ms transition across tick boundaries. Interpolation is
// per sample, with the same gain on both channels.
type pcmFade struct {
	from      []byte
	at, total int
}

func (f *pcmFade) start(previous []byte, format transcode.RawFormat) {
	// Continue from the last emitted sample, never replay an old PCM block.
	// The outgoing endpoint decays while the new source fades in.
	width := format.Channels * 2
	f.from = f.from[:0]
	if len(previous) >= width {
		f.from = append(f.from, previous[len(previous)-width:]...)
	}
	f.at = 0
	f.total = format.SampleRate * 40 / 1000
}
func (f *pcmFade) apply(pcm []byte, channels int) {
	if len(f.from) == 0 || f.at >= f.total {
		return
	}
	for i := 0; i+channels*2 <= len(pcm) && f.at < f.total; i += channels * 2 {
		for ch := 0; ch < channels; ch++ {
			at := i + ch*2
			oldAt := (f.at*channels*2 + ch*2) % len(f.from)
			old := int64(int16(binary.LittleEndian.Uint16(f.from[oldAt:])))
			next := int64(int16(binary.LittleEndian.Uint16(pcm[at:])))
			value := (old*int64(f.total-f.at) + next*int64(f.at)) / int64(f.total)
			binary.LittleEndian.PutUint16(pcm[at:], uint16(int16(value)))
		}
		f.at++
	}
}
