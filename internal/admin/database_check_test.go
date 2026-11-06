package admin_test

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/admin"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDatabaseCheckOwnerAndErrors(t *testing.T) {
	cfg := newStore(t, seededConfig)
	called := 0
	deps := admin.Deps{Config: cfg, IsOwnerToken: storedOwner(cfg), DatabaseCheck: func(ctx context.Context) (any, error) {
		called++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Error("missing deadline")
		}
		return map[string]any{"integrity": map[string]any{"ok": true}}, nil
	}}
	for _, tc := range []struct {
		token string
		want  int
	}{{"", 403}, {ownerToken, 200}} {
		h := admin.NewHandler(deps)
		r := req("POST", "/api/status/database/check", "{}")
		r.Header.Set("X-Plex-Token", tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("code=%d want=%d body=%s", w.Code, tc.want, w.Body)
		}
	}
	if called != 1 {
		t.Fatalf("called=%d", called)
	}
	deps.DatabaseCheck = func(context.Context) (any, error) { return nil, errors.New("check unavailable") }
	r := req("POST", "/api/status/database/check", "{}")
	r.Header.Set("X-Plex-Token", ownerToken)
	w := httptest.NewRecorder()
	admin.NewHandler(deps).ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("error: %d %s", w.Code, w.Body)
	}
	deps.Config = newStore(t, nil)
	deps.IsOwnerToken = storedOwner(deps.Config)
	w = httptest.NewRecorder()
	admin.NewHandler(deps).ServeHTTP(w, req("POST", "/api/status/database/check", "{}"))
	if w.Code != 403 {
		t.Fatalf("tokenless setup: %d %s", w.Code, w.Body)
	}
}
