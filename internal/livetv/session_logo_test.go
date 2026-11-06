package livetv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testLogoFetcher struct {
	url  string
	fail bool
}

func (f *testLogoFetcher) GetUntrustedURL(_ context.Context, url string) ([]byte, string, error) {
	f.url = url
	if f.fail {
		return nil, "", errors.New("failed")
	}
	return []byte("logo"), "image/png", nil
}

func TestOwnerChannelLogo(t *testing.T) {
	s := testService(t, Options{})
	c := testChannel()
	c.Logo = "https://images.example/logo.svg?secret=credential"
	if _, err := s.SaveChannel(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	fetch := &testLogoFetcher{}
	h := s.ownerHandler(fetch)
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		return r
	}
	r := get("/channels/test/logo?url=https://attacker.invalid/")
	if r.Code != 200 || r.Body.String() != "logo" || fetch.url != c.Logo || r.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(r.Header().Get("Content-Security-Policy"), "sandbox") || r.Header().Get("Cache-Control") != "private, max-age=300" {
		t.Fatalf("response %d %q %v, fetched %q", r.Code, r.Body, r.Header(), fetch.url)
	}
	if r := get("/channels/missing/logo"); r.Code != 404 {
		t.Fatalf("missing: %d", r.Code)
	}
	fetch.fail = true
	if r := get("/channels/test/logo"); r.Code != 404 || strings.Contains(r.Body.String(), "credential") {
		t.Fatalf("failure: %d %s", r.Code, r.Body)
	}
}

func TestOwnerChannelLogoBlocksPrivateAddress(t *testing.T) {
	s := testService(t, Options{})
	c := testChannel()
	c.Logo = "http://127.0.0.1:1/logo.png"
	if _, err := s.SaveChannel(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	s.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/channels/test/logo", nil))
	if r.Code != 404 {
		t.Fatalf("private address: %d", r.Code)
	}
}
