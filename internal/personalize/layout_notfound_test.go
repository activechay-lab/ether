package personalize

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type layoutErrCollections struct {
	errs map[string]error
}

func (l *layoutErrCollections) HomeRowsFor(context.Context, *core.User, core.HubContext) ([]core.CollectionRow, error) {
	return nil, nil
}

func (l *layoutErrCollections) LayoutRow(_ context.Context, _ *core.User, _ core.HubContext, id string) (*core.CollectionRow, error) {
	if err, ok := l.errs[id]; ok {
		return nil, err
	}
	return nil, core.ErrNotFound
}

func TestDisabledLayoutCollectionDoesNotLogUnavailable(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cols := &layoutErrCollections{
		errs: map[string]error{
			"289": core.ErrNotFound,
			"288": context.DeadlineExceeded,
		},
	}
	h := newHarness(t, func(o *Options) {
		o.Collections = cols
		o.Logger = logger
	})
	ctx := context.Background()
	if _, err := h.e.SetHomeLayout(ctx, core.HomeLayout{
		Global: []core.LayoutPage{{
			Section: "home",
			Rows: []core.RowPolicy{
				{Row: "collection:289", Status: "always", Slot: 1},
				{Row: "collection:288", Status: "always", Slot: 2},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	u := core.User{ID: 1, IsOwner: true}
	h.rebuild(u)
	if _, err := h.e.HomeHubsFor(ctx, &u, hubContext(testNow, core.DeviceDesktop), 12); err != nil {
		t.Fatal(err)
	}
	logs := buf.String()
	if strings.Contains(logs, "id=289") {
		t.Fatalf("disabled/missing collection ErrNotFound should not log unavailable: %s", logs)
	}
	if !strings.Contains(logs, "id=288") {
		t.Fatalf("transient LayoutRow error should still log at debug: %s", logs)
	}
}
