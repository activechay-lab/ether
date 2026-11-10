package kindstream

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/core"
)

type fakeMediaSource struct {
	id     string
	name   string
	data   []byte
	closed bool
}

func (f *fakeMediaSource) ReleaseID() string { return f.id }
func (f *fakeMediaSource) Name() string      { return f.name }
func (f *fakeMediaSource) Size() int64       { return int64(len(f.data)) }
func (f *fakeMediaSource) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(f.data).ReadAt(p, off)
}
func (f *fakeMediaSource) Close() error {
	f.closed = true
	return nil
}

func buildTestZIP(t *testing.T, name string, payload []byte, method uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(out, flate.BestCompression)
	})
	hdr := &zip.FileHeader{
		Name:   name,
		Method: method,
	}
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("Write zip member: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close zip writer: %v", err)
	}
	return buf.Bytes()
}

// buildMinimalCompressedRAR constructs a valid RAR4 archive header + file
// header with compression method m3 (non-store) so ListRAR marks Stored=false.
func buildMinimalCompressedRAR() []byte {
	var buf bytes.Buffer
	// RAR4 signature
	buf.WriteString("Rar!\x1a\x07\x00")
	// Archive block (type 0x73)
	archHdr := []byte{
		0x73,       // type = MAIN_HEAD
		0x00, 0x00, // flags
		0x0d, 0x00, // size = 13
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	buf.Write(prependRAR4CRC(archHdr))

	// File block (type 0x74) with method 0x33 ('3' = normal compression, != 0x30 store)
	name := "compressed.bin"
	packed := []byte("compressed-data-bytes")
	fileBody := make([]byte, 0, 32+len(name))
	fileBody = append(fileBody,
		0x74,       // type = FILE_HEAD
		0x00, 0x80, // flags (LONG_BLOCK)
	)
	hdrSize := uint16(7 + 25 + len(name))
	fileBody = append(fileBody, byte(hdrSize), byte(hdrSize>>8))
	// packSize (4), unpSize (4), hostOS (1), fileCRC (4), ftime (4), unpVer (1), method (1), nameSize (2), attr (4)
	var meta [25]byte
	meta[0] = byte(len(packed))
	meta[4] = 100  // unpSize = 100
	meta[17] = 29  // unpVer
	meta[18] = 0x33 // method = m3 (compressed!)
	meta[19] = byte(len(name))
	fileBody = append(fileBody, meta[:]...)
	fileBody = append(fileBody, []byte(name)...)
	buf.Write(prependRAR4CRC(fileBody))
	buf.Write(packed)
	return buf.Bytes()
}

func prependRAR4CRC(body []byte) []byte {
	const poly = 0xedb88320
	crc := uint32(0xffffffff)
	for _, b := range body {
		crc ^= uint32(b)
		for range 8 {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ poly
			} else {
				crc >>= 1
			}
		}
	}
	crc = ^crc
	out := make([]byte, 2+len(body))
	out[0] = byte(crc)
	out[1] = byte(crc >> 8)
	copy(out[2:], body)
	return out
}

