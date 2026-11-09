package onair

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/onair/encode"
	"strconv"

	"github.com/braedonsaunders/ether/internal/mediaproc"
)

func (s *Service) renderMomentVideo(ctx context.Context, j renderJob, out string, sounds []encode.SoundInput) error {
	if s.o.FFmpeg == nil || s.o.Probe == nil {
		return fmt.Errorf("media conversion unavailable")
	}
	var kind string
	if e := s.db.QueryRowContext(ctx, "SELECT kind FROM onair_assets WHERE digest=?", j.AssetDigest).Scan(&kind); e != nil {
		return e
	}
	if kind != "video" {
		return ErrNotFound
	}
	source, e := s.assetPath(ctx, j.AssetDigest)
	if e != nil {
		return e
	}
	// Supply silence when necessary, keeping authored picture and sound together.
	info, e := s.o.Probe(ctx, source)
	if e != nil {
		return e
	}
	hasAudio := false
	for _, st := range info.Streams {
		hasAudio = hasAudio || st.Type == "audio"
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-ss", strconv.FormatFloat(func() float64 {
		if j.Request.AssetDigest == "" && j.Scene.Video != nil {
			return j.Scene.Video.Start
		}
		return 0
	}(), 'f', 6, 64), "-i", source}
	if !hasAudio {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo")
	}

	audioBase := "0:a:0"
	first := 1
	if !hasAudio {
		audioBase = "1:a:0"
		first = 2
	}
	for _, snd := range sounds {
		args = append(args, "-i", snd.Path)
	}
	audioMap := audioBase
	if len(sounds) > 0 {
		args = append(args, "-filter_complex", encode.MixFilter(audioBase, sounds, first, j.Doc.Duration))
		audioMap = "[mixed]"
	}
	args = append(args, "-map", "0:v:0", "-map", audioMap)

	args = append(args, "-t", strconv.FormatFloat(j.Doc.Duration, 'f', 6, 64), "-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,format=yuv420p,tpad=stop_mode=clone:stop_duration=30", "-af", "apad", "-r", "30", "-c:v", "libx264", "-threads", "2", "-preset", "medium", "-crf", "20", "-c:a", "aac", "-ar", "48000", "-ac", "2", "-movflags", "+faststart", out)
	return mediaproc.CommandContext(ctx, mediaproc.EncodeTimeout, s.o.FFmpeg(), args...).Run()
}

// Crossfade is read from saved look metadata by the background input planner.
func (s *Service) Crossfade(look, moment string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.looks[look].Scenes {
		if sc.Role == moment && sc.Enabled && sc.Video != nil && sc.Video.Crossfade {
			return true
		}
	}
	return false
}
