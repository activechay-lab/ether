//go:build splicelab

// splicelab prints the real HLS command arguments for synthetic offline media.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/braedonsaunders/ether/internal/transcode"
	"os"
	"os/signal"
)

func main() {
	input := flag.String("input", "", "fixture")
	probe := flag.String("probe", "", "ffprobe JSON")
	dir := flag.String("dir", "", "output directory")
	container := flag.String("container", "mpegts", "mpegts or fmp4")
	first := flag.Int("first", 0, "first segment")
	copyVideo := flag.Bool("copy-video", false, "remux source keyframe grid")
	fast := flag.Bool("fast-start", false, "drive the real FastStart manager")
	ff := flag.String("ffmpeg", "ffmpeg", "pinned FFmpeg")
	resume := flag.Bool("resume", false, "explicit Resume, including segment0")
	limit := flag.Int("limit", 0, "stop real manager after this exclusive segment index")
	flag.Parse()
	b, e := os.ReadFile(*probe)
	if e != nil {
		panic(e)
	}
	info, e := transcode.ParseProbe(b)
	if e != nil {
		panic(e)
	}
	if *fast {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		if e := transcode.SpliceFastLab(ctx, *ff, *input, *dir, *info, *container, *first, *resume, *limit); e != nil {
			os.Stderr.WriteString(e.Error() + "\n")
			os.Exit(1)
		}
		return
	}
	json.NewEncoder(os.Stdout).Encode(transcode.SpliceLabArgs(*input, *dir, *info, *container, *first, *copyVideo))
}
