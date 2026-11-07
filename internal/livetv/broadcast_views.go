package livetv

import (
	"context"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// awaitBroadcastCushion holds a tune (never a playlist request) until its
// output has published a first segment and, for a live provider output, one
// whole target duration: a client then starts with a segment of cushion and
// its first playlist answers at once.
func awaitBroadcastCushion(ctx context.Context, lease *broadcastLease, provider bool) {
	index, err := lease.output()
	if err != nil {
		return
	}
	segment := broadcastSegment(lease)
	limit := 6 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		limit = min(limit, time.Until(deadline)-500*time.Millisecond)
	}
	if limit <= 0 {
		return
	}
	wait, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	latest, ok := index.Latest()
	if !ok {
		if latest, err = index.Wait(wait, -1); err != nil {
			return
		}
	}
	// A live output's client starts two segments behind its first picture.
	// AVPlayer reloads a changed playlist once per target duration, so its
	// buffer then never drops below about one segment, and a segment
	// published up to a second late costs nothing; no opening cut of the
	// ladder is longer than that lead (continuousLadder).
	first := index.Since(-1, 1)
	for provider && len(first) > 0 && latest.Start.Add(latest.Duration).Sub(first[0].Start) < segment*2 {
		if latest, err = index.Wait(wait, latest.Seq); err != nil {
			return
		}
	}
}

// broadcastSegment is the target duration of the output a lease reads.
func broadcastSegment(l *broadcastLease) time.Duration {
	if l == nil || l.broadcast == nil || l.broadcast.segment <= 0 {
		return segmentDuration
	}
	return l.broadcast.segment
}

