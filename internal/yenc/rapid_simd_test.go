//go:build !cgo && goexperiment.simd && (amd64 || arm64)

package yenc

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"runtime"
	"testing"
)

func TestRapidPublicDecoder(t *testing.T) {
	if !rapidEnabled() {
		t.Skip("SIMD is unavailable")
	}
	data := randBytes(346, 768<<10)
	enc, _ := EncodeBytes(data, EncodeOptions{Name: "direct"})
	p, err := DecodeLines(&bytesLines{b: enc}, nil)
	if err != nil || !bytes.Equal(p.Data, data) {
		t.Fatalf("public decoder: %v", err)
	}
	t.Logf("decoder kernel: %s", rapidKernel())
}

func TestRapidLineInfo(t *testing.T) {
	if !rapidEnabled() {
		t.Skip("SIMD unavailable")
	}
	for size := 0; size <= 257; size++ {
		line := randBytes(int64(size), size)
		for _, escaped := range []bool{false, true} {
			for _, pattern := range []byte{'=', '\r', '\n', 'a'} {
				for i := 0; i < size; i += 17 {
					line[i] = pattern
				}
				want, wantEsc := decodeLineScalar(line, escaped)
				n, next, _ := rapidLineInfo(line, escaped)
				if n != len(want) || next != wantEsc {
					t.Fatalf("size=%d escaped=%v: %d/%v want %d/%v", size, escaped, n, next, len(want), wantEsc)
				}
			}
		}
	}
}

func FuzzRapidArticleCompatibility(f *testing.F) {
	data := randBytes(346, 4096)
	enc, _ := EncodeBytes(data, EncodeOptions{Name: "seed", Part: 2, Total: 3, Begin: 4097, FileSize: 12288})
	f.Add(enc, uint16(16384), false)
	f.Add(enc, uint16(16384), true)
	f.Fuzz(func(t *testing.T, wire []byte, limit uint16, reuse bool) {
		assertArticleCompatibility(t, wire, limit, reuse)
	})
}

func assertArticleCompatibility(t *testing.T, wire []byte, limit uint16, reuse bool) {
	t.Helper()
	opt := DecodeOptions{MaxSize: int64(limit) + 1024}
	if reuse {
		opt.Buffer = make([]byte, 0, 8192)
	}
	got, gotErr := DecodeLines(&bytesLines{b: wire}, &opt)
	lr := &bytesLines{b: wire}
	want, parseErr := readBegin(lr)
	var wantErr error
	if parseErr != nil {
		wantErr = parseErr
	} else {
		referenceOpt := DecodeOptions{MaxSize: opt.MaxSize}
		if reuse {
			referenceOpt.Buffer = make([]byte, 0, 8192)
		}
		want, wantErr = decodeBodySWAR(lr, referenceOpt, want, nil, true, false, bodyWireBudget(opt.MaxSize))
	}
	sameError := gotErr == nil && wantErr == nil
	for _, kind := range []error{ErrNoHeader, ErrTruncated, ErrCRCMismatch, ErrSizeMismatch, ErrMalformed, ErrTooLarge} {
		if errors.Is(gotErr, kind) && errors.Is(wantErr, kind) {
			sameError = true
		}
	}
	if !sameError {
		t.Fatalf("error mismatch: got=%v want=%v", gotErr, wantErr)
	}
	if (got == nil) != (want == nil) {
		t.Fatalf("partial output mismatch: got=%v want=%v", gotErr, wantErr)
	}
	if got != nil && (got.Header != want.Header || got.Trailer != want.Trailer || !bytes.Equal(got.Data, want.Data)) {
		t.Fatalf("data/metadata mismatch: got=%v want=%v", gotErr, wantErr)
	}
}

func TestRapidCompatibilityMutations(t *testing.T) {
	rng := rand.New(rand.NewSource(346))
	data := randBytes(346, 4096)
	seed, _ := EncodeBytes(data, EncodeOptions{Name: "seed", Part: 2, Total: 3, Begin: 4097, FileSize: 12288})
	for i := 0; i < 300; i++ {
		wire := bytes.Clone(seed)
		for j := 0; j < 1+i%7; j++ {
			wire[rng.Intn(len(wire))] = byte(rng.Intn(256))
		}
		assertArticleCompatibility(t, wire, 16384, i%2 == 0)
	}
}

type explicitArticleLines struct{ lines [][]byte }

func (l *explicitArticleLines) ReadLine() ([]byte, error) {
	if len(l.lines) == 0 {
		return nil, io.EOF
	}
	line := l.lines[0]
	l.lines = l.lines[1:]
	return line, nil
}

func TestRapidPoolAfterTrailerControlCharacters(t *testing.T) {
	if !rapidEnabled() {
		t.Skip("SIMD unavailable")
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for i := 0; i < 8; i++ {
		trailer := []byte("=yend size=4096")
		if i == 0 {
			trailer = []byte("=yend size=4096\r\n.\r\npoison")
		}
		lr := &explicitArticleLines{lines: [][]byte{[]byte("=ybegin size=4096 name=controls"), bytes.Repeat([]byte{'a'}, 4096), trailer, []byte("signature")}}
		p, err := DecodeLines(lr, nil)
		if err != nil || p == nil || !bytes.Equal(p.Data, bytes.Repeat([]byte{'a' - 42}, 4096)) {
			t.Fatalf("decode %d: %v", i, err)
		}
		if len(lr.lines) != 1 {
			t.Fatal("read beyond trailer")
		}
	}
}
