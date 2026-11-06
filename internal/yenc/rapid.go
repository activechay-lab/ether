//go:build !cgo && goexperiment.simd && (amd64 || arm64)

package yenc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"

	"github.com/mnightingale/rapidyenc"
)

var rapidSIMD = rapidyenc.DecodeKernel() != "generic"

func rapidEnabled() bool  { return rapidSIMD }
func rapidKernel() string { return rapidyenc.DecodeKernel() }

var errRapidFallback = errors.New("yenc: resume bounded SWAR decoding")

// A decoder is reusable only after it consumes its complete synthetic NNTP
// response. Error/overflow paths discard it, including its private read buffer.
// The input adapter never reads beyond Ether's =yend boundary.
type rapidState struct {
	reader  boundedRapidReader
	decoder *rapidyenc.Decoder
}

var rapidPool = sync.Pool{New: func() any {
	state := &rapidState{}
	state.decoder = rapidyenc.NewDecoder(&state.reader, rapidyenc.WithStatusLineAlreadyRead(), rapidyenc.WithDataFunc(func() []byte { return state.reader.output }))
	return state
}}

type prependLineReader struct {
	line    []byte
	pending bool
	reader  LineReader
}

func (r *prependLineReader) ReadLine() ([]byte, error) {
	if r.pending {
		r.pending = false
		return r.line, nil
	}
	return r.reader.ReadLine()
}

func decodeRapidBody(lr LineReader, o DecodeOptions, p *Part) (*Part, error) {
	budget := bodyWireBudget(o.MaxSize)
	fallback := func(line []byte) (*Part, error) {
		return decodeBodySWAR(&prependLineReader{line: line, pending: true, reader: lr}, o, p, nil, true, false, budget)
	}
	// Avoid even an adapter allocation when an exact caller buffer already
	// determines that the portable path is required.
	declared := p.Header.PartSize()
	if declared > 0 && int64(cap(o.Buffer)) >= declared && int64(cap(o.Buffer))-declared < 64 {
		return decodeBodySWAR(lr, o, p, nil, true, false, budget)
	}
	if o.MaxSize < 1024 {
		return decodeBodySWAR(lr, o, p, nil, true, false, budget)
	}
	first, err := lr.ReadLine()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return p, ErrTruncated
		}
		return nil, err
	}
	if len(first) > MaxLineSize {
		return nil, fmt.Errorf("%w: encoded line exceeds %d bytes", ErrTooLarge, MaxLineSize)
	}
	if int64(len(first))+2 > budget {
		return nil, fmt.Errorf("%w: encoded body exceeds budget", ErrTooLarge)
	}
	h := p.Header
	multipart := bytes.HasPrefix(first, []byte(keyPart))
	if multipart {
		if err := ParsePart(first, &h); err != nil {
			return nil, err
		}
	}
	hint := h.PartSize()
	// Upstream needs at least 1 KiB and 64 bytes of end-marker slack. An
	// exact-capacity caller buffer must remain reusable, so use SWAR for it.
	if hint < 1024 || hint > o.MaxSize || hint > int64(int(^uint(0)>>1))-64 || (int64(cap(o.Buffer)) >= hint && int64(cap(o.Buffer)) < hint+64) {
		return fallback(first)
	}
	p.Header = h
	state := rapidPool.Get().(*rapidState)
	r := &state.reader
	*r = boundedRapidReader{lr: lr, part: p, maxSize: o.MaxSize, hint: hint, budget: budget}
	if multipart {
		r.budget -= int64(len(first)) + 2
	} else {
		r.first = first
		r.hasFirst = true
	}
	if int64(cap(o.Buffer)) >= hint+64 {
		r.output = o.Buffer[: 0 : int(hint)+64]
	} else {
		r.output = make([]byte, 0, int(hint)+64)
	}
	// Normalize the upstream allocation hint to the validated part size. Keep
	// Ether's original file/multipart metadata and trailer for verification.
	r.pending = append(r.header[:0], "=ybegin part=1 total=1 size="...)
	r.pending = strconv.AppendInt(r.pending, hint, 10)
	r.pending = append(r.pending, " name=ether\r\n=ypart begin=1 end="...)
	r.pending = strconv.AppendInt(r.pending, hint, 10)
	r.pending = append(r.pending, "\r\n"...)
	response, decodeErr := state.decoder.Next()
	reusable := r.done && r.err == nil && len(r.pending) == 0 && len(r.suffix) == 0
	defer func() {
		*r = boundedRapidReader{}
		if reusable {
			rapidPool.Put(state)
		}
	}()
	if response != nil {
		p.Data = response.Data[:len(response.Data):min(cap(response.Data), int(o.MaxSize))]
	}
	if errors.Is(r.err, errRapidFallback) {
		if response == nil || int64(len(p.Data)) != r.decoded {
			return nil, fmt.Errorf("yenc: upstream partial output differs from bounded input")
		}
		return decodeBodySWAR(&prependLineReader{line: r.fallback, pending: true, reader: lr}, o, p, p.Data, false, r.escaped, r.budget)
	}
	if r.err != nil {
		if r.partial {
			return p, r.err
		}
		return nil, r.err
	}
	if response == nil {
		return nil, decodeErr
	}
	if !r.done || int64(len(p.Data)) != r.decoded {
		return p, fmt.Errorf("%w: incomplete upstream response", ErrTruncated)
	}
	// Upstream validates one expected CRC; Ether additionally checks both CRCs
	// when the article contains the entire file, and honors SkipCRC.
	if decodeErr != nil && !errors.Is(decodeErr, rapidyenc.ErrCrcMismatch) && !errors.Is(decodeErr, rapidyenc.ErrDataCorruption) {
		return p, decodeErr
	}
	return p, verifyChecksum(p, o.SkipCRC, response.Metadata.CRC)
}