func TestKindStreamAllPatternsAndAcceptance(t *testing.T) {
	ctx := context.Background()

	// Load real fixture payload_a.bin and archive.rar from internal/archive/testdata/altmount.
	fixtureDir := filepath.Join("..", "archive", "testdata", "altmount")
	wantPayload, err := os.ReadFile(filepath.Join(fixtureDir, "payload_a.bin"))
	if err != nil {
		t.Fatalf("read payload_a.bin: %v", err)
	}
	rarSingleBytes, err := os.ReadFile(filepath.Join(fixtureDir, "archive.rar"))
	if err != nil {
		t.Fatalf("read archive.rar: %v", err)
	}
	rarPart1Bytes, err := os.ReadFile(filepath.Join(fixtureDir, "archive.part01.rar"))
	if err != nil {
		t.Fatalf("read archive.part01.rar: %v", err)
	}
	rarPart2Bytes, err := os.ReadFile(filepath.Join(fixtureDir, "archive.part02.rar"))
	if err != nil {
		t.Fatalf("read archive.part02.rar: %v", err)
	}

	// Build a stored ZIP and a deflate-compressed ZIP of wantPayload.
	zipStoredBytes := buildTestZIP(t, "book/payload_a.bin", wantPayload, zip.Store)
	zipDeflateBytes := buildTestZIP(t, "book/payload_a.bin", wantPayload, zip.Deflate)
	rarCompressedBytes := buildMinimalCompressedRAR()

	var seenAuthHeader atomic.Value
	var lastTemplatedPath atomic.Value

	mux := http.NewServeMux()
	mux.HandleFunc("/range/payload.bin", func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			seenAuthHeader.Store(auth)
		}
		http.ServeContent(w, r, "payload.bin", time.Time{}, bytes.NewReader(wantPayload))
	})
	mux.HandleFunc("/norange/payload.bin", func(w http.ResponseWriter, r *http.Request) {
		// Intentionally ignore Range header and return 200 OK with full payload.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(wantPayload)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(wantPayload)
	})
	mux.HandleFunc("/template/", func(w http.ResponseWriter, r *http.Request) {
		lastTemplatedPath.Store(r.URL.Path + "?" + r.URL.RawQuery)
		q := r.URL.Query()
		if br := q.Get("br"); br != "" {
			parts := strings.SplitN(br, "-", 2)
			start, _ := strconv.ParseInt(parts[0], 10, 64)
			end, _ := strconv.ParseInt(parts[1], 10, 64)
			if end >= int64(len(wantPayload)) {
				end = int64(len(wantPayload)) - 1
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(wantPayload)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(wantPayload[start : end+1])
			return
		}
		startStr := q.Get("start")
		endStr := q.Get("end")
		if startStr != "" && endStr != "" {
			start, _ := strconv.ParseInt(startStr, 10, 64)
			end, _ := strconv.ParseInt(endStr, 10, 64)
			if end >= int64(len(wantPayload)) {
				end = int64(len(wantPayload)) - 1
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(wantPayload)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(wantPayload[start : end+1])
			return
		}
		http.ServeContent(w, r, "templated.bin", time.Time{}, bytes.NewReader(wantPayload))
	})
	mux.HandleFunc("/archive/single.rar", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "single.rar", time.Time{}, bytes.NewReader(rarSingleBytes))
	})
	mux.HandleFunc("/archive/part01.rar", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "part01.rar", time.Time{}, bytes.NewReader(rarPart1Bytes))
	})
	mux.HandleFunc("/archive/part02.rar", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "part02.rar", time.Time{}, bytes.NewReader(rarPart2Bytes))
	})
	mux.HandleFunc("/archive/stored.zip", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "stored.zip", time.Time{}, bytes.NewReader(zipStoredBytes))
	})
	mux.HandleFunc("/archive/deflate.zip", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "deflate.zip", time.Time{}, bytes.NewReader(zipDeflateBytes))
	})
	mux.HandleFunc("/archive/compressed.rar", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "compressed.rar", time.Time{}, bytes.NewReader(rarCompressedBytes))
	})
	mux.HandleFunc("/error/html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-43/44")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>403</body></html>"))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	cache, err := blobcache.Open(t.TempDir(), blobcache.Options{SweepPeriod: -1})
	if err != nil {
		t.Fatalf("blobcache.Open: %v", err)
	}
	defer cache.Close()
	ns := cache.Namespace("pk-test-lib", blobcache.Quota{MaxBytes: 32 << 20})

	payloadSum := sha256.Sum256(wantPayload)
	payloadSHA := hex.EncodeToString(payloadSum[:])

	deps := Deps{
		HTTP:      srv.Client(),
		Cache:     ns,
		ItemIndex: 4, // pageNumber = 5
		Secret: func(ctx context.Context, name string) (string, error) {
			if name == "api_token" {
				return "secret-xyz", nil
			}
			return "", fmt.Errorf("unknown secret %q", name)
		},
		OpenRelease: func(ctx context.Context, r ReleaseHint) (core.MediaSource, error) {
			if r.InfoHash == "archive-rar-release" {
				return &fakeMediaSource{
					id:   "rel-rar",
					name: "archive.rar",
					data: rarSingleBytes,
				}, nil
			}
			return &fakeMediaSource{
				id:   "rel-1",
				name: "release_payload.bin",
				data: wantPayload,
			}, nil
		},
	}

	// 1. Table test across all 5 retrieval patterns.
	patterns := []struct {
		name    string
		source  PlayableSource
		checkFn func(t *testing.T, bs ByteSource)
	}{
		{
			name: "pattern=range",
			source: PlayableSource{
				ID:      "src-range",
				Pattern: PatternRange,
				MIME:    "application/octet-stream",
				URL:     srv.URL + "/range/payload.bin",
				Headers: map[string]string{"Authorization": "Bearer {secret:api_token}"},
			},
			checkFn: func(t *testing.T, bs ByteSource) {
				if got, _ := seenAuthHeader.Load().(string); got != "Bearer secret-xyz" {
					t.Fatalf("Authorization header = %q, want Bearer secret-xyz", got)
				}
			},
		},
		{
			name: "pattern=download",
			source: PlayableSource{
				ID:       "src-download",
				Pattern:  PatternDownload,
				MIME:     "application/octet-stream",
				URL:      srv.URL + "/norange/payload.bin",
				Size:     int64(len(wantPayload)),
				SHA256:   payloadSHA,
				MaxBytes: 1 << 20,
			},
		},
		{
			name: "pattern=direct",
			source: PlayableSource{
				ID:      "src-direct",
				Pattern: PatternDirect,
				MIME:    "application/octet-stream",
				URL:     srv.URL + "/template/item-{itemIndex}/page-{pageNumber}.bin",
				Size:    int64(len(wantPayload)),
			},
			checkFn: func(t *testing.T, bs ByteSource) {
				ds, ok := AsDirectSource(bs)
				if !ok {
					t.Fatalf("expected *DirectSource for direct pattern")
				}
				wantURL := srv.URL + "/template/item-4/page-5.bin"
				if ds.DirectURL() != wantURL {
					t.Fatalf("DirectURL = %q, want %q", ds.DirectURL(), wantURL)
				}
			},
		},
		{
			name: "pattern=container (store-mode single RAR over HTTP)",
			source: PlayableSource{
				ID:      "src-container-rar",
				Pattern: PatternContainer,
				MIME:    "application/octet-stream",
				Container: &ContainerRef{
					Format: "rar",
					Volumes: []VolumeRef{
						{URL: srv.URL + "/archive/single.rar", Size: int64(len(rarSingleBytes))},
					},
					Member: "payload_a.bin",
				},
			},
		},
		{
			name: "pattern=container (store-mode multi-volume RAR over HTTP)",
			source: PlayableSource{
				ID:      "src-container-multirar",
				Pattern: PatternContainer,
				MIME:    "application/octet-stream",
				Container: &ContainerRef{
					Volumes: []VolumeRef{
						{URL: srv.URL + "/archive/part01.rar", Size: int64(len(rarPart1Bytes))},
						{URL: srv.URL + "/archive/part02.rar", Size: int64(len(rarPart2Bytes))},
					},
					MemberIndex: new(0),
				},
			},
		},
		{
			name: "pattern=release (direct MediaSource)",
			source: PlayableSource{
				ID:      "src-release",
				Pattern: PatternRelease,
				MIME:    "application/octet-stream",
				Release: &ReleaseHint{
					InfoHash: "abc12345",
				},
			},
		},
		{
			name: "pattern=release+container composition (RAR release)",
			source: PlayableSource{
				ID:      "src-release-rar",
				Pattern: PatternRelease,
				MIME:    "video/mp4",
				Release: &ReleaseHint{
					InfoHash: "archive-rar-release",
					FileHint: "payload_a.bin",
				},
			},
		},
	}

	for _, tc := range patterns {
		t.Run(tc.name, func(t *testing.T) {
			bs, err := Open(ctx, tc.source, deps)
			if err != nil {
				t.Fatalf("Open(%s): %v", tc.name, err)
			}
			defer bs.Close()

			if tc.checkFn != nil {
				tc.checkFn(t, bs)
			}
			if bs.Size() != int64(len(wantPayload)) {
				t.Fatalf("Size = %d, want %d", bs.Size(), len(wantPayload))
			}

			// Verify random-access slice read at middle offset.
			midOff := int64(77777)
			sliceBuf := make([]byte, 4096)
			n, err := bs.ReadAt(sliceBuf, midOff)
			if err != nil {
				t.Fatalf("ReadAt(%d): %v", midOff, err)
			}
			if n != len(sliceBuf) || !bytes.Equal(sliceBuf, wantPayload[midOff:midOff+int64(len(sliceBuf))]) {
				t.Fatalf("ReadAt slice mismatch at offset %d", midOff)
			}

			// Verify full byte-for-byte equality with local fixture payload_a.bin.
			full := make([]byte, bs.Size())
			n, err = bs.ReadAt(full, 0)
			if err != nil && !errors.Is(err, io.EOF) {
				t.Fatalf("ReadAt full: %v", err)
			}
			if n != len(wantPayload) || !bytes.Equal(full, wantPayload) {
				t.Fatalf("full content mismatch (read %d bytes, want %d)", n, len(wantPayload))
			}
		})
	}

	// 2. URL template placeholders ({itemIndex}, {pageNumber}, {byteStart}, {byteEnd}, {byteRange}).
	t.Run("URLTemplatePlaceholders", func(t *testing.T) {
		src1 := PlayableSource{
			ID:      "tmpl-start-end",
			Pattern: PatternRange,
			MIME:    "application/octet-stream",
			Size:    int64(len(wantPayload)),
			URL:     srv.URL + "/template/i-{itemIndex}/p-{pageNumber}?start={byteStart}&end={byteEnd}",
		}
		bs1, err := Open(ctx, src1, deps)
		if err != nil {
			t.Fatalf("Open tmpl-start-end: %v", err)
		}
		defer bs1.Close()

		buf := make([]byte, 128)
		if _, err := bs1.ReadAt(buf, 1000); err != nil {
			t.Fatalf("ReadAt(1000): %v", err)
		}
		if !bytes.Equal(buf, wantPayload[1000:1128]) {
			t.Fatalf("templated ReadAt mismatch")
		}
		if got, _ := lastTemplatedPath.Load().(string); got != "/template/i-4/p-5?start=1000&end=1127" {
			t.Fatalf("expanded URL path = %q, want /template/i-4/p-5?start=1000&end=1127", got)
		}

		src2 := PlayableSource{
			ID:      "tmpl-byterange",
			Pattern: PatternRange,
			MIME:    "application/octet-stream",
			Size:    int64(len(wantPayload)),
			URL:     srv.URL + "/template/i-{itemIndex}?br={byteRange}",
		}
		bs2, err := Open(ctx, src2, deps)
		if err != nil {
			t.Fatalf("Open tmpl-byterange: %v", err)
		}
		defer bs2.Close()
		if _, err := bs2.ReadAt(buf, 2000); err != nil {
			t.Fatalf("ReadAt(2000): %v", err)
		}
		if got, _ := lastTemplatedPath.Load().(string); got != "/template/i-4?br=2000-2127" {
			t.Fatalf("expanded byteRange path = %q, want /template/i-4?br=2000-2127", got)
		}

		// Unknown placeholder must fail validation.
		if _, err := ParseURLTemplate("https://example.com/{evil}", nil); !errors.Is(err, ErrInvalidTemplate) {
			t.Fatalf("expected ErrInvalidTemplate for unknown placeholder, got %v", err)
		}
	})

	// 3. Range-less upstream demotion from range to download.
	t.Run("RangeDemotionToDownload", func(t *testing.T) {
		// When MaxBytes is too small, range demotion must fail.
		tooSmall := PlayableSource{
			ID:       "demote-too-large",
			Pattern:  PatternRange,
			MIME:     "application/octet-stream",
			URL:      srv.URL + "/norange/payload.bin",
			MaxBytes: 1024, // 1 KiB < 200 KB payload
		}
		if _, err := Open(ctx, tooSmall, deps); !errors.Is(err, ErrSizeLimitExceeded) {
			t.Fatalf("expected ErrSizeLimitExceeded on oversized range demotion, got %v", err)
		}

		// When within MaxBytes, range automatically demotes to download into blobcache.
		allowed := PlayableSource{
			ID:       "demote-ok",
			Pattern:  PatternRange,
			MIME:     "application/octet-stream",
			URL:      srv.URL + "/norange/payload.bin",
			MaxBytes: 1 << 20,
		}
		bs, err := Open(ctx, allowed, deps)
		if err != nil {
			t.Fatalf("Open demote-ok: %v", err)
		}
		defer bs.Close()
		slice := make([]byte, 500)
		if _, err := bs.ReadAt(slice, 12345); err != nil {
			t.Fatalf("ReadAt demoted: %v", err)
		}
		if !bytes.Equal(slice, wantPayload[12345:12845]) {
			t.Fatalf("demoted ReadAt mismatch")
		}
	})

	// 4. Compressed-zip member extraction into blobcache + undecodable compressed RAR fallback + HTML MIME mismatch fallback.
	t.Run("CompressedZIPExtractionAndFallback", func(t *testing.T) {
		sources := []PlayableSource{
			// Candidate 1: Upstream returns an HTML error page for an image/binary source -> ErrMIMEMismatch
			{
				ID:      "bad-html",
				Pattern: PatternRange,
				MIME:    "image/jpeg",
				URL:     srv.URL + "/error/html",
			},
			// Candidate 2: Compressed RAR member -> ErrMemberUndecodable
			{
				ID:      "undecodable-rar",
				Pattern: PatternContainer,
				MIME:    "application/octet-stream",
				Container: &ContainerRef{
					Format: "rar",
					Volumes: []VolumeRef{
						{URL: srv.URL + "/archive/compressed.rar", Size: int64(len(rarCompressedBytes))},
					},
					Member: "compressed.bin",
				},
			},
			// Candidate 3: Deflate-compressed ZIP member -> extracted into blobcache!
			{
				ID:      "deflate-zip",
				Pattern: PatternContainer,
				MIME:    "application/octet-stream",
				Container: &ContainerRef{
					Format: "zip",
					Volumes: []VolumeRef{
						{URL: srv.URL + "/archive/deflate.zip", Size: int64(len(zipDeflateBytes))},
					},
					Member: "book/payload_a.bin",
				},
			},
		}

		// Verify candidate 2 alone returns ErrMemberUndecodable.
		if _, err := Open(ctx, sources[1], deps); !errors.Is(err, ErrMemberUndecodable) {
			t.Fatalf("Open(compressed RAR) = %v, want ErrMemberUndecodable", err)
		}

		// Verify OpenSources skips candidates 1 and 2 and succeeds on candidate 3.
		bs, chosen, err := OpenSources(ctx, sources, deps)
		if err != nil {
			t.Fatalf("OpenSources: %v", err)
		}
		defer bs.Close()
		if chosen.ID != "deflate-zip" {
			t.Fatalf("chosen source = %q, want deflate-zip", chosen.ID)
		}
		if bs.Size() != int64(len(wantPayload)) {
			t.Fatalf("extracted ZIP member size = %d, want %d", bs.Size(), len(wantPayload))
		}
		got := make([]byte, len(wantPayload))
		if _, err := bs.ReadAt(got, 0); err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("ReadAt extracted ZIP member: %v", err)
		}
		if !bytes.Equal(got, wantPayload) {
			t.Fatalf("extracted ZIP member content does not match payload_a.bin")
		}
	})

	// 5. Sequence lazy leaf opening, cross-leaf ReadAt, max-2 open leaves, and 90% prefetch.
	t.Run("SequenceConcatenatedReadAtAndPrefetch", func(t *testing.T) {
		part0 := bytes.Repeat([]byte("0123456789"), 10) // 100 bytes
		part1 := bytes.Repeat([]byte("abcdefghij"), 10) // 100 bytes
		part2 := bytes.Repeat([]byte("ABCDEFGHIJ"), 10) // 100 bytes
		part3 := bytes.Repeat([]byte("klmnopqrst"), 10) // 100 bytes
		parts := [][]byte{part0, part1, part2, part3}
		concatWant := bytes.Join(parts, nil)

		var openCalls [4]int32
		leaves := make([]Leaf, 4)
		for i := range 4 {
			idx := i
			leaves[i] = Leaf{
				Name: fmt.Sprintf("part-%d.bin", idx),
				MIME: "audio/mpeg",
				Size: int64(len(parts[idx])),
				Open: func(ctx context.Context) (ByteSource, error) {
					atomic.AddInt32(&openCalls[idx], 1)
					return &bytesByteSource{
						r:    bytes.NewReader(parts[idx]),
						size: int64(len(parts[idx])),
						name: fmt.Sprintf("part-%d.bin", idx),
						mime: "audio/mpeg",
					}, nil
				},
			}
		}

		seq := NewSequence(leaves)
		defer seq.Close()

		if seq.OpenLeafCount() != 0 {
			t.Fatalf("initial OpenLeafCount = %d, want 0 (lazy open)", seq.OpenLeafCount())
		}

		// Reading first 50 bytes of leaf 0 (50% < 90%) should open ONLY leaf 0.
		buf50 := make([]byte, 50)
		if _, err := seq.ReadAt(buf50, 0); err != nil {
			t.Fatalf("ReadAt(0,50): %v", err)
		}
		if !bytes.Equal(buf50, concatWant[:50]) {
			t.Fatalf("ReadAt(0,50) mismatch")
		}
		if seq.OpenLeafCount() != 1 || !seq.IsLeafOpen(0) || seq.IsLeafOpen(1) {
			t.Fatalf("after 50%% read: openCount=%d leaf0=%v leaf1=%v, want 1/true/false",
				seq.OpenLeafCount(), seq.IsLeafOpen(0), seq.IsLeafOpen(1))
		}

		// Reading up to offset 92 in leaf 0 (>= 90%) must prefetch leaf 1 without reading from leaf 1 yet.
		buf42 := make([]byte, 42)
		if _, err := seq.ReadAt(buf42, 50); err != nil {
			t.Fatalf("ReadAt(50,42): %v", err)
		}
		if !seq.IsLeafOpen(1) || seq.OpenLeafCount() != 2 {
			t.Fatalf("expected leaf 1 to be prefetched at >=90%% of leaf 0 (openCount=%d)", seq.OpenLeafCount())
		}

		// Cross-boundary read spanning leaf 0, leaf 1, and leaf 2 (offset 80, length 150 -> [80..230)).
		spanBuf := make([]byte, 150)
		if _, err := seq.ReadAt(spanBuf, 80); err != nil {
			t.Fatalf("ReadAt(80,150): %v", err)
		}
		if !bytes.Equal(spanBuf, concatWant[80:230]) {
			t.Fatalf("cross-leaf ReadAt(80,150) mismatch")
		}
		if seq.OpenLeafCount() > 2 {
			t.Fatalf("OpenLeafCount = %d, must never exceed 2", seq.OpenLeafCount())
		}

		// Jump to leaf 3 near end (offset 350..400) -> at most 2 open leaves.
		tailBuf := make([]byte, 50)
		if _, err := seq.ReadAt(tailBuf, 350); err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("ReadAt(350,50): %v", err)
		}
		if !bytes.Equal(tailBuf, concatWant[350:400]) {
			t.Fatalf("ReadAt(350,50) mismatch")
		}
		if seq.OpenLeafCount() > 2 {
			t.Fatalf("OpenLeafCount = %d, must never exceed 2", seq.OpenLeafCount())
		}
	})

	// 6. Loopback transcode bridge helper.
	t.Run("ServeLoopbackForTranscode", func(t *testing.T) {
		bs, err := Open(ctx, PlayableSource{
			ID:      "src-zip-stored",
			Pattern: PatternContainer,
			MIME:    "application/octet-stream",
			Container: &ContainerRef{
				Format: "zip",
				Volumes: []VolumeRef{
					{URL: srv.URL + "/archive/stored.zip", Size: int64(len(zipStoredBytes))},
				},
				Member: "book/payload_a.bin",
			},
		}, deps)
		if err != nil {
			t.Fatalf("Open stored zip: %v", err)
		}
		defer bs.Close()

		loopURL, closeLoop, err := ServeLoopback(bs)
		if err != nil {
			t.Fatalf("ServeLoopback: %v", err)
		}
		defer closeLoop()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, loopURL, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Range", "bytes=100-299")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET loopback: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusPartialContent {
			t.Fatalf("loopback status = %d, want 206", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil || !bytes.Equal(body, wantPayload[100:300]) {
			t.Fatalf("loopback range body mismatch")
		}
	})
}
