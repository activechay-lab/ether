package livetv

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"
)

// slateTS is one TS packet carrying a PES with a PTS, enough for shiftTS.
func slateTS(pts uint64) []byte {
	p := make([]byte, tsPacket)
	p[0], p[1], p[2], p[3] = 0x47, 0x41, 0x00, 0x10 // PUSI, PID 0x100, payload only
	pes := []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0x80, 5, 0x21, 0, 1, 0, 1}
	shiftPESTime(pes[9:14], int64(pts)-1)
	copy(p[4:], pes)
	for i := 4 + len(pes); i < tsPacket; i++ {
		p[i] = 0xff
	}
	return p
}

func ptsOf(p []byte) uint64 {
	t := p[4+9:]
	return uint64(t[0]>>1&7)<<30 | uint64(t[1])<<22 | uint64(t[2]>>1)<<15 | uint64(t[3])<<7 | uint64(t[4]>>1)
}

type renderedSeg struct {
	seq     int64
	discSeq int64 // discontinuity sequence number the segment belongs to
	dur     float64
	uri     string
}

// parseOwned reads what a client derives from one playlist: each segment's
// media sequence number and discontinuity sequence number.
func parseOwned(t *testing.T, body []byte) (target int, segs []renderedSeg) {
	t.Helper()
	var seq, disc int64
	var dur float64
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			target, _ = strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-TARGETDURATION:"))
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			seq, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			disc, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"), 10, 64)
		case line == "#EXT-X-DISCONTINUITY":
			disc++
		case strings.HasPrefix(line, "#EXTINF:"):
			v, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			dur, _ = strconv.ParseFloat(v, 64)
		case line != "" && !strings.HasPrefix(line, "#"):
			segs = append(segs, renderedSeg{seq: seq, discSeq: disc, dur: dur, uri: line})
			seq++
		}
	}
	return target, segs
}

