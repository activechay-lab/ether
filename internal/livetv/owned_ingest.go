package livetv

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

// hlsOrigin is where an ingest reads a live media playlist and its
// resources: a provider over HTTP, or a local ffmpeg relay session.
type hlsOrigin interface {
	media(ctx context.Context) (body []byte, base *url.URL, err error)
	get(ctx context.Context, uri string, length, offset int64) ([]byte, error)
	label() string
}

// segmentError is a failure to fetch one listed segment (or its key or init
// section). The playlist itself is fine; the ingest retries and then skips
// the segment rather than abandoning the source.
type segmentError struct{ err error }

func (e *segmentError) Error() string { return "livetv: segment fetch: " + e.err.Error() }
func (e *segmentError) Unwrap() error { return e.err }

var (
	errOriginEnded  = errors.New("livetv: live source ended")
	errMasterSource = errors.New("livetv: master playlist where a media playlist was expected")
)

// hlsIngest follows one origin's media playlist and feeds every new segment,
// with its init section and key, into an owned timeline. It owns the mapping
// from the origin's media sequence to "what has been ingested", so a reload,
// a reconnect or a renumbered playlist neither repeats nor skips media
// silently: anything lost is marked with a discontinuity.
type hlsIngest struct {
	tl     *ownedTimeline
	origin hlsOrigin
	log    *slog.Logger
	// reserve is how far behind the origin's live edge a fresh start
	// begins (the backlog primed into the timeline's reserve).
	reserve time.Duration
	// startSegs is how many segments the first playlist carries.
	startSegs int
	// live marks an origin that cuts segments in real time (a local
	// relay): its short first playlist is not a short backlog.
	live bool
	// accept may veto a segment (the relay drops keyframe-starved
	// overlong segments); a vetoed segment is not ingested.
	accept func(*ownedSegment) bool

	// seed is a playlist the opener already fetched; the next step uses it
	// instead of fetching again.
	seed     []byte
	seedBase *url.URL
	// prefetched holds segment bytes fetched concurrently for a fresh
	// start's first playlist, keyed by URI and range.
	prefetched map[string][]byte

	lastMSN  int64
	lastURI  string
	gapNext  bool // the next ingested segment follows lost media
	maps     map[string]string
	keys     map[string]keyEntry
	failMSN  int64
	failures int
	target   time.Duration // origin's target duration
	lastNew  time.Time     // when the origin last listed a new segment
	lastDur  time.Duration
	opened   time.Time
}

type keyEntry struct {
	res  string
	data []byte
}

func newHLSIngest(tl *ownedTimeline, origin hlsOrigin, log *slog.Logger, reserve time.Duration, startSegs int) *hlsIngest {
	return &hlsIngest{tl: tl, origin: origin, log: log, reserve: reserve, startSegs: startSegs, lastMSN: -1, failMSN: -1,
		maps: map[string]string{}, keys: map[string]keyEntry{}, opened: time.Now()}
}

// rebase points the ingest at a new origin (a re-resolved URL, a backup
// source, a new producer). The next segment starts a new span.
func (in *hlsIngest) rebase(origin hlsOrigin) {
	in.origin = origin
	in.lastMSN, in.lastURI = -1, ""
	in.gapNext = true
	in.failMSN, in.failures = -1, 0
	in.maps = map[string]string{}
	in.keys = map[string]keyEntry{}
	in.opened = time.Now()
	in.lastNew = time.Time{}
}

// stale is how long the origin has failed to list a new segment beyond the
// one it was expected to.
func (in *hlsIngest) stale(now time.Time) time.Duration {
	from := in.lastNew
	if from.IsZero() {
		from = in.opened
	}
	return now.Sub(from.Add(in.lastDur))
}

// startIndex picks where a fresh start begins: deep enough that the first
// playlist has startSegs segments and the reserve is primed from the
// backlog, but never the oldest listed segment when that can be avoided (it
// may expire before it is fetched).
func startIndex(segs []playlistSegment, reserve time.Duration, startSegs int) int {
	if len(segs) == 0 {
		return 0
	}
	i := len(segs)
	var acc time.Duration
	need := 0
	for i > 0 {
		if need >= startSegs && acc >= reserve {
			break
		}
		i--
		if need < startSegs {
			need++
		} else {
			acc += segs[i].dur
		}
	}
	if i == 0 && len(segs) > startSegs+1 {
		i = 1
	}
	return i
}

