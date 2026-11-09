package transcode

import (
	"encoding/binary"
	"net/http"
	"os"
	"time"
)

// AVPlayer, playing a multivariant playlist, cancels a media segment
// request that receives no bytes for about one target duration (-12889 "No
// response for media file"), and retries it from the start. Headers alone
// do not count, and neither do a few bytes followed by silence: bytes must
// keep arriving. A heavy encode (HDR tone mapping, a loaded host) can take
// longer than a short opening segment lasts, so every attempt is cancelled
// and the first frame waits for several.
//
// A GET that has waited segmentPadAfter for its segment commits the
// response (200, no Content-Length, no ranges) and sends container padding
// every segmentPadEvery until the segment is ready, then the segment's own
// bytes: MPEG-TS null packets (PID 0x1FFF), which demuxers discard, or
// top-level ISO BMFF 'free' boxes before an fMP4 fragment. A response that
// would have been an error after that point aborts the connection, so the
// player sees a failed request (and retries) rather than a short segment:
// a committed response ends cleanly only once it has carried the segment's
// every byte. Padding stops at segmentPadMax (the request aborts), well
// inside the segment timeout.
const (
	segmentPadAfter = time.Second
	segmentPadEvery = 400 * time.Millisecond
	segmentPadMax   = 20 * time.Second
)

var (
	tsNullPacket = func() []byte {
		p := make([]byte, 188)
		p[0], p[1], p[2], p[3] = 0x47, 0x1f, 0xff, 0x10 // PID 0x1FFF, payload only
		for i := 4; i < len(p); i++ {
			p[i] = 0xff
		}
		return p
	}()
	fmp4FreeBox = func() []byte {
		b := make([]byte, 16)
		binary.BigEndian.PutUint32(b, uint32(len(b)))
		copy(b[4:], "free")
		return b
	}()
)

// segmentPadding is the padding unit for an HLS segment container, nil
// when it has none.
func segmentPadding(container string) []byte {
	switch container {
	case ContainerMPEGTS:
		return tsNullPacket
	case ContainerFMP4:
		return fmp4FreeBox
	}
	return nil
}

// paddedSegmentWriter is the response of one segment request that may be
// committed early with padding (pad). Until then it is transparent. After
// it, a success status is already on the wire, header changes go nowhere,
// and any other status marks the response failed: its body is dropped and
// the handler aborts the connection.
type paddedSegmentWriter struct {
	http.ResponseWriter
	unit      []byte
	committed bool
	failed    bool
	complete  bool  // the segment's bytes were all written
	media     int64 // body bytes written after the padding
	spare     http.Header
}

func (p *paddedSegmentWriter) Header() http.Header {
	if p.committed {
		if p.spare == nil {
			p.spare = http.Header{}
		}
		return p.spare
	}
	return p.ResponseWriter.Header()
}

func (p *paddedSegmentWriter) WriteHeader(code int) {
	if !p.committed {
		p.ResponseWriter.WriteHeader(code)
		return
	}
	if code != http.StatusOK {
		p.failed = true
	}
}

func (p *paddedSegmentWriter) Write(b []byte) (int, error) {
	if p.failed {
		return len(b), nil
	}
	n, err := p.ResponseWriter.Write(b)
	p.media += int64(n)
	return n, err
}

// delivered records that the segment files in paths were served: a
// committed response is complete only if all their bytes went out.
func (p *paddedSegmentWriter) delivered(paths []string) {
	if p == nil || !p.committed || p.failed {
		return
	}
	var size int64
	for _, path := range paths {
		st, err := os.Stat(path)
		if err != nil {
			return
		}
		size += st.Size()
	}
	p.complete = size > 0 && p.media == size
}

func (p *paddedSegmentWriter) Unwrap() http.ResponseWriter { return p.ResponseWriter }

// pad commits the response (the first time) and sends one padding unit.
func (p *paddedSegmentWriter) pad(s *Session) {
	if p.failed {
		return
	}
	if !p.committed {
		s.setSegmentHeaders(p.ResponseWriter)
		h := p.ResponseWriter.Header()
		h.Del("Content-Length")
		h.Set("Accept-Ranges", "none")
		p.ResponseWriter.WriteHeader(http.StatusOK)
		p.committed = true
		p.media = 0
	}
	if _, err := p.ResponseWriter.Write(p.unit); err != nil {
		p.failed = true
		return
	}
	_ = http.NewResponseController(p.ResponseWriter).Flush()
}

// finish aborts a committed response that did not carry its whole
// segment: without the chunked terminator the player sees a failed request,
// never a valid-looking empty or short segment.
func (p *paddedSegmentWriter) finish() {
	if p != nil && p.committed && !p.complete {
		panic(http.ErrAbortHandler)
	}
}
