package yenc

import (
	"bytes"
	"errors"
	"strconv"
	"sync"
	"testing"
)

func TestArticleDecoderRetainsContract(t *testing.T) {
	data := randBytes(346, 4097)
	for _, declared := range []int{4096, 4097, 4098} {
		enc, err := EncodeBytes(data, EncodeOptions{Name: "contract"})
		if err != nil {
			t.Fatal(err)
		}
		enc = bytes.Replace(enc, []byte("size=4097 name="), []byte("size="+strconv.Itoa(declared)+" name="), 1)
		p, err := Decode(enc)
		if declared == len(data) {
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrSizeMismatch) {
			t.Fatalf("declared=%d: %v", declared, err)
		}
		if p == nil || !bytes.Equal(p.Data, data) {
			t.Fatalf("declared=%d: missing verified/partial data", declared)
		}
	}
}

func TestArticleDecoderTruncationAndOutputOwnership(t *testing.T) {
	data := randBytes(346, 4096)
	enc, _ := EncodeBytes(data, EncodeOptions{Name: "first"})
	cut := enc[:bytes.LastIndex(enc, []byte("=yend"))]
	p, err := Decode(cut)
	if !errors.Is(err, ErrTruncated) || p == nil || !bytes.Equal(p.Data, data) {
		t.Fatalf("truncation: %v", err)
	}
	for _, capacity := range []int{len(data), len(data) + 64, len(data) * 2} {
		buf := make([]byte, 0, capacity)
		p, err = DecodeLines(&bytesLines{b: enc}, &DecodeOptions{Buffer: buf})
		if err != nil || !bytes.Equal(p.Data, data) || &p.Data[0] != &buf[:1][0] {
			t.Fatalf("capacity=%d reuse: %v", capacity, err)
		}
	}
	lr := &bytesLines{b: append(bytes.Clone(enc), enc...)}
	first, err := DecodeLines(lr, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DecodeLines(lr, nil)
	if err != nil {
		t.Fatal(err)
	}
	second.Data[0] ^= 1
	if !bytes.Equal(first.Data, data) {
		t.Fatal("later decode overwrote returned data")
	}
}

func TestArticleDecoderBoundsDishonestHeader(t *testing.T) {
	data := randBytes(346, 10000)
	enc, _ := EncodeBytes(data, EncodeOptions{Name: "bounded"})
	enc = bytes.Replace(enc, []byte("size=10000 name="), []byte("size=4096 name="), 1)
	p, err := DecodeLines(&bytesLines{b: enc}, &DecodeOptions{MaxSize: 4500, Buffer: make([]byte, 0, 1<<20)})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("bound: %v", err)
	}
	if p != nil && len(p.Data) > 4500 {
		t.Fatal("output exceeds bound")
	}
}

func TestArticleDecoderNormalizesSplitEscapes(t *testing.T) {
	prefix := bytes.Repeat([]byte{'a'}, 4096)
	wire := append([]byte("=ybegin size=4099 name=split\r\n"), prefix...)
	wire = append(wire, []byte("\r\n=\r\n}xy\r\n=yend size=4099\r\nsignature\r\n")...)
	want := append(bytes.Repeat([]byte{'a' - 42}, 4096), 19, 'x'-42, 'y'-42)
	lr := &bytesLines{b: wire}
	p, err := DecodeLines(lr, nil)
	if err != nil || !bytes.Equal(p.Data, want) {
		t.Fatalf("split escape: %v", err)
	}
	line, err := lr.ReadLine()
	if err != nil || string(line) != "signature" {
		t.Fatal("decoder consumed trailing text")
	}
}

func TestConcurrentArticleDecoders(t *testing.T) {
	data := randBytes(346, 8192)
	wire, _ := EncodeBytes(data, EncodeOptions{Name: "concurrent"})
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				p, err := Decode(bytes.Clone(wire))
				if err != nil || p == nil || !bytes.Equal(p.Data, data) {
					t.Errorf("concurrent decode: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestArticleDecoderBothCRCsAndSkip(t *testing.T) {
	data := randBytes(346, 4096)
	for _, fileSize := range []int64{4096, 8192} {
		wire, _ := EncodeBytes(data, EncodeOptions{Name: "crc", Part: 1, Total: 2, FileSize: fileSize, HasFileCRC: true, FileCRC: 0})
		p, err := Decode(wire)
		if fileSize == 4096 {
			if !errors.Is(err, ErrCRCMismatch) || p == nil || !bytes.Equal(p.Data, data) {
				t.Fatalf("whole CRC: %v", err)
			}
		} else if err != nil {
			t.Fatalf("partial whole-file CRC: %v", err)
		}
		if _, err := DecodeLines(&bytesLines{b: wire}, &DecodeOptions{SkipCRC: true}); err != nil {
			t.Fatalf("skip CRC: %v", err)
		}
	}
}
