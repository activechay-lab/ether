package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestNaturalSearchAnswersLocallyWhileModelRuns(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"titles\":[{\"title\":\"Model pick\",\"type\":\"movie\"}]}"}}]}`))
	}))
	defer server.Close()
	local := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "Local"}
	model := core.Title{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "Model pick"}
	cat := fakeCatalog{byTitle: map[string][]core.Title{"request": {local}, "model pick": {model}}}
	s := New(ServiceOptions{Client: NewClient(ClientOptions{Config: config.LLM{Enabled: true, BaseURL: server.URL, Model: "test"}}), Catalog: cat, CallTimeout: time.Second})
	start := time.Now()
	got, err := s.NaturalSearch(context.Background(), &core.User{ID: 1}, "request", 10)
	elapsed := time.Since(start)
	close(release)
	if err != nil || len(got) != 1 || got[0].Key != local.Key {
		t.Fatalf("local answer %v %v", got, err)
	}
	if elapsed >= 100*time.Millisecond {
		t.Fatalf("waited on model %s", elapsed)
	}
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.flights) == 0 })
	got, err = s.NaturalSearch(context.Background(), &core.User{ID: 1}, "request", 10)
	if err != nil || len(got) != 1 || got[0].Key != model.Key {
		t.Fatalf("cached suggestions lost %v %v", got, err)
	}
}
