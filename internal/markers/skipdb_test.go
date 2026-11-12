package markers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchSkipDBMapsSegmentsAndDropsOutOfRange(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		io.WriteString(w, `{"segments":{"intro":{"start_ms":61000,"end_ms":91000,"match":"exact"},
			"recap":null,
			"outro":{"start_ms":1100000,"match":"shifted"},
			"preview":{"start_ms":2000000,"end_ms":2100000}}}`)
	}))
	defer srv.Close()
	d := 20 * time.Minute
	got, err := FetchSkipDB(context.Background(), srv.URL, "", "tt0903747", 1, 1, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"imdb_id=tt0903747", "season=1", "episode=1", "duration=1200"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("query %q missing %q", raw, want)
		}
	}
	// preview (2000s) is past the 1200s runtime and must be dropped; the
	// outro has no end so it runs to the played duration and is final.
	if len(got) != 2 {
		t.Fatalf("markers %+v", got)
	}
	byType := map[string]Marker{}
	for _, m := range got {
		byType[m.Type] = m
	}
	if m := byType["intro"]; m.StartMS != 61000 || m.EndMS != 91000 || m.Source != SkipDBSource {
		t.Fatalf("intro %+v", m)
	}
	if m := byType["credits"]; m.EndMS != d.Milliseconds() || !m.Final {
		t.Fatalf("outro %+v", m)
	}
}

func TestFetchSkipDBEmptyAndAbsentSegments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"segments":{"intro":null,"recap":{"start_ms":0,"end_ms":0},"outro":null,"preview":null}}`)
	}))
	defer srv.Close()
	got, err := FetchSkipDB(context.Background(), srv.URL, "", "tt1", 1, 1, 10*time.Minute)
	if err != nil || len(got) != 0 {
		t.Fatalf("sentinel ranges became markers: %+v %v", got, err)
	}
	// No IMDb id is a quiet no-op, never a request.
	if got, err := FetchSkipDB(context.Background(), srv.URL, "", "", 0, 0, time.Minute); err != nil || got != nil {
		t.Fatalf("no imdb: %+v %v", got, err)
	}
}

func TestProbeSkipDBSendsKeyAndAcceptsNotFound(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if strings.Contains(gotAuth, "bad") {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if err := ProbeSkipDB(context.Background(), srv.URL, ""); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Fatalf("public probe sent auth %q", gotAuth)
	}
	if err := ProbeSkipDB(context.Background(), srv.URL, "good"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer good" {
		t.Fatalf("auth %q", gotAuth)
	}
	if err := ProbeSkipDB(context.Background(), srv.URL, "bad"); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("bad key: %v", err)
	}
}
