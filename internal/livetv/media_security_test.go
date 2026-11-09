package livetv

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Owned resources are init sections and keys Ether fetched itself; the
// preview route still serves them sandboxed, whatever bytes they hold.
func TestOwnerPreviewResourceIsolatesActiveContent(t *testing.T) {
	s := testService(t, Options{})
	c, err := s.SaveChannel(context.Background(), Channel{ID: "security", Number: "1", Name: "Security", Kind: "iptv", Enabled: true, StreamURL: "http://example.invalid/index"})
	if err != nil {
		t.Fatal(err)
	}
	tl := newOwnedTimeline("security", s.logger(), nil, 0, nil)
	tl.addResource("html", []byte(`<script>bad()</script>`), "video/mp4")
	tl.addResource("svg", []byte(`<svg onload="bad()"/>`), "application/octet-stream")
	s.sessions["security"] = &session{caps: core.ClientCaps{Product: previewCaps.Product, ClientID: "ether-preview-security"}, user: core.User{IsOwner: true}, ready: true, info: SessionInfo{ID: "security", UserID: 0, Channel: c}, inputURL: c.StreamURL, owned: &ownedLease{tl: tl, release: func() {}}}
	for _, key := range []string{"html", "svg"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/security/resource/"+key, nil))
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") || w.Header().Get("X-Content-Type-Options") != "nosniff" || strings.Contains(w.Header().Get("Content-Type"), "html") {
			t.Fatalf("%s status=%d headers=%v body=%q", key, w.Code, w.Header(), w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/security/resource/"+stableID("http://example.invalid/anything"), nil))
	if w.Code != 404 {
		t.Fatalf("an unregistered resource was served: %d", w.Code)
	}
}
