package livetv

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"

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

// frameRing owns decoder buffers. The flattened audio cursor is independent of
// video frame count, preserving fractional-rate audio across loop seams.
type frameRing struct {
	frames  []transcode.RawFrame
	pcm     []byte
	audioAt int
	videoAt int
}

func readFrameRing(ctx context.Context, d transcode.Decoder, f transcode.RawFormat, maxBytes int64) (*frameRing, error) {
	defer d.Close()
	r := &frameRing{}
	var used int64
	for {
		frame, err := d.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if !validRawFrame(f, frame) {
			return nil, fmt.Errorf("invalid decoded ring frame")
		}
		used += int64(len(frame.Video) + len(frame.PCM))
		if used > maxBytes {
			return nil, fmt.Errorf("decoded ring exceeds memory budget")
		}
		r.frames = append(r.frames, transcode.RawFrame{Video: frame.Video})
		r.pcm = append(r.pcm, frame.PCM...)
	}
	if len(r.frames) == 0 || len(r.pcm) == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return r, nil
}
func (r *frameRing) next(samples, channels int) transcode.RawFrame {
	frame := transcode.RawFrame{Video: r.frames[r.videoAt].Video, PCM: make([]byte, samples*channels*2)}
	r.videoAt = (r.videoAt + 1) % len(r.frames)
	for at := 0; at < len(frame.PCM); {
		n := copy(frame.PCM[at:], r.pcm[r.audioAt:])
		at += n
		r.audioAt = (r.audioAt + n) % len(r.pcm)
	}
	return frame
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
	f.from = append(f.from[:0], previous...)
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
