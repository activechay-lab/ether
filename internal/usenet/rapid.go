//go:build (!cgo && goexperiment.simd && (amd64 || arm64)) || (cgo && !android && !ios && (((darwin || linux) && (amd64 || arm64)) || (windows && amd64)))

package usenet

import (
	"bytes"
	"errors"
	"strconv"
	"sync"

	"github.com/braedonsaunders/ether/internal/yenc"
	"github.com/mnightingale/rapidyenc"
)

// The public decoder owns framing, payload decoding and CRC. This guard only
// checks Ether's limits and records the metadata not exposed by upstream.
// Existing caller buffers retain the portable decoder's exact reuse contract.
func decodeBody(br *BodyReader, opt *yenc.DecodeOptions) (*yenc.Part, error) {
	if rapidyenc.DecodeKernel() == "generic" || (opt != nil && cap(opt.Buffer) > 0) {
		return yenc.DecodeLines(br, opt)
	}
	maxSize := int64(yenc.DefaultMaxSize)
	if opt != nil && opt.MaxSize > 0 {
		maxSize = opt.MaxSize
	}
	maxSize = min(maxSize, int64(int(^uint(0)>>1))-1024)
	state := wireDecoderPool.Get().(*wireDecoder)
	state.input = guardedBody{body: br, maxSize: maxSize}
	if maxSize <= (int64(^uint64(0)>>1)-yenc.MaxPreambleSize)/6 {
		state.input.budget = maxSize*6 + yenc.MaxPreambleSize
	} else {
		state.input.budget = int64(^uint64(0) >> 1)
	}
	response, err := state.decoder.Next()
	g := &state.input
	p := &g.part
	if response != nil {
		p.Data = response.Data
	}
	// Copy the result before clearing pooled state. Never keep media buffers in
	// the pool, and discard upstream's private read buffer on incomplete input.
	result := *p
	guardErr := g.err
	complete := g.terminated && guardErr == nil && len(g.pending) == 0 && len(g.suffix) == 0 &&
		(err == nil || errors.Is(err, rapidyenc.ErrCrcMismatch) || errors.Is(err, rapidyenc.ErrDataCorruption))
	began, ended, decoded := g.began, g.ended, g.decoded
	state.input = guardedBody{}
	if complete {
		wireDecoderPool.Put(state)
	}
	if guardErr != nil {
		return &result, guardErr
	}
	if !began {
		return nil, yenc.ErrNoHeader
	}
	if !ended {
		return &result, yenc.ErrTruncated
	}
	if err != nil && !errors.Is(err, rapidyenc.ErrCrcMismatch) && !errors.Is(err, rapidyenc.ErrDataCorruption) {
		return &result, err
	}
	if response == nil {
		return &result, yenc.ErrTruncated
	}
	if int64(len(result.Data)) != decoded {
		return &result, yenc.ErrSizeMismatch
	}
	return &result, yenc.VerifyChecksum(&result, opt != nil && opt.SkipCRC, response.Metadata.CRC)
}

type wireDecoder struct {
	input   guardedBody
	decoder *rapidyenc.Decoder
}

var wireDecoderPool = sync.Pool{New: func() any {
	s := &wireDecoder{}
	s.decoder = rapidyenc.NewDecoder(&s.input, rapidyenc.WithStatusLineAlreadyRead(), rapidyenc.WithDataFunc(func() []byte {
		// The guard validated this hint before forwarding its first data line.
		return make([]byte, 0, max(1024, min(s.input.maxSize, s.input.part.Header.PartSize())+64))
	}))
	return s
}}

type guardedBody struct {
	body                         *BodyReader
	part                         yenc.Part
	maxSize, budget, decoded     int64
	preamble                     int
	began, first, ended, escaped bool
	terminated                   bool
	pending                      []byte
	suffix                       string
	err                          error
}