type boundedRapidReader struct {
	lr                             LineReader
	part                           *Part
	maxSize, hint, budget, decoded int64
	first                          []byte
	hasFirst                       bool
	pending                        []byte
	suffix                         string
	dot                            bool
	output                         []byte
	header                         [160]byte
	escaped                        bool
	done                           bool
	err                            error
	partial                        bool
	fallback                       []byte
	scratch, normalized            []byte
}

func (r *boundedRapidReader) Read(dst []byte) (int, error) {
	written := 0
	for written < len(dst) {
		switch {
		case r.dot:
			dst[written] = '.'
			written++
			r.dot = false
		case len(r.pending) > 0:
			n := copy(dst[written:], r.pending)
			written += n
			r.pending = r.pending[n:]
		case len(r.suffix) > 0:
			n := copy(dst[written:], r.suffix)
			written += n
			r.suffix = r.suffix[n:]
		case r.err != nil:
			if written > 0 {
				return written, nil
			}
			return 0, r.err
		case r.done:
			if written > 0 {
				return written, nil
			}
			return 0, io.EOF
		default:
			r.nextLine()
		}
	}
	return written, nil
}

func decodedLineSize(line []byte, escaped bool) (int, bool) {
	n := 0
	if escaped && len(line) > 0 {
		n++
		line = line[1:]
		escaped = false
	}
	for len(line) > 0 {
		i := bytes.IndexByte(line, '=')
		if i < 0 {
			return n + len(line), escaped
		}
		n += i
		line = line[i+1:]
		if len(line) == 0 {
			return n, true
		}
		n++
		line = line[1:]
	}
	return n, escaped
}

func (r *boundedRapidReader) nextLine() {
	var line []byte
	var err error
	if r.hasFirst {
		line = r.first
		r.first = nil
		r.hasFirst = false
	} else {
		line, err = r.lr.ReadLine()
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			r.err = ErrTruncated
			r.partial = true
		} else {
			r.err = err
		}
		return
	}
	if len(line) > MaxLineSize {
		r.err = fmt.Errorf("%w: encoded line exceeds %d bytes", ErrTooLarge, MaxLineSize)
		return
	}
	wire := int64(len(line)) + 2
	if wire > r.budget {
		r.err = fmt.Errorf("%w: encoded body exceeds budget", ErrTooLarge)
		return
	}
	if bytes.HasPrefix(line, []byte(keyEnd)) {
		r.budget -= wire
		if r.escaped {
			r.err = fmt.Errorf("%w: dangling escape before =yend", ErrMalformed)
			r.partial = true
			return
		}
		trailer, err := ParseEnd(line)
		if err != nil {
			r.err = err
			r.partial = true
			return
		}
		r.part.Trailer = trailer
		// ParseEnd accepts whitespace inside a LineReader-provided trailer.
		// Re-serialize fields so embedded CRLF cannot terminate the upstream
		// response early and leave hidden bytes in its pooled read buffer.
		r.pending = append(r.header[:0], "=yend size="...)
		r.pending = strconv.AppendInt(r.pending, trailer.Size, 10)
		if trailer.HasPartCRC {
			r.pending = append(r.pending, " pcrc32="...)
			r.pending = appendHex32(r.pending, trailer.PartCRC32)
		} else if trailer.HasCRC {
			r.pending = append(r.pending, " crc32="...)
			r.pending = appendHex32(r.pending, trailer.CRC32)
		}
		r.suffix = "\r\n.\r\n"
		r.done = true
		return
	}
	n, escaped, normalize := rapidLineInfo(line, r.escaped)
	if int64(n) > r.maxSize-r.decoded {
		r.err = fmt.Errorf("%w: more than %d bytes", ErrTooLarge, r.maxSize)
		return
	}
	if int64(n) > r.hint-r.decoded {
		// Do not let an incorrect header trigger upstream's unbounded output
		// growth. Continue with the original bounded decoder at this exact line.
		r.err = errRapidFallback
		r.fallback = line
		return
	}
	r.budget -= wire
	r.decoded += int64(n)
	if normalize || bytes.HasPrefix(line, []byte("=y")) {
		// Ether permits escapes split across lines and literal CR/LF in the
		// LineReader API. Normalize only those uncommon lines for NNTP decoding.
		if cap(r.scratch) < len(line) {
			r.scratch = make([]byte, len(line))
		}
		count, _ := DecodeLine(r.scratch[:len(line)], line, r.escaped)
		r.normalized = r.normalized[:0]
		for _, d := range r.scratch[:count] {
			e := d + 42
			if e == 0 || e == '\r' || e == '\n' || e == '=' {
				r.normalized = append(r.normalized, '=', e+64)
			} else {
				r.normalized = append(r.normalized, e)
			}
		}
		line = r.normalized
	}
	r.escaped = escaped
	r.pending = line
	r.suffix = "\r\n"
	// LineReader already removed NNTP dot-stuffing. Put it back exactly once
	// because RapidYenc's public decoder consumes NNTP wire bytes.
	r.dot = len(line) > 0 && line[0] == '.'
}