// step reloads the playlist once and ingests what is new. progress reports
// that at least one segment was ingested.
func (in *hlsIngest) step(ctx context.Context) (progress bool, err error) {
	body, base := in.seed, in.seedBase
	in.seed, in.seedBase = nil, nil
	if body == nil {
		if body, base, err = in.origin.media(ctx); err != nil {
			return false, err
		}
	}
	if isMasterPlaylist(body) {
		return false, errMasterSource
	}
	m, ok := parseMediaPlaylist(body, base)
	if !ok {
		return false, fmt.Errorf("livetv: invalid media playlist from %s", in.origin.label())
	}
	if m.target > 0 {
		in.target = m.target
	}
	segs := m.segments
	from := 0
	switch {
	case len(segs) == 0:
	case in.lastMSN < 0:
		from = startIndex(segs, in.reserve, in.startSegs)
		if in.live {
			// Everything a fresh local producer lists is new media (its
			// backlog primes the reserve).
			from = 0
			in.tl.expectStart(0, m.target)
		} else {
			in.tl.expectStart(min(in.startSegs, len(segs)-from), m.target)
		}
	default:
		from = -1
		for i, s := range segs {
			if s.msn == in.lastMSN && s.uri == in.lastURI {
				from = i + 1
				break
			}
		}
		if from < 0 {
			// Renumbered, or the sequence moved without us: find the last
			// ingested URI, else decide by sequence number.
			for i, s := range segs {
				if s.uri == in.lastURI {
					from = i + 1
					break
				}
			}
		}
		if from < 0 {
			switch {
			case segs[0].msn > in.lastMSN:
				// Lost media between reloads.
				from = 0
				in.gapNext = true
			case segs[len(segs)-1].msn >= in.lastMSN:
				// Same numbering, rotated URIs (signed segment URLs).
				from = int(in.lastMSN + 1 - segs[0].msn)
			default:
				// The origin restarted its numbering: a new stream.
				from = startIndex(segs, in.reserve, in.startSegs)
				in.gapNext = true
				in.log.Info("owned live source restarted its media sequence", "origin", in.origin.label())
			}
		}
	}
	if in.lastMSN < 0 && !in.live {
		in.prefetch(ctx, segs[from:min(len(segs), from+in.startSegs)])
	}
	defer func() { in.prefetched = nil }()
	for i := from; i < len(segs); i++ {
		s := segs[i]
		if s.gap {
			in.lastMSN, in.lastURI = s.msn, s.uri
			in.gapNext = true
			continue
		}
		if in.lastMSN >= 0 && s.msn > in.lastMSN+1 && !in.gapNext {
			in.gapNext = true
		}
		in.lastNew = time.Now()
		in.lastDur = s.dur
		in.tl.setInflight(true)
		seg, err := in.fetch(ctx, s, m.target)
		in.tl.setInflight(false)
		if err != nil {
			if ctx.Err() != nil {
				return progress, ctx.Err()
			}
			if in.failMSN == s.msn {
				in.failures++
			} else {
				in.failMSN, in.failures = s.msn, 1
			}
			if in.failures < 3 {
				return progress, &segmentError{err}
			}
			// A segment that keeps failing is skipped; the gap is marked.
			in.log.Warn("owned live segment skipped after repeated failures", "origin", in.origin.label(), "err", err)
			in.lastMSN, in.lastURI = s.msn, s.uri
			in.gapNext = true
			continue
		}
		if in.accept != nil && !in.accept(seg) {
			in.lastMSN, in.lastURI = s.msn, s.uri
			in.gapNext = true
			continue
		}
		seg.disc = seg.disc || in.gapNext || (s.disc && in.lastMSN >= 0)
		in.gapNext = false
		in.lastMSN, in.lastURI = s.msn, s.uri
		in.tl.ingest(seg)
		progress = true
	}
	if m.ended {
		return progress, errOriginEnded
	}
	return progress, nil
}

