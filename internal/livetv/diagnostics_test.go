package livetv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type diagnosticBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *diagnosticBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}
func (b *diagnosticBuffer) text() string { b.mu.Lock(); defer b.mu.Unlock(); return b.Buffer.String() }

func TestLiveDiagnosticsRetainProviderFailureAndReleaseResources(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int64
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "SECRET_PROVIDER_BODY", status)
			}))
			defer up.Close()
			var logs diagnosticBuffer
			s := testService(t, Options{HTTPClient: up.Client(), Logger: slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
			ctx := context.Background()
			_, err := s.ImportSource(ctx, Source{ID: "provider", Name: "Provider", MaxConnections: 1, Playlist: "#EXTM3U\n#EXTINF:-1,Test\n" + up.URL + "/SECRET_USER/SECRET_PASSWORD/1.ts?token=SECRET_TOKEN\n"})
			if err != nil {
				t.Fatal(err)
			}
			channels, _ := s.Channels(ctx)
			_, err = s.Tune(ctx, &core.User{ID: 1}, channels[0].ID, core.ClientCaps{ClientID: "test-tv"})
			var detail *TuneError
			if !errors.As(err, &detail) || detail.UpstreamStatus != status || !errors.Is(err, core.ErrNotAvailable) {
				t.Fatalf("lost upstream evidence: %#v", err)
			}
			if requests.Load() != 1 || len(s.ViewingSessions()) != 0 || s.inputRelay.taps.open("provider") != 0 {
				t.Fatal("failed tune leaked resources or retried a capped provider")
			}
			text := logs.text()
			for _, secret := range []string{"SECRET_USER", "SECRET_PASSWORD", "SECRET_TOKEN", "SECRET_PROVIDER_BODY", up.URL} {
				if strings.Contains(text, secret) || strings.Contains(err.Error(), secret) {
					t.Fatalf("credential exposed: %s", secret)
				}
			}
			var sessionID string
			seen := map[string]bool{}
			for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
				var record map[string]any
				if json.Unmarshal([]byte(line), &record) != nil {
					t.Fatal("invalid diagnostic record")
				}
				msg, _ := record["msg"].(string)
				seen[msg] = true
				if id, ok := record["session"].(string); ok {
					if sessionID == "" {
						sessionID = id
					}
					if id != sessionID {
						t.Fatal("lost tune correlation")
					}
				}
				if msg == "live tune failed" && record["upstream_status"] != float64(status) {
					t.Fatal("final tune event lost provider status")
				}
			}
			for _, msg := range []string{"live tune started", "live source attempt failed", "live session resources released", "live tune failed"} {
				if !seen[msg] {
					t.Fatalf("missing %s", msg)
				}
			}
			before := requests.Load()
			_, err = s.Tune(ctx, &core.User{ID: 1}, channels[0].ID, core.ClientCaps{ClientID: "test-tv"})
			if Diagnose("tune", err).Code != "source_cooldown" || requests.Load() != before {
				t.Fatal("cooldown did not explain refusal without opening another provider connection")
			}
		})
	}
}

func TestLiveDiagnosticsNeverPrintTransportErrors(t *testing.T) {
	inner := &net.DNSError{Name: "SECRET_HOST", Err: "SECRET_PROVIDER_RESPONSE"}
	raw := &url.Error{Op: "Get", URL: "https://SECRET_USER:SECRET_PASSWORD@example.invalid/SECRET_PATH", Err: inner}
	detail := Diagnose("upstream_headers", raw)
	if detail.Code != "dns" || !errors.Is(detail, inner) || strings.Contains(detail.Error(), "SECRET") {
		t.Fatal("unsafe or unclassified transport failure")
	}
	for _, tc := range []struct {
		err  error
		code string
	}{{context.Canceled, "cancelled"}, {context.DeadlineExceeded, "timeout"}, {ErrProviderBusy, "provider_busy"}, {ErrProviderPaced, "provider_paced"}, {core.ErrStreamLimit, "stream_limit"}} {
		d := Diagnose("tune", tc.err)
		if d.Code != tc.code || !errors.Is(d, tc.err) {
			t.Fatalf("lost classification %s", tc.code)
		}
	}
}

func TestLiveMediaDiagnosticsRequireDeliveredBytesAndBoundFailures(t *testing.T) {
	var logs diagnosticBuffer
	s := testService(t, Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	ss := &session{info: SessionInfo{ID: "session", Channel: Channel{ID: "channel"}}}
	for range 3 {
		rec := httptest.NewRecorder()
		observed := s.observeMedia(rec, ss)
		http.Error(observed, "unavailable", 503)
		observed.done()
		if rec.Code != 503 || rec.Body.String() != "unavailable\n" {
			t.Fatal("observation altered error response")
		}
	}
	empty := s.observeMedia(httptest.NewRecorder(), ss)
	empty.WriteHeader(200)
	empty.done()
	if ss.firstMediaDelivered {
		t.Fatal("failed or empty response counted as delivered media")
	}
	for range 2 {
		rec := httptest.NewRecorder()
		observed := s.observeMedia(rec, ss)
		observed.Header().Set("Content-Type", "video/mp2t")
		observed.WriteHeader(206)
		observed.Write([]byte("media-bytes"))
		observed.Flush()
		observed.done()
		if rec.Code != 206 || rec.Body.String() != "media-bytes" || !rec.Flushed {
			t.Fatal("observation altered media response")
		}
	}
	text := logs.text()
	if strings.Count(text, "live media response failed") != 1 || strings.Count(text, "live first media response delivered") != 1 {
		t.Fatal("media evidence missing or repeated warnings unbounded")
	}
}
