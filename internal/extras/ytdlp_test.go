package extras

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

func TestDownloaderInstallVerifyAndTamperReject(t *testing.T) {
	fakeBinary := []byte("#!/bin/sh\necho yt-dlp\n")
	sumBytes := sha256.Sum256(fakeBinary)
	fakeSum := hex.EncodeToString(sumBytes[:])

	origSpec := pinnedYTDLPAssets["darwin/arm64"]
	pinnedYTDLPAssets["darwin/arm64"] = ytdlpPlatformAsset{
		Asset:  "yt-dlp_macos",
		Binary: "yt-dlp",
		SHA256: fakeSum,
	}
	defer func() {
		pinnedYTDLPAssets["darwin/arm64"] = origSpec
	}()

	updatedBinary := []byte("#!/bin/sh\necho yt-dlp-2026.11.01\n")
	updatedSumBytes := sha256.Sum256(updatedBinary)
	updatedSum := hex.EncodeToString(updatedSumBytes[:])

	var serveCorrupt bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/" + PinnedYTDLPVersion + "/SHA2-256SUMS":
			_, _ = fmt.Fprintf(w, "%s  yt-dlp_macos\n", fakeSum)
		case "/download/" + PinnedYTDLPVersion + "/yt-dlp_macos":
			if serveCorrupt {
				_, _ = w.Write([]byte("corrupted-binary-payload"))
				return
			}
			_, _ = w.Write(fakeBinary)
		case "/latest/download/SHA2-256SUMS":
			http.Redirect(w, r, "/download/2026.11.01/SHA2-256SUMS", http.StatusFound)
		case "/download/2026.11.01/SHA2-256SUMS":
			_, _ = fmt.Fprintf(w, "%s  yt-dlp_macos\n", updatedSum)
		case "/download/2026.11.01/yt-dlp_macos":
			_, _ = w.Write(updatedBinary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	toolsDir := t.TempDir()
	dl := NewDownloader(DownloaderOptions{
		ToolsDir:   toolsDir,
		Config:     func() config.Extras { return config.Extras{Enabled: true} },
		LookPath:   func(string) (string, error) { return "", errors.New("not on PATH") },
		ReleaseURL: srv.URL,
		Client:     srv.Client(),
		GOOS:       "darwin",
		GOARCH:     "arm64",
	})

	st := dl.Status()
	if st.Available || st.Source != "none" || !st.CanInstall {
		t.Fatalf("initial status = %+v, want unavailable/none/canInstall", st)
	}

	// Corrupt download must be rejected and never written to the final path.
	serveCorrupt = true
	if _, err := dl.Install(context.Background(), false); err == nil || !strings.Contains(err.Error(), "SHA-256 verification failed") {
		t.Fatalf("corrupt install err = %v, want SHA-256 verification failure", err)
	}
	if _, _, ok := dl.VerifiedManagedBinary(); ok {
		t.Fatal("corrupt binary must not be marked verified")
	}

	// Valid install succeeds and verifies.
	serveCorrupt = false
	st, err := dl.Install(context.Background(), false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !st.Available || st.Source != "managed" || st.Version != PinnedYTDLPVersion || !st.CanUpdate {
		t.Fatalf("post-install status = %+v", st)
	}

	// Extras Service shares the managed binary when ResolverCommand is blank.
	svc := New(Options{
		Config:     func() config.Extras { return config.Extras{Enabled: true} },
		LookPath:   func(string) (string, error) { return "", errors.New("not on PATH") },
		Downloader: dl,
	})
	if !svc.CommandAvailable() {
		t.Fatal("expected extras service CommandAvailable() == true with verified managed binary")
	}

	// Tampering with the binary on disk invalidates verification immediately.
	binPath := filepath.Join(toolsDir, "yt-dlp", "yt-dlp")
	if err := os.WriteFile(binPath, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := dl.VerifiedManagedBinary(); ok {
		t.Fatal("tampered binary must not pass verification")
	}
	if svc.CommandAvailable() {
		t.Fatal("tampered binary must not be reported available")
	}

	// Update replaces with the latest verified release.
	st, err = dl.Install(context.Background(), true)
	if err != nil {
		t.Fatalf("Install(update): %v", err)
	}
	if !st.Available || st.Version != "2026.11.01" {
		t.Fatalf("post-update status = %+v, want version 2026.11.01", st)
	}
}

func TestDownloaderDownloadMetadataErrorsAndRedaction(t *testing.T) {
	var progressValues []float64
	dl := NewDownloader(DownloaderOptions{
		Config:   func() config.Extras { return config.Extras{ResolverCommand: "/usr/local/bin/yt-dlp"} },
		LookPath: func(cmd string) (string, error) { return cmd, nil },
		Runner: func(ctx context.Context, name string, args []string) ([]byte, error) {
			var outTemplate string
			for i := 0; i+1 < len(args); i++ {
				if args[i] == "-o" {
					outTemplate = args[i+1]
				}
			}
			target := strings.ReplaceAll(outTemplate, "%(ext)s", "mp4")
			if err := os.WriteFile(target, []byte("fake-mp4-bytes"), 0o600); err != nil {
				return nil, err
			}
			out := "[download]  42.5% of 1.20MiB at 2.00MiB/s ETA 00:01\n" +
				`{"_type":"video","title":"1984 Soda Commercial","uploader":"Retro Vault","duration":29.5}` + "\n"
			return []byte(out), nil
		},
	})

	outDir := t.TempDir()
	res, err := dl.Download(context.Background(), DownloadRequest{
		URL:         "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Kind:        "video",
		Reel:        true,
		MaxBytes:    500 << 20,
		MaxDuration: 3 * time.Minute,
		OutputDir:   outDir,
		Progress: func(pct float64) {
			progressValues = append(progressValues, pct)
		},
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if res.Title != "1984 Soda Commercial" || res.Attribution != "Retro Vault" {
		t.Fatalf("unexpected provenance: %+v", res)
	}
	if res.Path == "" {
		t.Fatal("expected downloaded file path")
	}
	if len(progressValues) == 0 || progressValues[0] != 42.5 {
		t.Fatalf("expected progress callback 42.5, got %v", progressValues)
	}

	// Playlist URL rejected before running command.
	if _, err := dl.Download(context.Background(), DownloadRequest{
		URL:         "https://www.youtube.com/playlist?list=PL12345",
		Kind:        "video",
		Reel:        true,
		MaxBytes:    500 << 20,
		MaxDuration: 3 * time.Minute,
		OutputDir:   t.TempDir(),
	}); err == nil || !strings.Contains(err.Error(), "playlists are not supported") {
		t.Fatalf("expected playlist error, got %v", err)
	}

	// Age-gated classification.
	dlAge := NewDownloader(DownloaderOptions{
		Config:   func() config.Extras { return config.Extras{ResolverCommand: "yt-dlp"} },
		LookPath: func(cmd string) (string, error) { return cmd, nil },
		Runner: func(ctx context.Context, name string, args []string) ([]byte, error) {
			return []byte("ERROR: [youtube] abc: Sign in to confirm your age. This video may be inappropriate for some users."), errors.New("exit status 1")
		},
	})
	if _, err := dlAge.Download(context.Background(), DownloadRequest{
		URL:         "https://www.youtube.com/watch?v=abc",
		Kind:        "video",
		Reel:        true,
		MaxBytes:    500 << 20,
		MaxDuration: 3 * time.Minute,
		OutputDir:   t.TempDir(),
	}); err == nil || !strings.Contains(err.Error(), "age-restricted") {
		t.Fatalf("expected age-restricted error, got %v", err)
	}

	// Secret/token/query redaction in unknown error output.
	sanitized := sanitizeCommandOutput("ERROR: failed fetching https://cdn.example.com/v.mp4?token=secret123&sig=abcd Cookie: session_id=xyz987")
	if strings.Contains(sanitized, "secret123") || strings.Contains(sanitized, "xyz987") || strings.Contains(sanitized, "abcd") {
		t.Fatalf("expected tokens and query params to be redacted, got %q", sanitized)
	}
}