// The owned timeline's numbering is what lets Ether splice recoveries into
// one stream. Across a long session with slate runs, codec splices and a
// sliding window: media sequence numbers only grow, every segment keeps its
// discontinuity sequence number in every later playlist (RFC 8216 4.3.3.3),
// the target never changes, slate fills every deadline breach, and
// consecutive slate segments play as one clip (continuous timestamps).
func TestOwnedTimelineSplicesKeepNumberingConsistent(t *testing.T) {
	clock := time.Unix(1_000_000, 0)
	slate := &slateClip{ext: "ts", dur: 2 * time.Second, data: slateTS(900000)}
	tl := newOwnedTimeline("t", slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return clock }, 0,
		func(slateProfile) *slateClip { return slate })
	tl.expectStart(3, 4*time.Second)
	aac := mediaFormat{known: true, video: "h264", audio: []audioFormat{{pid: 257, codec: "aac", rate: 48000, channels: 2}}}
	ac3 := mediaFormat{known: true, video: "h264", audio: []audioFormat{{pid: 257, codec: "ac3", rate: 48000, channels: 6}}}
	n := 0
	media := func(f mediaFormat) {
		n++
		tl.ingest(&ownedSegment{dur: 4 * time.Second, ext: "ts", data: []byte{byte(n)}, format: f})
	}
	advance := func(d time.Duration) {
		for end := clock.Add(d); clock.Before(end); {
			clock = clock.Add(100 * time.Millisecond)
			tl.mu.Lock()
			tl.tickLocked(clock)
			tl.mu.Unlock()
		}
	}
	seen := map[int64]renderedSeg{}
	var lastFirst int64 = -1
	check := func() []renderedSeg {
		t.Helper()
		target, segs := parseOwned(t, tl.render(func(seq int64, ext string) string { return strconv.FormatInt(seq, 10) + "." + ext }, func(n string) string { return n }))
		if target != 4 {
			t.Fatalf("target duration changed to %d", target)
		}
		if len(segs) == 0 || segs[0].seq < lastFirst {
			t.Fatalf("media sequence went backwards: %+v", segs)
		}
		lastFirst = segs[0].seq
		for _, s := range segs {
			if prev, ok := seen[s.seq]; ok && prev.discSeq != s.discSeq {
				t.Fatalf("segment %d moved from discontinuity %d to %d", s.seq, prev.discSeq, s.discSeq)
			}
			seen[s.seq] = s
		}
		return segs
	}

	for i := 0; i < 3; i++ {
		media(aac)
	}
	if segs := check(); len(segs) != 3 {
		t.Fatalf("first playlist has %d segments", len(segs))
	}
	// The source stalls: nothing until the deadline (target plus half a
	// second), then slate keeps the playlist moving at real time.
	advance(4*time.Second + 400*time.Millisecond)
	if tl.slateSegments != 0 {
		t.Fatal("slate before the deadline")
	}
	advance(200 * time.Millisecond)
	if tl.slateSegments != 1 {
		t.Fatalf("no slate at the deadline: %d", tl.slateSegments)
	}
	advance(6 * time.Second)
	segs := check()
	var slates []*ownedSegment
	for _, s := range tl.window {
		if s.slate {
			slates = append(slates, s)
		}
	}
	if len(slates) < 3 || !slates[0].disc || slates[1].disc {
		t.Fatalf("slate run not spliced as one clip: %d slates", len(slates))
	}
	if d := ptsOf(slates[1].data) - ptsOf(slates[0].data); d != 180000 {
		t.Fatalf("consecutive slate timestamps differ by %d, want 2s", d)
	}
	// The source returns with an unannounced AAC -> AC3 change: both the
	// rejoin and the codec change are marked.
	media(aac)
	advance(4 * time.Second)
	media(ac3)
	advance(100 * time.Millisecond)
	if tl.codecSplices != 1 {
		t.Fatalf("codec change not marked: %d", tl.codecSplices)
	}
	segs = check()
	last := segs[len(segs)-1]
	if prev := segs[len(segs)-2]; last.discSeq != prev.discSeq+1 {
		t.Fatalf("codec change shares a discontinuity span: %+v %+v", prev, last)
	}
	// A long session slides the window; numbering stays consistent.
	for i := 0; i < 40; i++ {
		media(ac3)
		advance(4 * time.Second)
		check()
	}
	segs = check()
	if len(segs) > 8 || len(segs) < ownedMinWindow {
		t.Fatalf("window holds %d segments", len(segs))
	}
	if segs[0].discSeq != tl.discSeq || tl.discSeq < 3 {
		t.Fatalf("discontinuity sequence %d does not count what slid out", tl.discSeq)
	}
}

// With a reserve, the backlog primes the timeline and publication paces at
// real time: a provider stall shorter than the reserve never reaches slate.
func TestOwnedTimelineReserveAbsorbsAStall(t *testing.T) {
	clock := time.Unix(1_000_000, 0)
	slate := &slateClip{ext: "ts", dur: 2 * time.Second, data: slateTS(0)}
	tl := newOwnedTimeline("t", slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return clock }, 12*time.Second,
		func(slateProfile) *slateClip { return slate })
	tl.expectStart(3, 4*time.Second)
	for i := 0; i < 6; i++ { // three to start, twelve seconds of reserve
		tl.ingest(&ownedSegment{dur: 4 * time.Second, ext: "ts"})
	}
	if len(tl.window) != 3 || tl.depth() != 12*time.Second {
		t.Fatalf("start: window %d, reserve %v", len(tl.window), tl.depth())
	}
	for i := 0; i < 110; i++ { // eleven seconds without the provider
		clock = clock.Add(100 * time.Millisecond)
		tl.mu.Lock()
		tl.tickLocked(clock)
		tl.mu.Unlock()
	}
	if tl.slateSegments != 0 || len(tl.window) != 5 || tl.depth() != 4*time.Second {
		t.Fatalf("reserve did not pace the stall: slate %d window %d depth %v", tl.slateSegments, len(tl.window), tl.depth())
	}
}
