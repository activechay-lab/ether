package onair

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"net/http"
	"net/http/httptest"
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
	a, e := s.putReelAsset(ctx, "long", []byte("media"))
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
