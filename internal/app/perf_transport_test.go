package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// A barrier, not a slow handler: synchronous trace settlement prevents HTTP
// completion. The gate's asynchronous mode must deliver the response while
// still joining and charging all retained work before reading the trace.
func TestPerfObserverSettlementTransport(t *testing.T) {
	for _, async := range []bool{false, true} {
		name := "synchronous"
		if async {
			name = "gate-async"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "trace.jsonl")
			t.Setenv(PerfTraceEnv, path)
			db, err := store.OpenPath(t.Context(), filepath.Join(t.TempDir(), "work.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			p := newPerfObserver(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
			defer store.CaptureCallSites(false)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			finish := func() { once.Do(func() { close(release) }) }
			defer p.close()
			defer finish()
			p.asyncTrace = async
			p.settleTrace = func(ctx context.Context) {
				close(entered)
				<-release
				var one int
				if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
					t.Error(err)
				}
			}
			srv := httptest.NewServer(p.wrap("plex", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })))
			defer srv.Close()
			// Unblock settlement before server.Close even on a failed assertion.
			defer finish()
			client := srv.Client()
			client.Timeout = 250 * time.Millisecond
			defer client.CloseIdleConnections()
			done := make(chan error, 1)
			var body []byte
			var status int
			go func() {
				req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
				req.Header.Set(PerfTagHeader, "transport")
				resp, err := client.Do(req)
				if err == nil {
					body, err = io.ReadAll(resp.Body)
					status = resp.StatusCode
					resp.Body.Close()
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("settle hook never entered")
			}
			clientErr := <-done
			err = clientErr
			if async && err != nil {
				t.Fatalf("settlement delayed response: %v", err)
			}
			if !async && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("want client deadline behind synchronous barrier, got %v", err)
			}
			if async && (status != 200 || string(body) != "ok") {
				t.Fatalf("response changed: %d %q", status, body)
			}
			finish()
			// server.Close joins the synchronous handler; waitTraces joins async work.
			srv.Close()
			p.close()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var rec PerfTraceRecord
			if err = json.Unmarshal(data, &rec); err != nil {
				t.Fatal(err)
			}
			if rec.Queries != 1 || rec.Rows != 1 || rec.Status != 200 {
				t.Fatalf("retained work lost: %+v", rec)
			}
			t.Logf("async=%v client_error=%v trace_queries=%d rows=%d", async, clientErr, rec.Queries, rec.Rows)
		})
	}
}
