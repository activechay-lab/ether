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

func TestResourceCredentialOriginAndRedirect(t *testing.T) {
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Provider-Secret") != "" || r.Header.Get("Referer") != "" {
			t.Error("source headers or referring URL leaked to foreign origin")
		}
		_, _ = w.Write([]byte("public CDN segment"))
	}))
	defer foreign.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-Provider-Secret") != "secret" {
			t.Error("source headers missing")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, foreign.URL, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("origin"))
	}))
	defer origin.Close()
	s := testService(t, Options{HTTPClient: origin.Client()})
	headers := http.Header{"Authorization": {"Bearer secret"}, "X-Provider-Secret": {"secret"}}
	for _, target := range []string{origin.URL, origin.URL + "/redirect?token=secret", foreign.URL} {
		res, err := s.upstreamWithHeaders(context.Background(), target, httptest.NewRequest("GET", "/", nil), headers, origin.URL)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d", target, res.StatusCode)
		}
	}
}

func TestPlaylistForeignResourceDoesNotReceiveSourceHeaders(t *testing.T) {
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Provider-Secret") != "" {
			t.Error("foreign resource received source headers")
		}
		_, _ = w.Write([]byte("public segment"))
	}))
	defer foreign.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-Provider-Secret") != "secret" {
			t.Error("source missing headers")
		}
		fmt.Fprintf(w, "#EXTM3U\n#EXTINF:4,\n%s/segment\n", foreign.URL)
	}))
	defer origin.Close()
	s := testService(t, Options{HTTPClient: origin.Client()})
	c, err := s.SaveChannel(context.Background(), Channel{ID: "scope", Number: "1", Name: "scope", Kind: "iptv", Enabled: true, StreamURL: origin.URL + "/index"})
	if err != nil {
		t.Fatal(err)
	}
	ss := &session{ready: true, info: SessionInfo{ID: "scope", UserID: 1, Channel: c}, inputURL: c.StreamURL, inputSource: StreamSource{ID: "primary", URL: c.StreamURL, Enabled: true}, inputHeaders: http.Header{"Authorization": {"Bearer secret"}, "X-Provider-Secret": {"secret"}}, ctx: t.Context()}
	ss.owned = s.acquirePassthrough(ss)
	t.Cleanup(ss.owned.close)
	s.sessions["scope"] = ss
	req := httptest.NewRequest("GET", "/", nil)
	user := &core.User{ID: 1}
	playlist := httptest.NewRecorder()
	s.ServePlaylist(playlist, req, "scope", user)
	if playlist.Code != 200 || !strings.Contains(playlist.Body.String(), "/relay/0.ts") || strings.Contains(playlist.Body.String(), foreign.URL) {
		t.Fatalf("playlist: %d %s", playlist.Code, playlist.Body.String())
	}
	segment := httptest.NewRecorder()
	s.ServeRelaySegment(segment, req, "scope", 0, user)
	if segment.Code != 200 || segment.Body.String() != "public segment" {
		t.Fatalf("segment: %d %s", segment.Code, segment.Body.String())
	}
}

func TestPublicCDNHTTPRedirectStillWorks(t *testing.T) {
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Provider-Secret") != "" {
			t.Error("source headers reached HTTP CDN")
		}
		_, _ = w.Write([]byte("public"))
	}))
	defer cdn.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, cdn.URL, http.StatusFound) }))
	defer origin.Close()
	s := testService(t, Options{HTTPClient: origin.Client()})
	res, err := s.upstreamWithHeaders(context.Background(), origin.URL, httptest.NewRequest("GET", "/", nil), http.Header{"Authorization": {"secret"}, "X-Provider-Secret": {"secret"}}, origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
}
