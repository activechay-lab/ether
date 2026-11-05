// Package livetvtest supplies deterministic runner fakes for protocol adapters.
package livetvtest

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/transcode"
	"io"
	"net/http"
	"time"
)

type Encoder struct{ Index transcode.SegmentIndex }

func (e *Encoder) Write(context.Context, transcode.RawFrame) error { return nil }
func (e *Encoder) Segments() transcode.SegmentIndex                { return e.Index }
func (e *Encoder) Close() error                                    { return nil }

type Decoder struct{}

func (*Decoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	<-ctx.Done()
	return transcode.RawFrame{}, ctx.Err()
}
func (*Decoder) Close() error { return nil }

type Index struct {
	Payload string
	Height  int
}

func (i Index) Latest() (transcode.Segment, bool) {
	return transcode.Segment{Seq: 5, Start: time.Unix(20, 0).UTC(), Duration: 4 * time.Second}, true
}
func (i Index) Window(from, until time.Time) []transcode.Segment {
	var out []transcode.Segment
	for n := int64(0); n < 6; n++ {
		seg := transcode.Segment{Seq: n, Start: time.Unix(n*4, 0).UTC(), Duration: 4 * time.Second}
		if seg.Start.Before(until) && seg.Start.Add(seg.Duration).After(from) {
			out = append(out, seg)
		}
	}
	return out
}
func (i Index) Since(seq int64, max int) []transcode.Segment {
	var out []transcode.Segment
	for n := seq + 1; n < 6 && len(out) < max; n++ {
		if n >= 0 {
			out = append(out, transcode.Segment{Seq: n, Start: time.Unix(n*4, 0).UTC(), Duration: 4 * time.Second})
		}
	}
	return out
}
func (i Index) Wait(ctx context.Context, after int64) (transcode.Segment, error) {
	if segments := i.Since(after, 1); len(segments) > 0 {
		return segments[0], nil
	}
	<-ctx.Done()
	return transcode.Segment{}, ctx.Err()
}
func (i Index) ServeTS(w http.ResponseWriter, _ *http.Request, seq int64) {
	w.Header().Set("Content-Type", "video/mp2t")
	fmt.Fprintf(w, "%s%d", i.Payload, seq)
}
func (i Index) ServeInit(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "init-shared") }
func (i Index) ServeFMP4(w http.ResponseWriter, _ *http.Request, seq int64) {
	fmt.Fprintf(w, "media-shared-%d", seq)
}
func (i Index) AttachTS(context.Context) (io.ReadCloser, error) { return nil, io.EOF }
func (i Index) Format() (string, int, int, int) {
	height := i.Height
	if height == 0 {
		height = 720
	}
	return "avc1.640028,mp4a.40.2", height * 16 / 9, height, 3000000
}
