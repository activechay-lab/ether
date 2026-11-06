// Command hlsplay simulates a bounded Plex HLS player against an explicit lab.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/braedonsaunders/ether/internal/hlsplay"
)

func main() {
	var c hlsplay.Config
	var tokenFile, reference string
	flag.StringVar(&c.BaseURL, "url", "", "lab ingress origin")
	flag.StringVar(&tokenFile, "token-file", "", "private file containing lab viewer token")
	flag.StringVar(&c.RatingKey, "rating-key", "", "generated lab title key")
	flag.StringVar(&c.Output, "output", "", "new artifact directory outside git")
	flag.StringVar(&c.Session, "session", "", "unique Plex session (default random)")
	flag.StringVar(&c.Host, "host", "", "optional ingress Host for a port-forward")
	flag.DurationVar(&c.Window, "window", 90*time.Second, "bounded wall-clock observation window, including startup")
	flag.DurationVar(&c.ForwardBuffer, "buffer", 30*time.Second, "startup and forward buffer target")
	flag.DurationVar(&c.RequestTimeout, "timeout", 5*time.Second, "per-request timeout")
	flag.DurationVar(&c.Backoff, "backoff", 200*time.Millisecond, "retry exponential backoff base (caps at 16x)")
	flag.DurationVar(&c.SegmentDuration, "segment-duration", 4*time.Second, "fixed segment grid")
	flag.IntVar(&c.Tries, "tries", 8, "maximum attempts on 404, 5xx or transport failure")
	flag.StringVar(&c.FFmpeg, "ffmpeg", "ffmpeg", "decode checker executable")
	flag.StringVar(&c.FFprobe, "ffprobe", "ffprobe", "packet checker executable")
	flag.StringVar(&reference, "reference", "", "prior no-loss report.json for exact audio AU comparison")
	flag.StringVar(&c.ProducerEvidence, "producer-evidence", "", "trusted observer JSON array of session/segmentIndex/generation rows")
	flag.Parse()
	token, e := os.ReadFile(tokenFile)
	if e != nil {
		fmt.Fprintln(os.Stderr, "cannot read viewer token file")
		os.Exit(2)
	}
	c.Token = strings.TrimSpace(string(token))
	if reference != "" {
		c.Reference, e = hlsplay.LoadReference(reference)
		if e != nil {
			fmt.Fprintln(os.Stderr, "cannot read reference")
			os.Exit(2)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	r, e := hlsplay.Run(ctx, c)
	fmt.Printf("outcome=%s segments=%d stalls=%d retries=%d\n", r.Outcome, len(r.Segments), len(r.Stalls), r.Retries)
	if e != nil || r.Outcome != "passed" {
		os.Exit(1)
	}
}
