// Package livetvtest supplies deterministic runner fakes for protocol adapters.
package livetvtest

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/transcode"
	"io"
	"net/http"
	"sync"
	"time"
)

// Encoder stamps the planner's latest policy identities on every segment, as
// the real continuous index does, so adapters exercise tag authorization.
type Encoder struct {
	Index transcode.SegmentIndex
	mu    sync.Mutex
	tags  []any
	ready chan struct{}
}

func (e *Encoder) Write(_ context.Context, f transcode.RawFrame) error {
	if len(f.PolicyTags) > 0 {
		e.mu.Lock()
		if e.tags == nil {
			close(e.readyChan())
		}
		e.tags = append([]any(nil), f.PolicyTags...)
		e.mu.Unlock()
	}
	return nil
}
func (e *Encoder) readyChan() chan struct{} {
	if e.ready == nil {
		e.ready = make(chan struct{})
	}
	return e.ready
}
func (e *Encoder) Segments() transcode.SegmentIndex { return taggedIndex{e.Index, e} }
func (e *Encoder) Close() error                     { return nil }

type taggedIndex struct {
	transcode.SegmentIndex
	e *Encoder
}

// A real index publishes a segment only after an encoded, tagged frame, so
// viewer reads (Window, Wait) wait, bounded, for the first one. Latest and Since
// stay immediate: the broadcast reads its origin and startup state through them
// before frames flow.
func (t taggedIndex) aired() {
	t.e.mu.Lock()
	ready := t.e.readyChan()
	t.e.mu.Unlock()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
	}
}
func (t taggedIndex) stamp(segments ...transcode.Segment) []transcode.Segment {
	t.e.mu.Lock()
	defer t.e.mu.Unlock()
	for i := range segments {
		segments[i].PolicyTags = t.e.tags
	}
	return segments
}
func (t taggedIndex) Latest() (transcode.Segment, bool) {
	seg, ok := t.SegmentIndex.Latest()
	return t.stamp(seg)[0], ok
}
func (t taggedIndex) Window(from, until time.Time) []transcode.Segment {
	t.aired()
	return t.stamp(t.SegmentIndex.Window(from, until)...)
}
func (t taggedIndex) Since(seq int64, max int) []transcode.Segment {
	return t.stamp(t.SegmentIndex.Since(seq, max)...)
}
func (t taggedIndex) Wait(ctx context.Context, after int64) (transcode.Segment, error) {
	t.aired()
	seg, err := t.SegmentIndex.Wait(ctx, after)
	return t.stamp(seg)[0], err
}

// Decoder yields black frames in Format, paced at about 100 fps, so the
// broadcast airs (and tags) programme output. A zero Format never yields.
type Decoder struct{ Format transcode.RawFormat }

func (d *Decoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	f := d.Format
	if f.Width == 0 || f.Height == 0 {
		<-ctx.Done()
		return transcode.RawFrame{}, ctx.Err()
	}
	select {
	case <-ctx.Done():
		return transcode.RawFrame{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	video := make([]byte, f.Width*f.Height*3/2)
	for i := range video {
		if i < f.Width*f.Height {
			video[i] = 16
		} else {
			video[i] = 128
		}
	}
	samples := 0
	if f.FPSNum > 0 {
		samples = f.SampleRate * f.FPSDen / f.FPSNum
	}
	return transcode.RawFrame{Video: video, PCM: make([]byte, samples*f.Channels*2)}, nil
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
