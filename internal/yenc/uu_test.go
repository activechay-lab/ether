package yenc

import (
	"bytes"
	"strings"
	"testing"
)

func encodeUU(data []byte, name string, mode string, trimSpaces bool) []byte {
	var buf bytes.Buffer
	if name != "" {
		buf.WriteString("begin " + mode + " " + name + "\r\n")
	}
	encChar := func(v byte) byte {
		v &= 0x3f
		if v == 0 {
			return '`'
		}
		return v + 0x20
	}
	for len(data) > 0 {
		chunk := data
		if len(chunk) > 45 {
			chunk = chunk[:45]
		}
		data = data[len(chunk):]
		var line bytes.Buffer
		line.WriteByte(encChar(byte(len(chunk))))
		for i := 0; i < len(chunk); i += 3 {
			var b0, b1, b2 byte
			b0 = chunk[i]
			if i+1 < len(chunk) {
				b1 = chunk[i+1]
			}
			if i+2 < len(chunk) {
				b2 = chunk[i+2]
			}
			c0 := b0 >> 2
			c1 := ((b0 & 0x03) << 4) | (b1 >> 4)
			c2 := ((b1 & 0x0f) << 2) | (b2 >> 6)
			c3 := b2 & 0x3f
			if trimSpaces {
				// Use space for 0 so trimming strips trailing zero groups.
				put := func(v byte) {
					if v == 0 {
						line.WriteByte(' ')
					} else {
						line.WriteByte(v + 0x20)
					}
				}
				put(c0)
				put(c1)
				put(c2)
				put(c3)
			} else {
				line.WriteByte(encChar(c0))
				line.WriteByte(encChar(c1))
				line.WriteByte(encChar(c2))
				line.WriteByte(encChar(c3))
			}
		}
		s := line.String()
		if trimSpaces {
			s = strings.TrimRight(s, " ")
		}
		buf.WriteString(s + "\r\n")
	}
	if name != "" {
		buf.WriteString("`\r\nend\r\n")
	}
	return buf.Bytes()
}

func TestUUEncodeDecodeWithHeaderAndTrimmedSpaces(t *testing.T) {
	payload := append(bytes.Repeat([]byte("uuencoded-usenet-article-"), 20), 0, 0, 0, 0, 0, 0)
	raw := encodeUU(payload, "Movie.2024.mkv", "644", true)
	p, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode UU: %v", err)
	}
	if p.Header.Name != "Movie.2024.mkv" {
		t.Fatalf("Header.Name = %q, want Movie.2024.mkv", p.Header.Name)
	}
	if !bytes.Equal(p.Data, payload) {
		t.Fatalf("decoded payload mismatch (%d vs %d bytes)", len(p.Data), len(payload))
	}
}

func TestUUEncodeDecodeContinuationWithoutBegin(t *testing.T) {
	payload := bytes.Repeat([]byte("continuation-part-payload-bytes-45-aligned!"), 5)
	raw := encodeUU(payload, "", "", false)
	p, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode raw UU continuation: %v", err)
	}
	if !bytes.Equal(p.Data, payload) {
		t.Fatalf("decoded continuation mismatch (%d vs %d bytes)", len(p.Data), len(payload))
	}
}
