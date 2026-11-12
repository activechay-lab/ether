package mdblist

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type testSealer struct{}

func (testSealer) SealField(p string, parts ...string) (string, error) {
	return "ether-sealed:v2:" + strings.Join(parts, "|") + ":" + p, nil
}
func (testSealer) OpenField(p string, parts ...string) (string, bool, error) {
	prefix := "ether-sealed:v2:" + strings.Join(parts, "|") + ":"
	if !strings.HasPrefix(p, prefix) {
		return "", false, errors.New("wrong context")
	}
	return strings.TrimPrefix(p, prefix), false, nil
}

func TestHistoryPayload(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tc := range []struct {
		key  core.RatingKey
		want []string
	}{
		{core.MovieKey(123), []string{`"movies"`, `"tmdb":123`, `"watched_at":"2026-01-02T03:04:05Z"`}},
		{core.EpisodeKey(456, 2, 3), []string{`"shows"`, `"tmdb":456`, `"number":2`, `"number":3`, `"watched_at":"2026-01-02T03:04:05Z"`}},
	} {
		payload, err := HistoryPayload(tc.key, at)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(payload)
		for _, want := range tc.want {
			if !strings.Contains(string(b), want) {
				t.Errorf("missing %s in %s", want, b)
			}
		}
	}
	if _, err := HistoryPayload(core.ShowKey(456), at); err == nil {
		t.Fatal("accepted show")
	}
	if _, err := HistoryPayload(core.MovieKey(123), time.Time{}); err == nil {
		t.Fatal("accepted empty time")
	}
}

// TestPushHistoryAdapter checks the wire contract: the API key travels as the
// apikey query parameter, the body carries the TMDB identity and watched_at,
// and a not_found response is reported as ErrUnsupported.
func TestPushHistoryAdapter(t *testing.T) {
	var gotKey, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("apikey")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"updated":{"movies":1},"not_found":null}`))
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := c.PushHistory(context.Background(), "secret-key", core.MovieKey(272), at); err != nil {
		t.Fatal(err)
	}
	if gotKey != "secret-key" || gotPath != "/sync/watched" {
		t.Fatalf("key=%q path=%q", gotKey, gotPath)
	}
	movies, _ := gotBody["movies"].([]any)
	if len(movies) != 1 {
		t.Fatalf("body %v", gotBody)
	}
	m := movies[0].(map[string]any)
	if m["watched_at"] != "2026-01-02T03:04:05Z" {
		t.Fatalf("watched_at %v", m["watched_at"])
	}
	if ids, _ := m["ids"].(map[string]any); ids["tmdb"] != float64(272) {
		t.Fatalf("ids %v", m["ids"])
	}
}

func TestPushHistoryNotFoundIsUnsupported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"not_found":{"movies":[{"ids":{"tmdb":1}}]}}`))
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL}
	err := c.PushHistory(context.Background(), "k", core.MovieKey(1), time.Now())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestPushHistoryRateLimitExposesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL}
	err := c.PushHistory(context.Background(), "k", core.MovieKey(1), time.Now())
	var h *HTTPError
	if !errors.As(err, &h) || h.Status != http.StatusTooManyRequests || h.RetryAfter != "120" {
		t.Fatalf("want 429 with Retry-After, got %v", err)
	}
}

func TestParseHistory(t *testing.T) {
	data := []byte(`{
		"movies":[{"watched_at":"2026-01-02T03:04:05Z","movie":{"ids":{"tmdb":272}}}],
		"episodes":[{"last_watched_at":"2026-02-03T04:05:06Z","episode":{"season":2,"number":3,"show":{"ids":{"tmdb":456}}}}],
		"shows":[{"last_watched_at":"2026-02-03T04:05:06Z","show":{"ids":{"tmdb":456}}}]
	}`)
	records, skipped, err := ParseHistory(7, data)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 || len(records) != 2 {
		t.Fatalf("records=%d skipped=%d", len(records), skipped)
	}
	if records[0].Key != core.MovieKey(272) || records[0].UserID != 7 {
		t.Fatalf("movie %+v", records[0])
	}
	if records[1].Key != core.EpisodeKey(456, 2, 3) {
		t.Fatalf("episode %+v", records[1])
	}
}
