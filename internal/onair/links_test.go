package onair

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extras"
)

func TestLinkClipZeroMediaAndResolutionForYouTubeAndArchive(t *testing.T) {
	ctx := context.Background()
	var dlCalls atomic.Int64
	dl := extras.NewDownloader(extras.DownloaderOptions{
		Config:   func() config.Extras { return config.Extras{ResolverCommand: "yt-dlp"} },
		LookPath: func(cmd string) (string, error) { return cmd, nil },
		Runner: func(_ context.Context, _ string, args []string) ([]byte, error) {
			dlCalls.Add(1)
			var outTemplate, target string
			for i := 0; i < len(args); i++ {
				if args[i] == "-o" && i+1 < len(args) {
					outTemplate = args[i+1]
				}
				if strings.HasPrefix(args[i], "http") {
					target = args[i]
				}
			}
			out := strings.ReplaceAll(outTemplate, "%(ext)s", "mp4")
			if err := os.WriteFile(out, []byte("raw-"+target), 0o600); err != nil {
				return nil, err
			}
			title := "YouTube Commercial 1988"
			if strings.Contains(target, "archive.org/details/") {
				title = "Archive Commercial 1994"
			}
			meta, _ := json.Marshal(map[string]any{
				"_type":    "video",
				"title":    title,
				"uploader": "Retro Vault",
				"duration": 30.0,
			})
			return append(meta, '\n'), nil
		},
	})

	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	s := testService(t, Options{
		Downloader: dl,
		Now:        func() time.Time { return now },
		NormalizeClip: func(_ context.Context, _ Reel, source, out string) (time.Duration, error) {
			raw, err := os.ReadFile(source)
			if err != nil {
				return 0, err
			}
			if err := os.WriteFile(out, append([]byte("norm:"), raw...), 0o600); err != nil {
				return 0, err
			}
			return 30 * time.Second, nil
		},
	})

	reel, err := s.SaveReel(ctx, Reel{Name: "Decade Ads", Order: "sequence", LoudnessMatch: true, TrimSilence: true})
	if err != nil {
		t.Fatalf("SaveReel: %v", err)
	}

	ytURL := "https://www.youtube.com/watch?v=spot1988"
	iaURL := "https://archive.org/details/spot_1994_vhs"
	imported, err := s.Import(ctx, reel.ID, ImportRequest{
		URLs: []string{ytURL, iaURL},
		Mode: "link",
		Tags: []string{"1980s", "toy"},
	})
	if err != nil {
		t.Fatalf("Import mode=link: %v", err)
	}
	if len(imported) != 2 {
		t.Fatalf("expected 2 link clips, got %d", len(imported))
	}
	if dlCalls.Load() != 0 {
		t.Fatalf("link creation must not invoke downloader, got %d calls", dlCalls.Load())
	}
	if _, err := os.Stat(s.linkDir()); !errors.Is(err, os.ErrNotExist) {
		entries, _ := os.ReadDir(s.linkDir())
		if len(entries) != 0 {
			t.Fatalf("expected zero cached files on link creation, got %d", len(entries))
		}
	}

	gotReel, err := s.Reel(ctx, reel.ID)
	if err != nil {
		t.Fatalf("Reel: %v", err)
	}
	if len(gotReel.Clips) != 2 || !gotReel.Clips[0].Link || gotReel.Clips[0].Cached || !gotReel.Clips[1].Link || gotReel.Clips[1].Cached {
		t.Fatalf("unexpected saved link clips: %+v", gotReel.Clips)
	}

	// Resolve both YouTube and archive.org/details/ clips via watched-channel Ensure.
	for _, clip := range gotReel.Clips {
		s.Ensure(ctx, core.ClipRequest{
			ChannelID:  "watched-80",
			Moment:     "reel",
			ReelID:     reel.ID,
			ReelClipID: clip.ID,
			Boundary:   now.Add(2 * time.Minute),
		})
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, _, ok0 := s.PeekCachedReelClip(reel.ID, gotReel.Clips[0].ID)
		_, _, ok1 := s.PeekCachedReelClip(reel.ID, gotReel.Clips[1].ID)
		if ok0 && ok1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, clip := range gotReel.Clips {
		path, dur, ok := s.PeekCachedReelClip(reel.ID, clip.ID)
		if !ok || path == "" || dur != 30*time.Second {
			t.Fatalf("clip %s not cached after Ensure: ok=%v path=%q dur=%v", clip.ID, ok, path, dur)
		}
	}
	if dlCalls.Load() != 2 {
		t.Fatalf("expected 2 downloader calls (YouTube + archive.org), got %d", dlCalls.Load())
	}

	// Cache hit avoids re-resolving.
	for _, clip := range gotReel.Clips {
		s.Ensure(ctx, core.ClipRequest{
			ChannelID:  "watched-80",
			Moment:     "reel",
			ReelID:     reel.ID,
			ReelClipID: clip.ID,
			Boundary:   now.Add(5 * time.Minute),
		})
	}
	if dlCalls.Load() != 2 {
		t.Fatalf("cache hit re-ran downloader: %d calls", dlCalls.Load())
	}

	// Verify HTTP handler on-demand preview serves cached bytes.
	mux := s.Handler()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/reels/"+reel.ID+"/clips/"+gotReel.Clips[0].ID+"/preview", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "norm:") {
		t.Fatalf("preview status=%d body=%q", rec.Code, rec.Body.String())
	}

	// Also verify archive.org/details/ fallback works offline when yt-dlp is unavailable.
	archiveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata/retro_fallback":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"metadata":{"title":"Fallback Archive Spot","creator":"Archive Uploader"},"files":[{"name":"spot.mp4","format":"h.264"}]}`))
		case "/download/retro_fallback/spot.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("archive-mp4-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer archiveSrv.Close()

	missingDL := extras.NewDownloader(extras.DownloaderOptions{
		Config:   func() config.Extras { return config.Extras{ResolverCommand: "missing-yt-dlp"} },
		LookPath: func(string) (string, error) { return "", errors.New("not installed") },
	})
	sNoYTDLP := testService(t, Options{
		Downloader:    missingDL,
		ArchiveOrigin: archiveSrv.URL,
		HTTPClient:    archiveSrv.Client(),
		ValidateURL:   func(context.Context, string) error { return nil },
		NormalizeClip: func(_ context.Context, _ Reel, source, out string) (time.Duration, error) {
			b, err := os.ReadFile(source)
			if err != nil {
				return 0, err
			}
			return 15 * time.Second, os.WriteFile(out, b, 0o600)
		},
	})
	r2, err := sNoYTDLP.SaveReel(ctx, Reel{Name: "Archive Only", Order: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	iaClip, err := sNoYTDLP.AddLinkClip(ctx, r2.ID, LinkClipInput{
		Source: "https://archive.org/details/retro_fallback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sNoYTDLP.ResolveReelClip(ctx, r2.ID, iaClip.ID); err != nil {
		t.Fatalf("ResolveReelClip archive fallback: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, ok := sNoYTDLP.PeekCachedReelClip(r2.ID, iaClip.ID); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, dur, ok := sNoYTDLP.PeekCachedReelClip(r2.ID, iaClip.ID); !ok || dur != 15*time.Second {
		t.Fatalf("archive.org fallback did not populate cache: ok=%v dur=%v", ok, dur)
	}
}

func TestLinkCacheBoundedLRUByLastAired(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	dl := extras.NewDownloader(extras.DownloaderOptions{
		Config:   func() config.Extras { return config.Extras{ResolverCommand: "yt-dlp"} },
		LookPath: func(cmd string) (string, error) { return cmd, nil },
		Runner: func(_ context.Context, _ string, args []string) ([]byte, error) {
			var outTemplate string
			for i := 0; i < len(args); i++ {
				if args[i] == "-o" && i+1 < len(args) {
					outTemplate = args[i+1]
				}
			}
			out := strings.ReplaceAll(outTemplate, "%(ext)s", "mp4")
			if err := os.WriteFile(out, []byte("raw"), 0o600); err != nil {
				return nil, err
			}
			return []byte(`{"_type":"video","title":"Spot","duration":15}` + "\n"), nil
		},
	})

	payload := bytes.Repeat([]byte("v"), 1024)
	s := testService(t, Options{
		Downloader:     dl,
		LinkCacheBytes: 2400, // fits 2 x 1024-byte clips, evicts on 3rd
		Now:            func() time.Time { return now },
		NormalizeClip: func(_ context.Context, _ Reel, _, out string) (time.Duration, error) {
			return 15 * time.Second, os.WriteFile(out, payload, 0o600)
		},
	})
	reel, err := s.SaveReel(ctx, Reel{Name: "LRU Reel", Order: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	var clips []ReelClip
	for _, id := range []string{"a", "b", "c"} {
		c, err := s.AddLinkClip(ctx, reel.ID, LinkClipInput{
			ID:     "clip-" + id,
			Name:   "Clip " + id,
			Source: "https://www.youtube.com/watch?v=lru_" + id,
		})
		if err != nil {
			t.Fatal(err)
		}
		clips = append(clips, c)
	}

	resolveAndWait := func(c ReelClip) {
		t.Helper()
		if _, err := s.ResolveReelClip(ctx, reel.ID, c.ID); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, _, ok := s.PeekCachedReelClip(reel.ID, c.ID); ok {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("clip %s did not resolve", c.ID)
	}

	resolveAndWait(clips[0]) // A resolved at T+0
	now = now.Add(10 * time.Minute)
	resolveAndWait(clips[1]) // B resolved at T+10m

	// Air A again at T+20m so B becomes least-recently-aired.
	now = now.Add(10 * time.Minute)
	if _, ok := s.CachedReelClip(reel.ID, clips[0].ID); !ok {
		t.Fatal("expected clip A in cache")
	}

	// Resolve C at T+30m -> exceeds 2400B -> must evict B, keeping A and C.
	now = now.Add(10 * time.Minute)
	resolveAndWait(clips[2])

	if _, _, ok := s.PeekCachedReelClip(reel.ID, clips[1].ID); ok {
		t.Fatal("expected least-recently-aired clip B to be evicted")
	}
	if _, _, ok := s.PeekCachedReelClip(reel.ID, clips[0].ID); !ok {
		t.Fatal("expected recently-aired clip A to remain cached")
	}
	if _, _, ok := s.PeekCachedReelClip(reel.ID, clips[2].ID); !ok {
		t.Fatal("expected newly-resolved clip C to remain cached")
	}
}

func TestLinkClipMissingResolverAndThrottledFailureLog(t *testing.T) {
	ctx := context.Background()
	var logBuf bytes.Buffer
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	dl := extras.NewDownloader(extras.DownloaderOptions{
		Config:   func() config.Extras { return config.Extras{ResolverCommand: "missing-yt-dlp"} },
		LookPath: func(string) (string, error) { return "", errors.New("executable not found") },
	})
	s := testService(t, Options{
		Downloader: dl,
		Now:        func() time.Time { return now },
		Logger:     slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	reel, err := s.SaveReel(ctx, Reel{Name: "Broken Links", Order: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	clip, err := s.AddLinkClip(ctx, reel.ID, LinkClipInput{
		Name:   "Needs Resolver",
		Source: "https://www.youtube.com/watch?v=dead_spot",
	})
	if err != nil {
		t.Fatal(err)
	}

	for attempt := range 2 {
		if attempt > 0 {
			now = now.Add(10 * time.Minute) // advance past retry backoff, still within faillog 15m throttle
		}
		s.Ensure(ctx, core.ClipRequest{
			ChannelID:  "watched-80",
			Moment:     "reel",
			ReelID:     reel.ID,
			ReelClipID: clip.ID,
			Boundary:   now.Add(time.Minute),
		})
		status := func() string {
			r, _ := s.Reel(ctx, reel.ID)
			if len(r.Clips) != 1 {
				return ""
			}
			return r.Clips[0].Status
		}
		// Generous bounds: only a hang should hit them, not a slow CI runner.
		if attempt > 0 {
			// The clip still shows the first attempt's error until the retry
			// picks it up; wait for the retry to start before waiting for it
			// to fail, or the loop below accepts the stale error.
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) && status() == "error" {
				time.Sleep(time.Millisecond)
			}
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) && status() != "error" {
			time.Sleep(10 * time.Millisecond)
		}
	}

	got, err := s.Reel(ctx, reel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Clips[0].Status != "error" || !strings.Contains(got.Clips[0].Error, "yt-dlp") {
		t.Fatalf("expected error status mentioning yt-dlp, got %+v", got.Clips[0])
	}
	lines := strings.Count(logBuf.String(), "onair: link clip resolution failed")
	if lines != 1 {
		t.Fatalf("expected throttled faillog to emit 1 warning across repeated failures, got %d:\n%s", lines, logBuf.String())
	}
}

func TestCancelChannelCancelsLinkResolution(t *testing.T) {
	ctx := context.Background()
	started := make(chan struct{}, 2)
	var runs atomic.Int64
	dl := extras.NewDownloader(extras.DownloaderOptions{
		Config:   func() config.Extras { return config.Extras{ResolverCommand: "yt-dlp"} },
		LookPath: func(cmd string) (string, error) { return cmd, nil },
		Runner: func(rctx context.Context, _ string, _ []string) ([]byte, error) {
			runs.Add(1)
			started <- struct{}{}
			<-rctx.Done()
			return nil, rctx.Err()
		},
	})
	s := testService(t, Options{Downloader: dl})
	reel, err := s.SaveReel(ctx, Reel{Name: "Cancel Reel", Order: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, slug := range []string{"one", "two", "three"} {
		c, err := s.AddLinkClip(ctx, reel.ID, LinkClipInput{
			Source: "https://www.youtube.com/watch?v=" + slug,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	for _, id := range ids {
		s.Ensure(ctx, core.ClipRequest{
			ChannelID:  "gone-ch",
			Moment:     "reel",
			ReelID:     reel.ID,
			ReelClipID: id,
		})
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("link download never started")
	}
	s.CancelChannel("gone-ch")

	drained := make(chan struct{})
	if !s.enqueue(renderJob{download: true, Hash: "drain-1", prepare: func(context.Context) error { close(drained); return nil }}) {
		t.Fatal("enqueue drain")
	}
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("download lane did not drain after CancelChannel")
	}
	for _, id := range ids {
		if _, _, ok := s.PeekCachedReelClip(reel.ID, id); ok {
			t.Fatalf("cancelled clip %s landed in cache", id)
		}
	}
}

func TestSeedShowcaseReelsUpgradeAndOwnerEdits(t *testing.T) {
	ctx := context.Background()
	s := testService(t, Options{Showcase: true})

	expectedCounts := map[string]int{
		"showcase-reel-80": 11,
		"showcase-reel-90": 12,
		"showcase-reel-00": 12,
		"showcase-reel-10": 11,
	}
	total := 0
	for id, want := range expectedCounts {
		r, err := s.Reel(ctx, id)
		if err != nil {
			t.Fatalf("shipped reel %s missing: %v", id, err)
		}
		if len(r.Clips) != want {
			t.Fatalf("reel %s clips=%d, want %d", id, len(r.Clips), want)
		}
		for _, c := range r.Clips {
			if !c.Link || c.Source == "" || c.DurationMS <= 0 {
				t.Fatalf("invalid shipped clip on %s: %+v", id, c)
			}
		}
		total += len(r.Clips)
	}
	if total != 46 {
		t.Fatalf("total shipped clips=%d, want 46", total)
	}
	if entries, err := os.ReadDir(s.linkDir()); err == nil && len(entries) != 0 {
		t.Fatalf("seeding shipped reels must write zero media files, got %d", len(entries))
	}

	// Owner deletes 1 clip from showcase-reel-80 and deletes all of showcase-reel-90.
	r80, _ := s.Reel(ctx, "showcase-reel-80")
	removedClipID := r80.Clips[0].ID
	if err := s.DeleteClip(ctx, "showcase-reel-80", removedClipID); err != nil {
		t.Fatalf("DeleteClip: %v", err)
	}
	if err := s.DeleteReel(ctx, "showcase-reel-90"); err != nil {
		t.Fatalf("DeleteReel: %v", err)
	}

	// Untouched showcase-reel-00 experiences runtime link error status at playtime:
	// it must STILL count as untouched when a newer shipped version arrives!
	r00, _ := s.Reel(ctx, "showcase-reel-00")
	mutClip := r00.Clips[0]
	mutClip.Status = "error"
	mutClip.Error = "temporary timeout"
	if err := s.setClip(ctx, "showcase-reel-00", mutClip); err != nil {
		t.Fatal(err)
	}

	// Re-run seedShowcase at same version (simulating restart): deletions stick.
	if err := s.seedShowcase(ctx); err != nil {
		t.Fatalf("seedShowcase restart: %v", err)
	}
	r80After, _ := s.Reel(ctx, "showcase-reel-80")
	if len(r80After.Clips) != 10 {
		t.Fatalf("owner single-clip deletion did not stick across restart: got %d clips", len(r80After.Clips))
	}
	if _, err := s.Reel(ctx, "showcase-reel-90"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner whole-reel deletion did not stick across restart: %v", err)
	}

	// Simulate an upgrade from version 0 in onair_settings for showcase reels:
	// - untouched showcase-reel-00 upgrades cleanly;
	// - owner-edited showcase-reel-80 (10 clips) stays untouched by seed;
	// - owner-deleted showcase-reel-90 stays deleted!
	rows, err := s.db.QueryContext(ctx, `SELECT k, v FROM onair_settings WHERE k LIKE 'showcase:showcase-reel-%'`)
	if err != nil {
		t.Fatal(err)
	}
	var pairs [][2]string
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			_, hash, _ := strings.Cut(v, ":")
			pairs = append(pairs, [2]string{k, "0:" + hash})
		}
	}
	rows.Close()
	for _, p := range pairs {
		if _, err := s.db.ExecContext(ctx, `UPDATE onair_settings SET v=? WHERE k=?`, p[1], p[0]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.seedShowcase(ctx); err != nil {
		t.Fatalf("seedShowcase version bump: %v", err)
	}
	r80Upgrade, _ := s.Reel(ctx, "showcase-reel-80")
	if len(r80Upgrade.Clips) != 10 {
		t.Fatalf("version bump overwrote owner clip deletion on showcase-reel-80: got %d clips", len(r80Upgrade.Clips))
	}
	if _, err := s.Reel(ctx, "showcase-reel-90"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("version bump resurrected owner-deleted showcase-reel-90: %v", err)
	}
	r00Upgrade, err := s.Reel(ctx, "showcase-reel-00")
	if err != nil || len(r00Upgrade.Clips) != 12 || r00Upgrade.Clips[0].Status != "ready" {
		t.Fatalf("untouched showcase-reel-00 did not upgrade cleanly: err=%v clips=%d", err, len(r00Upgrade.Clips))
	}
}
