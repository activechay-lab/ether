package yenc

import (
	"bufio"
	"bytes"

	"fmt"

	"io"
)

// Decode decodes a whole encoded article body held in memory.
func Decode(encoded []byte) (*Part, error) {
	return DecodeLines(&bytesLines{b: encoded}, nil)
}

// DecodeReader decodes one part from r.
func DecodeReader(r io.Reader, opt *DecodeOptions) (*Part, error) {
	return DecodeLines(NewLineReader(r), opt)
}

// bytesLines is a LineReader over an in-memory buffer.
type bytesLines struct{ b []byte }

func (l *bytesLines) ReadLine() ([]byte, error) {
	if len(l.b) == 0 {
		return nil, io.EOF
	}
	i := bytes.IndexByte(l.b, '\n')
	var line []byte
	if i < 0 {
		line, l.b = l.b, nil
	} else {
		line, l.b = l.b[:i], l.b[i+1:]
	}
	return trimCR(line), nil
}

func trimCR(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\r' {
		return b[:n-1]
	}
	return b
}

// NewLineReader adapts an io.Reader to a LineReader. Lines longer than the
// internal buffer are assembled in a side buffer up to MaxLineSize bytes.
// An oversized line returns ErrTooLarge; the reader must then be discarded.
func NewLineReader(r io.Reader) LineReader {
	if br, ok := r.(*bufio.Reader); ok {
		return &readerLines{r: br}
	}
	return &readerLines{r: bufio.NewReaderSize(r, 32<<10)}
}

type readerLines struct {
	r    *bufio.Reader
	long []byte
}

func (l *readerLines) ReadLine() ([]byte, error) {
	line, err := l.r.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		if len(line) > MaxLineSize {
			return nil, fmt.Errorf("%w: encoded line exceeds %d bytes", ErrTooLarge, MaxLineSize)
		}
		l.long = append(l.long[:0], line...)
		for err == bufio.ErrBufferFull {
			line, err = l.r.ReadSlice('\n')
			if len(line) > MaxLineSize+2-len(l.long) {
				return nil, fmt.Errorf("%w: encoded line exceeds %d bytes", ErrTooLarge, MaxLineSize)
			}
			l.long = append(l.long, line...)
		}
		line = l.long
	}
	if len(trimCR(bytes.TrimSuffix(line, []byte{'\n'}))) > MaxLineSize {
		return nil, fmt.Errorf("%w: encoded line exceeds %d bytes", ErrTooLarge, MaxLineSize)
	}
	if err != nil {
		if err == io.EOF && len(line) > 0 {
			return trimCR(line), nil
		}
		return nil, err
	}
	return trimCR(line[:len(line)-1]), nil
}
