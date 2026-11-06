// splicecheck verifies local offline HLS artifacts. Supply directories or JSON
// containing Segments and Options (including generation-specific InitPath).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/splicecheck"
)

func directory(dir string, boundary int) ([]splicecheck.Segment, error) {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return nil, e
	}
	var out []splicecheck.Segment
	for _, entry := range entries {
		ext := filepath.Ext(entry.Name())
		if ext != ".ts" && ext != ".m4s" {
			continue
		}
		n, e := strconv.Atoi(strings.TrimSuffix(entry.Name(), ext))
		if e != nil {
			return nil, fmt.Errorf("segment filename must be a numeric index: %s", entry.Name())
		}
		s := splicecheck.Segment{Index: n, Path: filepath.Join(dir, entry.Name()), Generation: 1}
		if boundary >= 0 && n >= boundary {
			s.Generation = 2
		}
		if ext == ".m4s" {
			s.InitPath = filepath.Join(dir, "init.mp4")
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}
func main() {
	input := flag.String("input", "", "JSON file containing Segments and Options")
	candidate := flag.String("candidate", "", "candidate segment directory (numeric filenames; init.mp4 for fMP4)")
	reference := flag.String("reference", "", "uninterrupted reference segment directory")
	boundary := flag.Int("boundary", -1, "first segment of the replacement generation")
	grid := flag.Duration("grid", 4*time.Second, "authored segment duration")
	ffmpeg := flag.String("ffmpeg", "ffmpeg", "FFmpeg executable")
	ffprobe := flag.String("ffprobe", "ffprobe", "FFprobe executable")
	flag.Parse()
	var req struct {
		Segments []splicecheck.Segment
		Options  splicecheck.Options
	}
	var e error
	if *input != "" {
		f, err := os.Open(*input)
		if err != nil {
			e = err
		} else {
			e = json.NewDecoder(f).Decode(&req)
			f.Close()
		}
	} else if *candidate != "" {
		req.Options = splicecheck.Options{SegmentDuration: *grid, FFmpeg: *ffmpeg, FFprobe: *ffprobe}
		req.Segments, e = directory(*candidate, *boundary)
		if e == nil && *reference != "" {
			req.Options.Reference, e = directory(*reference, -1)
		}
	} else {
		e = fmt.Errorf("usage: splicecheck -candidate DIR -reference DIR [-boundary N] [-grid 4s], or -input artifacts.json")
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	r, e := splicecheck.Check(ctx, req.Segments, req.Options)
	json.NewEncoder(os.Stdout).Encode(r)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
