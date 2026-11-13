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

	mbplayer "github.com/braedonsaunders/ether/internal/hlsplay/mediabrowser"
	plexplayer "github.com/braedonsaunders/ether/internal/hlsplay/plex"
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
	flag.StringVar(&c.SegmentBarrierDir, "segment-barrier-dir", "", "fresh session/index-bound request barrier directory")
	flag.IntVar(&c.PauseBeforeIndex, "pause-before-index", -1, "hold request for this segment until a verified fault release")
	flag.BoolVar(&c.ReencodedAudio, "reencoded-audio", false, "verify decoded PCM continuity against the reference instead of AAC AU equality")
	flag.BoolVar(&c.SkipDecision, "skip-decision", false, "diagnostic only: omit decision preflight (cannot pass acceptance)")
	flag.BoolVar(&c.PrivateHTTP, "private-http", true, "retain playlists and failed HTTP bodies privately, capped at 64 KiB each")
	flag.IntVar(&c.MaxVideoBitrate, "max-video-bitrate", 0, "optional client maxVideoBitrate (kbps)")
	flag.StringVar(&c.VideoResolution, "video-resolution", "", "optional client videoResolution")
	flag.BoolVar(&c.DirectStream, "direct-stream", false, "allow direct stream in client decision")
	flag.BoolVar(&c.DirectPlay, "direct-play", false, "allow direct play in client decision")
	flag.StringVar(&c.Protocol, "protocol", "plex", "plex, jellyfin or emby client path")
	flag.StringVar(&c.Prefix, "prefix", "", "MediaBrowser surface prefix")
	flag.StringVar(&c.DeviceID, "device-id", "", "bound viewer grant device ID (default random client ID)")
	flag.StringVar(&c.CredentialsFile, "credentials-file", "", "Jellyfin lab login JSON with username/password; credentials never printed")
	flag.StringVar(&c.DecisionOrigin, "decision-origin", "", "optional explicit lab frontend for Plex decision")
	flag.StringVar(&c.StartOrigin, "start-origin", "", "optional explicit lab frontend for Plex start")
	flag.StringVar(&c.StopOrigin, "stop-origin", "", "optional explicit lab frontend for Plex stop; media keeps ingress origin")
	flag.Parse()
	var token []byte
	var e error
	if tokenFile != "" {
		token, e = os.ReadFile(tokenFile)
	}
	if e != nil || (tokenFile == "" && c.CredentialsFile == "") {
		fmt.Fprintln(os.Stderr, "supply readable viewer token or Jellyfin credentials file")
		os.Exit(2)
	}
	c.Token = strings.TrimSpace(string(token))
	if reference != "" {
		c.Reference, c.ReferenceTimeline, e = hlsplay.LoadReference(reference)
		if e != nil {
			fmt.Fprintln(os.Stderr, "cannot read reference")
			os.Exit(2)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if c.Prefix == "" {
		c.Prefix = "/" + c.Protocol
	}
	switch c.Protocol {
	case "plex":
		c.NewControl = plexplayer.New
		c.PinControlOrigins = true
	case "jellyfin":
		c.NewControl = mbplayer.New("Jellyfin Web", "MediaBrowser")
		c.ReportStarted = true
	case "emby":
		c.NewControl = mbplayer.New("Emby Web", "Emby")
		c.ReportStarted = true
	default:
		fmt.Fprintln(os.Stderr, "unsupported protocol")
		os.Exit(2)
	}
	r, e := hlsplay.Run(ctx, c)
	fmt.Printf("outcome=%s segments=%d stalls=%d retries=%d\n", r.Outcome, len(r.Segments), len(r.Stalls), r.Retries)
	if e != nil || r.Outcome != "passed" {
		os.Exit(1)
	}
}
