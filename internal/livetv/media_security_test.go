package livetv

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOwnerPreviewResourceIsolatesActiveContent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/html" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<script>bad()</script>`)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		fmt.Fprint(w, `<svg onload="bad()"/>`)
	}))
	defer upstream.Close()
	s := testService(t, Options{HTTPClient: upstream.Client()})
	c, err := s.SaveChannel(context.Background(), Channel{ID: "security", Number: "1", Name: "Security", Kind: "iptv", Enabled: true, StreamURL: upstream.URL + "/index"})
	if err != nil {
		t.Fatal(err)
	}
	s.sessions["security"] = &session{caps: core.ClientCaps{Product: previewCaps.Product, ClientID: "ether-preview-security"}, user: core.User{IsOwner: true}, ready: true, info: SessionInfo{ID: "security", UserID: 0, Channel: c}, inputURL: c.StreamURL, resources: map[string]string{"html": upstream.URL + "/html", "svg": upstream.URL + "/svg"}}
	for _, key := range []string{"html", "svg"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/sessions/security/resource/"+key, nil))
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s status=%d headers=%v body=%q", key, w.Code, w.Header(), w.Body.String())
		}
	}
}