func (in *hlsIngest) fetch(ctx context.Context, s playlistSegment, target time.Duration) (*ownedSegment, error) {
	seg := &ownedSegment{dur: s.dur, origin: in.origin.label()}
	if seg.dur <= 0 {
		seg.dur = target
	}
	if s.mapURI != "" {
		mapKey := fmt.Sprintf("%s|%d|%d", s.mapURI, s.mapLen, s.mapOff)
		res, ok := in.maps[mapKey]
		if !ok {
			data, err := in.origin.get(ctx, s.mapURI, s.mapLen, s.mapOff)
			if err != nil {
				return nil, fmt.Errorf("init section: %w", err)
			}
			res = "om-" + stableID(string(data)) + ".mp4"
			in.tl.addResource(res, data, "video/mp4")
			in.maps[mapKey] = res
		}
		seg.mapRes = res
		if !in.tl.hasResource(res) {
			delete(in.maps, mapKey)
			return nil, fmt.Errorf("init section evicted")
		}
	}
	var key []byte
	if s.key.method != "" {
		k, ok := in.keys[s.key.uri]
		if !ok {
			data, err := in.origin.get(ctx, s.key.uri, 0, 0)
			if err != nil {
				return nil, fmt.Errorf("key: %w", err)
			}
			k = keyEntry{res: "ok-" + stableID(s.key.uri, string(data)), data: data}
			in.tl.addResource(k.res, data, "application/octet-stream")
			in.keys[s.key.uri] = k
		}
		if !in.tl.hasResource(k.res) {
			in.tl.addResource(k.res, k.data, "application/octet-stream")
		}
		iv := s.key.iv
		if iv == "" {
			iv = ivFromSequence(s.msn)
		}
		seg.key = ownedKey{method: s.key.method, res: k.res, iv: iv, format: s.key.format, versions: s.key.versions}
		if s.key.method == "AES-128" && (s.key.format == "" || strings.EqualFold(s.key.format, "identity")) {
			key = k.data
		}
	}
	data, ok := in.prefetched[segmentKey(s)]
	if !ok {
		var err error
		if data, err = in.origin.get(ctx, s.uri, s.rangeLen, s.rangeOff); err != nil {
			return nil, err
		}
	}
	seg.data = data
	plain := data
	if key != nil {
		plain = decryptPrefix(data, key, seg.key.iv)
	}
	if isTS(plain) {
		seg.ext = "ts"
		seg.format = inspectTS(plain)
	} else {
		seg.ext = "m4s"
		if s.mapURI == "" && !strings.HasSuffix(strings.ToLower(s.uri), ".m4s") && !strings.HasSuffix(strings.ToLower(s.uri), ".mp4") {
			seg.ext = "ts"
		}
	}
	return seg, nil
}

func segmentKey(s playlistSegment) string {
	return fmt.Sprintf("%s|%d|%d", s.uri, s.rangeLen, s.rangeOff)
}

// prefetch fetches the first playlist's segments concurrently: a cold tune
// waits for all of them, so their round trips must overlap. Failures are
// left to the ordinary sequential fetch and its retries.
func (in *hlsIngest) prefetch(ctx context.Context, segs []playlistSegment) {
	if len(segs) < 2 {
		return
	}
	type result struct {
		key  string
		data []byte
	}
	out := make(chan result, len(segs))
	for _, s := range segs {
		go func(s playlistSegment) {
			data, err := in.origin.get(ctx, s.uri, s.rangeLen, s.rangeOff)
			if err != nil {
				data = nil
			}
			out <- result{segmentKey(s), data}
		}(s)
	}
	in.prefetched = map[string][]byte{}
	for range segs {
		if r := <-out; r.data != nil {
			in.prefetched[r.key] = r.data
		}
	}
}

// decryptPrefix decrypts an AES-128 (CBC) segment for header inspection only;
// the client still receives the provider's encrypted bytes.
func decryptPrefix(data, key []byte, ivHex string) []byte {
	if len(key) != 16 {
		return nil
	}
	iv, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(ivHex, "0x"), "0X"))
	if err != nil || len(iv) > 16 {
		return nil
	}
	if len(iv) < 16 {
		padded := make([]byte, 16)
		copy(padded[16-len(iv):], iv)
		iv = padded
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil
	}
	n := len(data) / aes.BlockSize * aes.BlockSize
	out := make([]byte, n)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data[:n])
	return out
}

// ivFromSequence is the implicit IV of RFC 8216 section 5.2.
func ivFromSequence(msn int64) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[8:], uint64(msn))
	return "0x" + strings.ToUpper(hex.EncodeToString(b[:]))
}
