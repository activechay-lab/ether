package blobcache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/segcache"
)

func TestBlobCacheEndToEnd(t *testing.T) {
	ctx := context.Background()

	t.Run("PutOpenLookupAndVerifySHA256", func(t *testing.T) {
		dir := t.TempDir()
		now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		c, err := Open(dir, Options{
			SweepPeriod: -1,
			Now:         func() time.Time { return now },
		})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer c.Close()

		// Write a stale .part file to verify reopen cleans it up later.
		ns := c.Namespace("pk-books-comics", Quota{MaxBytes: 1 << 20})
		payload := []byte("hello comic book page 001")
		sum := sha256.Sum256(payload)
		wantDigest := hex.EncodeToString(sum[:])

		// Mismatched expected SHA-256 must fail and leave nothing behind.
		_, err = ns.PutWithExpectedSHA256(ctx, "page-1.jpg", bytes.NewReader(payload), int64(len(payload)), strings.Repeat("0", 64), "image/jpeg")
		if err == nil {
			t.Fatalf("expected sha256 mismatch error")
		}
		if used, count := ns.Stats(); used != 0 || count != 0 {
			t.Fatalf("stats after failed put = (%d, %d), want (0, 0)", used, count)
		}

		// Size mismatch must fail.
		_, err = ns.Put(ctx, "page-1.jpg", bytes.NewReader(payload), int64(len(payload)+5))
		if err == nil {
			t.Fatalf("expected size mismatch error")
		}

		// Matching expected SHA-256 succeeds.
		ref, err := ns.PutWithExpectedSHA256(ctx, "issue1/page-1.jpg", bytes.NewReader(payload), int64(len(payload)), wantDigest, "image/jpeg")
		if err != nil {
			t.Fatalf("PutWithExpectedSHA256: %v", err)
		}
		if ref.String() != "blob:"+wantDigest {
			t.Fatalf("ref = %q, want %q", ref, "blob:"+wantDigest)
		}

		gotRef, ok := ns.LookupKey("issue1/page-1.jpg")
		if !ok || gotRef != ref {
			t.Fatalf("LookupKey = (%q, %v), want (%q, true)", gotRef, ok, ref)
		}

		src, err := ns.Open(ref)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer src.Close()
		if src.Size() != int64(len(payload)) {
			t.Fatalf("Size = %d, want %d", src.Size(), len(payload))
		}
		if src.Name() != "page-1.jpg" {
			t.Fatalf("Name = %q, want page-1.jpg", src.Name())
		}
		if src.MIME() != "image/jpeg" {
			t.Fatalf("MIME = %q, want image/jpeg", src.MIME())
		}
		buf := make([]byte, 5)
		if _, err := src.ReadAt(buf, 6); err != nil {
			t.Fatalf("ReadAt: %v", err)
		}
		if string(buf) != "comic" {
			t.Fatalf("ReadAt(6,5) = %q, want comic", buf)
		}

		// Verify stale .part cleanup and index reload on reopen.
		stalePart := filepath.Join(dir, "pk-books-comics", ".stale.part")
		if err := os.WriteFile(stalePart, []byte("partial"), 0o644); err != nil {
			t.Fatalf("WriteFile stale: %v", err)
		}
		if err := c.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		c2, err := Open(dir, Options{SweepPeriod: -1, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatalf("Reopen: %v", err)
		}
		defer c2.Close()
		if _, err := os.Stat(stalePart); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale .part file still exists: %v", err)
		}
		ns2 := c2.Namespace("pk-books-comics", Quota{MaxBytes: 1 << 20})
		src2, err := ns2.OpenByKey("issue1/page-1.jpg")
		if err != nil {
			t.Fatalf("OpenByKey after reopen: %v", err)
		}
		defer src2.Close()
		all, err := io.ReadAll(src2)
		if err != nil || !bytes.Equal(all, payload) {
			t.Fatalf("ReadAll after reopen = %q, err=%v", all, err)
		}
	})

	t.Run("LRUEvictionByLastOpenAndTTLExpiry", func(t *testing.T) {
		dir := t.TempDir()
		var mu sync.Mutex
		now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		advance := func(d time.Duration) {
			mu.Lock()
			now = now.Add(d)
			mu.Unlock()
		}
		c, err := Open(dir, Options{
			SweepPeriod: -1,
			Now: func() time.Time {
				mu.Lock()
				defer mu.Unlock()
				return now
			},
		})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer c.Close()

		// Quota fits at most two 100-byte blobs (250 bytes max).
		ns := c.Namespace("lib-lru", Quota{MaxBytes: 250, TTL: 10 * time.Minute})
		blobA := bytes.Repeat([]byte("A"), 100)
		blobB := bytes.Repeat([]byte("B"), 100)
		blobC := bytes.Repeat([]byte("C"), 100)

		refA, err := ns.Put(ctx, "a", bytes.NewReader(blobA), 100)
		if err != nil {
			t.Fatalf("Put A: %v", err)
		}
		advance(time.Minute)
		refB, err := ns.Put(ctx, "b", bytes.NewReader(blobB), 100)
		if err != nil {
			t.Fatalf("Put B: %v", err)
		}

		// Touch A so B becomes the least-recently-opened entry.
		advance(time.Minute)
		srcA, err := ns.Open(refA)
		if err != nil {
			t.Fatalf("Open A: %v", err)
		}
		_ = srcA.Close()

		// Putting C pushes usage to 300 > 250; B must be evicted while A and C remain.
		advance(time.Minute)
		refC, err := ns.Put(ctx, "c", bytes.NewReader(blobC), 100)
		if err != nil {
			t.Fatalf("Put C: %v", err)
		}
		if _, ok := ns.LookupKey("b"); ok {
			t.Fatalf("expected B to be evicted by LRU")
		}
		if _, err := ns.Open(refB); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Open(refB) = %v, want ErrNotFound", err)
		}
		if _, ok := ns.LookupKey("a"); !ok {
			t.Fatalf("expected A to remain after touching")
		}
		if _, ok := ns.LookupKey("c"); !ok {
			t.Fatalf("expected C to remain")
		}

		// Advance past TTL (10m from creation); both A and C should expire.
		advance(15 * time.Minute)
		if _, err := ns.Open(refC); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected C to expire by TTL, got %v", err)
		}
		ns.Sweep()
		if used, count := ns.Stats(); used != 0 || count != 0 {
			t.Fatalf("stats after TTL sweep = (%d, %d), want (0, 0)", used, count)
		}
	})

	t.Run("PerLibraryIsolationSharedCapAndDiskFloor", func(t *testing.T) {
		dir := t.TempDir()
		now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

		// Simulate a 10 GiB disk where 5% is 512 MiB, or configure SharedCap.
		// First test per-library isolation under a generous shared cap.
		var totalDisk int64 = 100 << 30 // 100 GiB (5% = 5 GiB, MinFreeBytes = 5 GiB)
		var freeDisk int64 = 20 << 30   // 20 GiB free

		c, err := Open(dir, Options{
			SweepPeriod: -1,
			SharedCap:   1000,
			Now:         func() time.Time { now = now.Add(time.Second); return now },
			DiskUsage: func() (int64, int64, error) {
				return totalDisk, freeDisk, nil
			},
		})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer c.Close()

		ns1 := c.Namespace("lib-1", Quota{MaxBytes: 200})
		ns2 := c.Namespace("lib-2", Quota{MaxBytes: 200})

		if _, err := ns1.Put(ctx, "k1", bytes.NewReader(bytes.Repeat([]byte("1"), 150)), 150); err != nil {
			t.Fatalf("ns1 put 1: %v", err)
		}
		if _, err := ns2.Put(ctx, "k2", bytes.NewReader(bytes.Repeat([]byte("2"), 150)), 150); err != nil {
			t.Fatalf("ns2 put 1: %v", err)
		}

		// Overflowing ns1 evicts k1 in ns1 but must NOT touch k2 in ns2.
		if _, err := ns1.Put(ctx, "k1-new", bytes.NewReader(bytes.Repeat([]byte("3"), 150)), 150); err != nil {
			t.Fatalf("ns1 put 2: %v", err)
		}
		if _, ok := ns1.LookupKey("k1"); ok {
			t.Fatalf("ns1 should have evicted k1")
		}
		if _, ok := ns2.LookupKey("k2"); !ok {
			t.Fatalf("ns2 k2 must be isolated from ns1 eviction")
		}

		// Now tighten disk free space so free space is at segcache.MinFreeBytes(totalDisk) - 200
		// (only 100 bytes of headroom for the cache).
		floor := segcache.MinFreeBytes(totalDisk)
		// Currently used = 300 (150 in ns1, 150 in ns2). Set freeDisk = floor - 140 so avail = 300 + (floor - 140) - floor = 160.
		freeDisk = floor - 140
		c.SweepAll()
		totalUsed, totalBlobs := c.TotalStats()
		if totalUsed > 160 || totalBlobs != 1 {
			t.Fatalf("after floor enforcement: used=%d blobs=%d, want <=160 and 1 blob", totalUsed, totalBlobs)
		}

		// Purge ns1 removes its directory and blobs.
		ns1.Purge()
		if u, n := ns1.Stats(); u != 0 || n != 0 {
			t.Fatalf("ns1 stats after Purge = (%d, %d), want (0, 0)", u, n)
		}
	})

	t.Run("HostBackendsAndSingleFlight", func(t *testing.T) {
		dir := t.TempDir()
		c, err := Open(dir, Options{SweepPeriod: -1})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer c.Close()

		putFn, openFn := c.HostBackends(func(pluginID, libraryKey string) Quota {
			return Quota{MaxBytes: 1 << 20}
		})

		refStr, err := putFn(ctx, "org.ether.books", "pk-org-ether-books-comics", "cover.png", strings.NewReader("png-bytes"))
		if err != nil {
			t.Fatalf("CachePut: %v", err)
		}
		// Calling plugin can open its blob.
		rc, err := openFn(ctx, "org.ether.books", refStr)
		if err != nil {
			t.Fatalf("CacheOpen same plugin: %v", err)
		}
		_ = rc.Close()

		// Another plugin cannot open org.ether.books's blob.
		if _, err := openFn(ctx, "org.ether.other", refStr); !errors.Is(err, ErrNotFound) {
			t.Fatalf("CacheOpen other plugin = %v, want ErrNotFound", err)
		}

		// SingleFlightPut deduplicates concurrent callers.
		ns := c.Namespace("pk-org-ether-books-comics", Quota{MaxBytes: 1 << 20})
		var calls int
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 4)
		refs := make([]Ref, 4)
		for i := range 4 {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start
				refs[idx], errs[idx] = ns.SingleFlightPut(ctx, "shared-page", func(ctx context.Context) (Ref, error) {
					calls++
					time.Sleep(20 * time.Millisecond)
					return ns.Put(ctx, "shared-page", strings.NewReader("shared-payload"), int64(len("shared-payload")))
				})
			}(i)
		}
		close(start)
		wg.Wait()
		for i := range 4 {
			if errs[i] != nil {
				t.Fatalf("SingleFlightPut[%d]: %v", i, errs[i])
			}
			if refs[i] != refs[0] {
				t.Fatalf("SingleFlightPut[%d] ref = %q, want %q", i, refs[i], refs[0])
			}
		}
		if calls != 1 {
			t.Fatalf("SingleFlightPut ran fn %d times, want 1", calls)
		}
	})
}
