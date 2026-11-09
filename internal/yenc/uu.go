package yenc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// IsUUStartLine reports whether line starts a UUEncoded payload ("begin <mode> <name>"
// or a standard 61-character 'M' UUEncode data line).
func IsUUStartLine(line []byte) bool {
	_, ok := parseUUBegin(line)
	return ok || isRawUUMLine(line)
}

// DecodeUUFromFirstLine decodes a UUEncoded article body starting from firstLine.
func DecodeUUFromFirstLine(lr LineReader, firstLine []byte, opt *DecodeOptions) (*Part, error) {
	var o DecodeOptions
	if opt != nil {
		o = *opt
	}
	if o.MaxSize <= 0 {
		o.MaxSize = DefaultMaxSize
	}
	o.MaxSize = min(o.MaxSize, int64(int(^uint(0)>>1)))
	return decodeUU(lr, firstLine, o)
}

// parseUUBegin checks whether line is a UUEncode "begin <mode> <name>" header
// and returns the extracted file name.
func parseUUBegin(line []byte) (string, bool) {
	if !bytes.HasPrefix(line, []byte("begin ")) {
		return "", false
	}
	rest := line[len("begin "):]
	modeLen := 0
	for modeLen < len(rest) && modeLen < 4 && rest[modeLen] >= '0' && rest[modeLen] <= '7' {
		modeLen++
	}
	if modeLen < 3 || modeLen >= len(rest) || rest[modeLen] != ' ' {
		return "", false
	}
	name := string(bytes.TrimSpace(rest[modeLen+1:]))
	if name == "" {
		return "", false
	}
	return name, true
}

// isRawUUMLine reports whether line is a standard 45-byte UUEncode data line
// ('M' length byte followed by 60 valid UU characters in 0x20..0x60).
func isRawUUMLine(line []byte) bool {
	if len(line) != 61 || line[0] != 'M' {
		return false
	}
	for i := 1; i < len(line); i++ {
		if line[i] < 0x20 || line[i] > 0x60 {
			return false
		}
	}
	return true
}

func decodeUUVal(b byte) (byte, bool) {
	if b < 0x20 || b > 0x60 {
		return 0, false
	}
	return (b - 0x20) & 0x3f, true
}

func decodeUULine(dst []byte, line []byte) ([]byte, bool, error) {
	if len(line) == 0 {
		return dst, false, nil
	}
	if bytes.Equal(line, []byte("end")) {
		return dst, true, nil
	}
	nBytes, ok := decodeUUVal(line[0])
	if !ok || nBytes > 45 {
		return dst, false, fmt.Errorf("%w: invalid uuencode length byte %q", ErrMalformed, line[0])
	}
	if nBytes == 0 {
		for i := 1; i < len(line); i++ {
			if v, ok := decodeUUVal(line[i]); !ok || v != 0 {
				return dst, false, fmt.Errorf("%w: invalid uuencode zero line", ErrMalformed)
			}
		}
		return dst, false, nil
	}
	payload := line[1:]
	groups := (int(nBytes) + 2) / 3
	needChars := groups * 4
	// UUEncoders may strip trailing spaces (which encode 0) and/or append a 1-char checksum.
	if len(payload) > needChars+1 {
		return dst, false, fmt.Errorf("%w: uuencode line too long (%d chars for %d bytes)", ErrMalformed, len(payload), nBytes)
	}
	rem := int(nBytes)
	pos := 0
	for g := 0; g < groups; g++ {
		var c [4]byte
		for j := 0; j < 4; j++ {
			if pos < len(payload) {
				v, ok := decodeUUVal(payload[pos])
				if !ok {
					return dst, false, fmt.Errorf("%w: invalid uuencode character 0x%02x", ErrMalformed, payload[pos])
				}
				c[j] = v
				pos++
			}
		}
		b0 := (c[0] << 2) | (c[1] >> 4)
		b1 := (c[1] << 4) | (c[2] >> 2)
		b2 := (c[2] << 6) | c[3]
		switch {
		case rem >= 3:
			dst = append(dst, b0, b1, b2)
			rem -= 3
		case rem == 2:
			dst = append(dst, b0, b1)
			rem = 0
		case rem == 1:
			dst = append(dst, b0)
			rem = 0
		}
	}
	return dst, false, nil
}

// decodeUU decodes a UUEncoded article body starting from firstLine (either a
// "begin <mode> <name>" header or a raw 61-char 'M' data line).
func decodeUU(lr LineReader, firstLine []byte, o DecodeOptions) (*Part, error) {
	var name string
	var dst []byte
	if int64(cap(o.Buffer)) > 0 {
		dst = o.Buffer[:0:min(int64(cap(o.Buffer)), o.MaxSize)]
	} else {
		dst = make([]byte, 0, min(64<<10, o.MaxSize))
	}
	if n, ok := parseUUBegin(firstLine); ok {
		name = n
	} else {
		var err error
		dst, _, err = decodeUULine(dst, firstLine)
		if err != nil {
			return nil, err
		}
	}
	if int64(len(dst)) > o.MaxSize {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrTooLarge, o.MaxSize)
	}
	wireBudget := min(o.MaxSize, (int64(^uint64(0)>>1)-MaxPreambleSize)/2)*2 + MaxPreambleSize
	for {
		line, err := lr.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if len(line) > MaxLineSize {
			return nil, fmt.Errorf("%w: uuencode line exceeds %d bytes", ErrTooLarge, MaxLineSize)
		}
		wireBudget -= int64(len(line)) + 2
		if wireBudget < 0 {
			return nil, fmt.Errorf("%w: uuencode body exceeds budget", ErrTooLarge)
		}
		if len(line) == 0 {
			continue
		}
		var done bool
		dst, done, err = decodeUULine(dst, line)
		if err != nil {
			return nil, err
		}
		if int64(len(dst)) > o.MaxSize {
			return nil, fmt.Errorf("%w: more than %d bytes", ErrTooLarge, o.MaxSize)
		}
		if done {
			break
		}
	}
	if len(dst) == 0 && name == "" {
		return nil, ErrNoHeader
	}
	return &Part{
		Header:  Header{Name: name},
		Trailer: Trailer{Size: int64(len(dst))},
		Data:    dst,
	}, nil
}
