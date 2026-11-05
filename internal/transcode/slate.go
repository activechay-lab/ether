package transcode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const slateCacheLimit = 32 << 20

// slate caches a four-second loop, independent of slot length and join offset.
// The caller keeps its own session copy so cache eviction cannot break seeks.
func (m *Manager) slate(ctx context.Context, info core.MediaInfo, lines []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	m.slateMu.Lock()
	defer m.slateMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v := info.Video()
	if v == nil || v.Width < 2 || v.Height < 2 || v.Width > 4096 || v.Height > 2160 {
		return nil, fmt.Errorf("invalid slate dimensions")
	}
	key, _ := json.Marshal(struct {
		Streams []core.StreamInfo
		Lines   []string
	}{info.Streams, lines})
	dir := filepath.Join(m.tempDir, "slates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("%x.mp4", sha256.Sum256(key)))
	if data, err := os.ReadFile(path); err == nil {
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		return data, nil
	}
	work, err := os.MkdirTemp(dir, "render-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	card := image.NewRGBA(image.Rect(0, 0, v.Width, v.Height))
	draw.Draw(card, card.Bounds(), image.NewUniform(color.RGBA{18, 26, 40, 255}), image.Point{}, draw.Src)
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	for i, line := range lines {
		size := float64(v.Height) / 14
		face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		width := font.MeasureString(face, line).Ceil()
		if width > v.Width*9/10 {
			face.Close()
			face, err = opentype.NewFace(parsed, &opentype.FaceOptions{Size: size * float64(v.Width*9/10) / float64(width), DPI: 72})
			if err != nil {
				return nil, err
			}
		}
		d := font.Drawer{Dst: card, Src: image.White, Face: face, Dot: fixed.P((v.Width-font.MeasureString(face, line).Ceil())/2, v.Height/2+(i-1)*v.Height/8)}
		d.DrawString(line)
		face.Close()
	}
	pngPath := filepath.Join(work, "card.png")
	f, err := os.Create(pngPath)
	if err != nil {
		return nil, err
	}
	err = png.Encode(f, card)
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	channels, rate, audio, sourceLayout := 2, 48000, "aac", ""
	for _, st := range info.Streams {
		if st.Type == "audio" {
			channels, rate, audio, sourceLayout = st.Channels, st.SampleRate, st.Codec, st.ChannelLayout
			break
		}
	}
	layout := "stereo"
	switch channels {
	case 3:
		layout = "2.1"
	case 4:
		layout = "quad"
	case 5:
		layout = "5.0"
	case 7:
		layout = "6.1"
	}
	if channels == 1 {
		layout = "mono"
	}
	if channels == 6 {
		layout = "5.1"
	}
	if channels == 8 {
		layout = "7.1"
	}
	if sourceLayout == "5.1(side)" || sourceLayout == "7.1(wide)" {
		layout = sourceLayout
	}
	encoder := "libx264"
	if v.Codec == "hevc" {
		encoder = "libx265"
	}
	switch v.Codec {
	case "vp9":
		encoder = "libvpx-vp9"
	case "av1":
		encoder = "libaom-av1"
	}
	switch audio {
	case "opus":
		audio = "libopus"
	case "mp3":
		audio = "libmp3lame"
	}
	output := filepath.Join(work, "slate.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-loop", "1", "-framerate", "25", "-i", pngPath, "-f", "lavfi", "-i", fmt.Sprintf("anullsrc=r=%d:cl=%s", rate, layout), "-t", "4", "-map", "0:v", "-map", "1:a", "-c:v", encoder, "-threads", "1", "-pix_fmt", "yuv420p", "-g", "100", "-bf", "0", "-c:a", audio, "-ar", fmt.Sprint(rate), "-ac", fmt.Sprint(channels), "-movflags", "+faststart", output}
	if encoder == "libx265" {
		args = append(args[:len(args)-1], "-preset", "veryfast", "-x265-params", "pools=1:frame-threads=1", output)
	} else if encoder == "libx264" {
		profile := strings.ToLower(v.Profile)
		if profile == "constrained baseline" {
			profile = "baseline"
		}
		if profile != "high" && profile != "main" && profile != "baseline" {
			profile = "high"
		}
		args = append(args[:len(args)-1], "-preset", "veryfast", "-profile:v", profile, output)
	}
	if encoder == "libvpx-vp9" {
		args = append(args[:len(args)-1], "-deadline", "realtime", "-cpu-used", "8", output)
	}
	if encoder == "libaom-av1" {
		args = append(args[:len(args)-1], "-cpu-used", "8", output)
	}
	if audio == "flac" {
		args = append(args[:len(args)-1], "-strict", "-2", output)
	}
	var stderr bytes.Buffer
	if err := m.runner.Run(ctx, Command{Path: m.ffmpeg, Args: args, Stderr: &stderr}); err != nil {
		return nil, fmt.Errorf("render off-air slate: %w: %.1024s", err, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return nil, err
	}
	if len(data) > slateCacheLimit {
		return nil, fmt.Errorf("slate exceeds cache limit")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type cached struct {
		path string
		size int64
		at   time.Time
	}
	var files []cached
	total := int64(len(data))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".mp4" {
			continue
		}
		st, err := e.Info()
		if err != nil {
			continue
		}
		total += st.Size()
		files = append(files, cached{filepath.Join(dir, e.Name()), st.Size(), st.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.Before(files[j].at) })
	for _, f := range files {
		if total <= slateCacheLimit {
			break
		}
		if err := os.Remove(f.path); err != nil {
			return nil, err
		}
		total -= f.size
	}
	if err := os.Rename(output, path); err != nil {
		return nil, err
	}
	return data, nil
}