func (s *Service) broadcastSegments(ctx context.Context, ss *session) (transcode.SegmentIndex, error) {
	ss.mu.Lock()
	lease := ss.broadcast
	ss.mu.Unlock()
	if lease == nil {
		return nil, core.ErrNotAvailable
	}
	return lease.output()
}
func (s *Service) broadcastWindow(ctx context.Context, ss *session, r *http.Request) (transcode.SegmentIndex, []transcode.Segment, error) {
	index, err := s.broadcastSegments(ctx, ss)
	if err != nil {
		return nil, nil, err
	}
	// AVPlayer never starts an item whose first playlist response took a
	// whole target duration, and fails it on a 503: never hold a playlist
	// for more than half a target duration. The tune already waited for the
	// output's first segment (awaitBroadcastCushion).
	ss.mu.Lock()
	provider := ss.info.Channel.Kind == "iptv"
	segment := broadcastSegment(ss.broadcast)
	ss.mu.Unlock()
	wait, cancel := context.WithTimeout(ctx, segment/2)
	defer cancel()
	latest, ok := index.Latest()
	if !ok {
		latest, err = index.Wait(wait, -1)
		if err != nil {
			// No playlist exists yet, including when a cold encoder exits before
			// publishing. Tell clients to retry rather than report a bad gateway.
			return nil, nil, core.ErrNotAvailable
		}
	}
	if provider {
		if first := index.Since(-1, 1); len(first) > 0 {
			for latest.Start.Add(latest.Duration).Sub(first[0].Start) < segment {
				next, err := index.Wait(wait, latest.Seq)
				if err != nil {
					break
				}
				latest = next
			}
		}
	}
	end := latest.Start.Add(latest.Duration)
	from := end.Add(-24 * time.Second)
	if raw := r.URL.Query().Get("offset"); raw != "" {
		seconds, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds > broadcastDVR.Seconds() {
			return nil, nil, fmt.Errorf("invalid DVR offset")
		}
		end = end.Add(-time.Duration(seconds * float64(time.Second)))
		from = end.Add(-24 * time.Second)
	}
	ss.mu.Lock()
	user := ss.user
	ss.mu.Unlock()
	ctx, err = s.broadcastPolicyContext(ctx, ss, &user)
	if err != nil {
		return nil, nil, err
	}
	segments := index.Window(from, end)
	if len(segments) == 0 {
		return nil, nil, core.ErrNotAvailable
	}

	for _, seg := range segments {
		if !s.broadcastSegmentAllowed(ctx, ss, seg) {
			return nil, nil, core.ErrUnauthorized
		}
	}
	return index, segments, nil
}
func (s *Service) serveBroadcastPlaylist(w http.ResponseWriter, r *http.Request, ss *session) {
	_, segments, err := s.broadcastWindow(r.Context(), ss, r)
	if err != nil {
		if errors.Is(err, core.ErrNotAvailable) {
			w.Header().Set("Retry-After", "1")
		}
		writeStreamError(w, err)
		return
	}
	// TARGETDURATION is a property of the rendition, including its short
	// startup segments, and must not change as the window advances.
	ss.mu.Lock()
	target := int(math.Ceil(broadcastSegment(ss.broadcast).Seconds()))
	ss.mu.Unlock()
	for _, seg := range segments {
		target = max(target, int(math.Ceil(seg.Duration.Seconds())))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:%d\n", target, segments[0].Seq)
	for _, seg := range segments {
		uri := fmt.Sprintf("relay/%d.ts", seg.Seq)
		if !strings.Contains(r.URL.Path, "/livetv/sessions/") {
			uri = fmt.Sprintf("/livetv/sessions/%s/0/relay/%d.ts", ss.info.ID, seg.Seq)
		}
		uri = withAuth(uri, ss.streamGrant)
		fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:%.6f,\n%s\n", seg.Start.UTC().Format(time.RFC3339Nano), seg.Duration.Seconds(), uri)
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		fmt.Fprint(w, b.String())
	}
}
func (s *Service) serveBroadcastTS(w http.ResponseWriter, r *http.Request, ss *session, seq int64) {
	index, err := s.broadcastSegments(r.Context(), ss)
	if err != nil {
		writeStreamError(w, err)
		return
	}
	segments := index.Since(seq-1, 1)
	if len(segments) == 0 || segments[0].Seq != seq {
		writeStreamError(w, core.ErrNotFound)
		return
	}
	if !s.broadcastSegmentAllowed(r.Context(), ss, segments[0]) {
		writeStreamError(w, core.ErrUnauthorized)
		return
	}
	s.touchMedia(ss)
	observed := s.observeMedia(w, ss)
	defer observed.done()
	index.ServeTS(observed, r, seq)
}
func (s *Service) serveBroadcastDASH(w http.ResponseWriter, r *http.Request, ss *session) error {
	index, segments, err := s.broadcastWindow(r.Context(), ss, r)
	if err != nil {
		return err
	}
	codecs, width, height, bandwidth := index.Format()
	first := segments[0]
	ss.mu.Lock()
	lease := ss.broadcast
	ss.mu.Unlock()
	if lease == nil {
		return core.ErrNotAvailable
	}
	origin, err := lease.timelineOrigin(r.Context())
	if err != nil {
		return err
	}
	init := withAuth(fmt.Sprintf("/livetv/sessions/%s/0/dash/0/header", ss.info.ID), ss.streamGrant)
	media := withAuth(fmt.Sprintf("/livetv/sessions/%s/0/dash/0/$Number$.m4s", ss.info.ID), ss.streamGrant)
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="utf-8"?><MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="dynamic" profiles="urn:mpeg:dash:profile:isoff-live:2011" availabilityStartTime="%s" publishTime="%s" minimumUpdatePeriod="PT2S" minBufferTime="PT2S" timeShiftBufferDepth="PT1800S"><Period id="0" start="PT0S"><AdaptationSet mimeType="video/mp4" segmentAlignment="true"><Representation id="0" codecs="%s" width="%d" height="%d" bandwidth="%d"><SegmentTemplate timescale="1000000000" startNumber="%d" initialization="%s" media="%s"><SegmentTimeline>`, origin.UTC().Format(time.RFC3339Nano), s.o.Now().UTC().Format(time.RFC3339Nano), html.EscapeString(codecs), width, height, bandwidth, first.Seq, html.EscapeString(init), html.EscapeString(media))
	for _, seg := range segments {
		fmt.Fprintf(&b, `<S t="%d" d="%d"/>`, seg.Start.Sub(origin).Nanoseconds(), seg.Duration.Nanoseconds())
	}
	b.WriteString(`</SegmentTimeline></SegmentTemplate></Representation></AdaptationSet></Period></MPD>`)
	w.Header().Set("Content-Type", "application/dash+xml")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		fmt.Fprint(w, b.String())
	}
	return nil
}
func (s *Service) serveBroadcastDASHPart(w http.ResponseWriter, r *http.Request, ss *session, rep int, seq int64, init bool) {
	if rep != 0 {
		writeStreamError(w, core.ErrNotFound)
		return
	}
	index, err := s.broadcastSegments(r.Context(), ss)
	if err != nil {
		writeStreamError(w, err)
		return
	}
	if init {
		index.ServeInit(w, r)
		return
	}
	segments := index.Since(seq-1, 1)
	if len(segments) == 0 || segments[0].Seq != seq {
		writeStreamError(w, core.ErrNotFound)
		return
	}
	if !s.broadcastSegmentAllowed(r.Context(), ss, segments[0]) {
		writeStreamError(w, core.ErrUnauthorized)
		return
	}
	s.touchMedia(ss)
	observed := s.observeMedia(w, ss)
	defer observed.done()
	index.ServeFMP4(observed, r, seq)
}