func (g *guardedBody) Read(dst []byte) (int, error) {
	n := 0
	for n < len(dst) {
		if len(g.pending) > 0 {
			copied := copy(dst[n:], g.pending)
			n += copied
			g.pending = g.pending[copied:]
			continue
		}
		if len(g.suffix) > 0 {
			copied := copy(dst[n:], g.suffix)
			n += copied
			g.suffix = g.suffix[copied:]
			continue
		}
		if g.err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, g.err
		}
		line, err := g.body.readBodyLine(true)
		if err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		if g.body.done {
			g.terminated = true
			g.queueLine(line)
			continue
		}
		content := bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		// Dot unstuff only the view used for validation. Forward original bytes.
		if len(content) > 0 && content[0] == '.' {
			content = content[1:]
		}
		if g.ended {
			continue
		} // Drain signatures without feeding them to upstream.
		if len(content) > yenc.MaxLineSize {
			g.err = yenc.ErrTooLarge
			continue
		}
		if !g.began {
			if !bytes.HasPrefix(content, []byte("=ybegin ")) {
				g.preamble += len(content) + 2
				if g.preamble > yenc.MaxPreambleSize {
					g.err = yenc.ErrTooLarge
				}
				continue // Do not let upstream detect UU in a yEnc preamble.
			}
			g.part.Header, g.err = yenc.ParseBegin(content)
			if g.err != nil {
				continue
			}
			if !wireIntMatches(content, "size", g.part.Header.Size, false) || !wireIntMatches(content, "part", int64(g.part.Header.Part), true) {
				g.err = yenc.ErrMalformed
				continue
			}
			// A nonmultipart header can trigger upstream allocation even if the
			// next line is the terminator. Reject its hint before forwarding it.
			if !bytes.Contains(content, []byte(" part=")) && g.part.Header.PartSize() > g.maxSize {
				g.err = yenc.ErrTooLarge
				continue
			}
			g.began, g.first = true, true
		} else {
			g.budget -= int64(len(content)) + 2
			if g.budget < 0 {
				g.err = yenc.ErrTooLarge
				continue
			}
			switch {
			case g.first && bytes.HasPrefix(content, []byte("=ypart ")):
				g.err = yenc.ParsePart(content, &g.part.Header)
				if g.err == nil && (!wireIntMatches(content, "begin", g.part.Header.Begin, false) || !wireIntMatches(content, "end", g.part.Header.End, false)) {
					g.err = yenc.ErrMalformed
				}
				g.first = false
			case bytes.HasPrefix(content, []byte("=yend")):
				g.first = false
				if g.escaped {
					g.err = yenc.ErrMalformed
					continue
				}
				g.part.Trailer, g.err = yenc.ParseEnd(content)
				g.ended = true
			default:
				if bytes.HasPrefix(content, []byte("=y")) {
					g.err = yenc.ErrMalformed
					continue
				}
				g.first = false
				// Count escape pairs without decoding or copying the payload. Refuse
				// excess data before upstream can grow beyond the validated size hint.
				data := content
				if g.escaped && len(data) > 0 {
					g.decoded++
					data = data[1:]
					g.escaped = false
				}
				for len(data) > 0 {
					i := bytes.IndexByte(data, '=')
					if i < 0 {
						g.decoded += int64(len(data))
						break
					}
					g.decoded += int64(i)
					data = data[i+1:]
					if len(data) == 0 {
						g.escaped = true
						break
					}
					g.decoded++
					data = data[1:]
				}

				if g.escaped {
					g.err = yenc.ErrMalformed
				} else if g.decoded > g.maxSize {
					g.err = yenc.ErrTooLarge
				} else if g.decoded > g.part.Header.PartSize() {
					g.err = yenc.ErrSizeMismatch
				}
			}
		}
		if !g.first && g.part.Header.PartSize() > g.maxSize {
			g.err = yenc.ErrTooLarge
		}
		if g.err == nil {
			g.queueLine(line)
		}
	}
	return n, nil
}

// NNTP uses CRLF; retain BodyReader's tolerance for LF-only servers without
// changing headers, dot-stuffing, payload bytes or the actual terminator.
func (g *guardedBody) queueLine(line []byte) {
	g.pending = line
	if !bytes.HasSuffix(line, []byte("\r\n")) {
		g.pending = bytes.TrimSuffix(line, []byte("\n"))
		g.suffix = "\r\n"
	}
}

// Upstream uses the first literal lowercase field; Ether's metadata parser
// accepts case variants and uses the last duplicate. Reject disagreements
// before forwarding allocation hints, instead of rewriting the header.
func wireIntMatches(line []byte, key string, want int64, optional bool) bool {
	_, value, found := bytes.Cut(line, []byte(" "+key+"="))
	if !found {
		return optional && want == 0
	}
	if end := bytes.IndexAny(value, "\x00 \r\n"); end >= 0 {
		value = value[:end]
	}
	got, err := strconv.ParseInt(string(value), 10, 64)
	return err == nil && got == want
}
