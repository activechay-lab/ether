package onair

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extras"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImportRejectsPrivateRedirects(t *testing.T) {
	c := importClient()
	defer c.CloseIdleConnections()
	for _, raw := range []string{"http://127.0.0.1/a", "http://[::1]/a", "http://10.0.0.1/a", "http://169.254.169.254/a", "file:///etc/passwd", "https://user:pass@example.com/a"} {
		req, _ := http.NewRequest("GET", raw, nil)
		if e := c.CheckRedirect(req, nil); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestArchiveMetadataResolution(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metadata/ads" {
			t.Error(r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"metadata":{"title":"Era commercials","licenseurl":"https://creativecommons.org/licenses/by/4.0/"},"files":[{"name":"original.avi","format":"AVI"},{"name":"large.mp4","format":"MPEG4"},{"name":"best.mp4","format":"h.264"}]}`))
	}))
	defer fake.Close()
	raw, title, attr, e := resolveArchive(context.Background(), fake.Client(), fake.URL, "ads")
	if e != nil || !strings.HasSuffix(raw, "/download/ads/best.mp4") || title != "Era commercials" || !strings.Contains(attr, "creativecommons.org") {
		t.Fatal(raw, title, attr, e)
	}
}
func TestImportSizeCaps(t *testing.T) {
	for _, total := range []int64{-1, 100} {
		var out strings.Builder
		_, e := copyImport(&out, strings.NewReader(strings.Repeat("x", 100)), 32, total, func(float64) {})
		if e == nil || out.Len() > 32 {
			t.Fatal("size cap not enforced", total, out.Len(), e)
		}
	}
}

func TestRedirectIsRefusedBeforePrivateRequest(t *testing.T) {
	reached := false
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer private.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, private.URL, 302) }))
	defer source.Close()
	c := importClient()
	defer c.CloseIdleConnections()
	// Only the first hop is an admitted fake. Redirect policy stays production.
	c.Transport = source.Client().Transport
	if _, e := c.Get(source.URL); e == nil || reached {
		t.Fatal("followed private redirect", e, reached)
	}
}

func TestImportedClipDurationCap(t *testing.T) {
	s := testService(t, Options{FFmpeg: func() string { return "unused" }, Probe: func(context.Context, string) (*core.MediaInfo, error) {
		return &core.MediaInfo{Duration: 3*time.Minute + time.Second}, nil
	}})
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "long")
	if e := os.WriteFile(src, []byte("media"), 0o600); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256([]byte("media"))
	a, e := s.putReelAsset(ctx, "long", src, hex.EncodeToString(sum[:]), 5)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.convertClip(ctx, "not-needed", ReelClip{ID: "cap-test", SourceDigest: a.Digest}); e == nil || !strings.Contains(e.Error(), "3 minutes") {
		t.Fatal("duration cap bypassed", e)
	}
}

func TestImportsStartWhileRenderLaneBlocked(t *testing.T) {
	blocked := make(chan struct{})
	release := make(chan struct{})
	s := testService(t, Options{})
	defer close(release)
	if !s.enqueue(renderJob{Hash: "blocked-render", convert: func(context.Context) error { close(blocked); <-release; return nil }}) {
		t.Fatal("render enqueue")
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("render did not start")
	}
	started := make(chan struct{}, 2)
	for _, hash := range []string{"download-one", "download-two"} {
		if !s.enqueue(renderJob{download: true, Hash: hash, prepare: func(context.Context) error { started <- struct{}{}; return nil }}) {
			t.Fatal("import enqueue")
		}
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("render blocked import download start")
		}
	}
}

func TestExtractorImportProvenanceAndCancel(t *testing.T) {
	ctx := context.Background()
	dlStarted := make(chan struct{}, 1)
	dlBlocked := make(chan struct{})

	dl := extras.NewDownloader(extras.DownloaderOptions{
		Config:   func() config.Extras { return config.Extras{ResolverCommand: "yt-dlp"} },
		LookPath: func(cmd string) (string, error) { return cmd, nil },
		Runner: func(rctx context.Context, name string, args []string) ([]byte, error) {
			var outTemplate, targetURL string
			for i := 0; i < len(args); i++ {
				if args[i] == "-o" && i+1 < len(args) {
					outTemplate = args[i+1]
				}
				if strings.HasPrefix(args[i], "http") {
					targetURL = args[i]
				}
			}
			if strings.Contains(targetURL, "slow-cancel") {
				select {
				case dlStarted <- struct{}{}:
				default:
				}
				select {
				case <-rctx.Done():
					return nil, rctx.Err()
				case <-dlBlocked:
				}
			}
			path := strings.ReplaceAll(outTemplate, "%(ext)s", "mp4")
			if err := os.WriteFile(path, []byte("fake-mp4-video-payload"), 0o600); err != nil {
				return nil, err
			}
			return []byte("[download]  65.0% of 1.00MiB at 1.00MiB/s ETA 00:01\n" +
				`{"_type":"video","title":"Classic Station ID 1987","uploader":"Broadcast Archives","duration":12.0}` + "\n"), nil
		},
	})

	s := testService(t, Options{
		Downloader: dl,
		Probe: func(context.Context, string) (*core.MediaInfo, error) {
			return &core.MediaInfo{
				Duration: 12 * time.Second,
				Streams:  []core.StreamInfo{{Type: "video", Width: 1280, Height: 720}},
			}, nil
		},
	})
	defer close(dlBlocked)

	// 1. Library video import via extractor records title, attribution and source URL.
	seedJob := ImportJob{
		UseSourceTitle: true,
		ID:             "lib-import-1",
		Source:         "https://www.youtube.com/watch?v=classic87",
		Name:           "youtube.com · classic87",
		Kind:           "video",
		Status:         "downloading",
	}
	rawSeed, _ := json.Marshal(seedJob)
	if _, err := s.db.ExecContext(ctx, "INSERT INTO onair_imports(id,reel_id,data) VALUES(?,?,?)", seedJob.ID, "", string(rawSeed)); err != nil {
		t.Fatal(err)
	}
	if err := s.runExtractorImport(ctx, http.DefaultClient, "", seedJob, seedJob.Source, "video", assetLimit, 30*time.Second); err != nil {
		t.Fatalf("runExtractorImport: %v", err)
	}
	assets, err := s.Assets(ctx)
	if err != nil {
		t.Fatalf("Assets: %v", err)
	}
	var imported *Asset
	for i := range assets {
		if assets[i].Source == "https://www.youtube.com/watch?v=classic87" {
			imported = &assets[i]
			break
		}
	}
	if imported == nil {
		t.Fatalf("imported library asset not found in %+v", assets)
	}
	if imported.Name != "Classic Station ID 1987" || !strings.Contains(imported.Attribution, "Broadcast Archives") {
		t.Fatalf("unexpected imported asset provenance: %+v", *imported)
	}

	// 2. Cancelling an in-flight reel import via DeleteClip cancels the running context and removes the job.
	reel, err := s.SaveReel(ctx, Reel{Name: "Test Reel", Order: "sequence"})
	if err != nil {
		t.Fatalf("SaveReel: %v", err)
	}
	slowJob := ImportJob{
		UseSourceTitle: true,
		ID:             "slow-import-1",
		Source:         "https://www.youtube.com/watch?v=slow-cancel",
		Name:           "youtube.com · slow-cancel",
		Status:         "downloading",
	}
	rawSlow, _ := json.Marshal(slowJob)
	if _, err := s.db.ExecContext(ctx, "INSERT INTO onair_imports(id,reel_id,data) VALUES(?,?,?)", slowJob.ID, reel.ID, string(rawSlow)); err != nil {
		t.Fatal(err)
	}
	jobCtx, jobCancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.importCancels[slowJob.ID] = jobCancel
	s.mu.Unlock()

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- s.runExtractorImport(jobCtx, http.DefaultClient, reel.ID, slowJob, slowJob.Source, "video", 500<<20, 3*time.Minute)
	}()

	select {
	case <-dlStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slow download did not start")
	}

	if err := s.DeleteClip(ctx, reel.ID, slowJob.ID); err != nil {
		t.Fatalf("DeleteClip cancel: %v", err)
	}

	select {
	case runErr := <-doneCh:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled download did not exit")
	}

	afterReel, err := s.Reel(ctx, reel.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range afterReel.Clips {
		if c.ID == slowJob.ID {
			t.Fatalf("cancelled clip still present in reel: %+v", c)
		}
	}
}

func TestDownloaderHandlerRoutes(t *testing.T) {
	fakeBinary := []byte("#!/bin/sh\necho yt-dlp\n")
	sum := sha256.Sum256(fakeBinary)
	hexSum := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest/download/SHA2-256SUMS":
			http.Redirect(w, r, "/download/2099.01.01/SHA2-256SUMS", http.StatusFound)
		case "/download/2099.01.01/SHA2-256SUMS":
			_, _ = w.Write([]byte(hexSum + "  yt-dlp_macos\n"))
		case "/download/2099.01.01/yt-dlp_macos":
			_, _ = w.Write(fakeBinary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dl := extras.NewDownloader(extras.DownloaderOptions{
		ToolsDir:   t.TempDir(),
		Config:     func() config.Extras { return config.Extras{} },
		LookPath:   func(string) (string, error) { return "", errors.New("missing") },
		ReleaseURL: srv.URL,
		Client:     srv.Client(),
		GOOS:       "darwin",
		GOARCH:     "arm64",
	})
	s := testService(t, Options{Downloader: dl})
	h := s.Handler()

	// GET /downloader
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/downloader", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /downloader status = %d: %s", rec.Code, rec.Body.String())
	}
	var st extras.DownloaderStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Available || !st.CanInstall {
		t.Fatalf("unexpected status: %+v", st)
	}

	// POST /downloader/install (update=true to use the test server's 2099.01.01 tag)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/downloader/install", bytes.NewReader([]byte(`{"update":true}`)))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /downloader/install status = %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Available || st.Source != "managed" || st.Version != "2099.01.01" {
		t.Fatalf("unexpected post-install status: %+v", st)
	}
}
