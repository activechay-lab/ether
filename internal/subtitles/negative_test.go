package subtitles

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNegativeSearchIsolationExpiryAndErrors(t *testing.T) {
	calls := 0
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if fail {
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()
	now := time.Now()
	s := New(Options{BaseURL: server.URL, Now: func() time.Time { return now }, NegativeTTL: time.Hour})
	search := func(id int64, lang string) {
		t.Helper()
		_, err := s.Search(context.Background(), &core.Title{Key: core.MovieKey(id)}, lang, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	search(1, "en")
	search(1, "en")
	search(1, "fr")
	search(2, "en")
	if calls != 3 {
		t.Fatalf("isolation: calls=%d", calls)
	}
	now = now.Add(time.Hour)
	search(1, "en")
	if calls != 4 {
		t.Fatal("miss did not expire")
	}
	fail = true
	for range 2 {
		if _, err := s.Search(context.Background(), &core.Title{Key: core.MovieKey(3)}, "en", ""); err == nil {
			t.Fatal("expected error")
		}
	}
	if calls != 6 {
		t.Fatal("transport error was cached")
	}
	fail = false
	clear(s.negative)
	for n := range 4096 {
		s.negative[fmt.Sprintf("seed-%d", n)] = now.Add(time.Hour)
	}
	search(4, "en")
	if len(s.negative) > 4096 {
		t.Fatal("cache exceeded bound")
	}
}
