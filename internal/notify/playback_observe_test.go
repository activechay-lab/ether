package notify

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/store"
)

// The playback event bus also carries console-refresh kinds (update, progress,
// surface) that are not notification events. Observing them must neither queue
// anything nor log an enqueue failure, even for a channel subscribed to "*".
func TestObservePlaybackIgnoresNonNotificationKinds(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var logs bytes.Buffer
	s := New(db, Options{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	if err := s.Save(ctx, Channel{ID: "hook", Kind: "webhook", Enabled: true, Secrets: map[string]string{"endpoint": "https://fixture.example/hook"}, Events: []string{"*"}}); err != nil {
		t.Fatal(err)
	}
	session := &core.PlaySession{ID: "s1", Username: "viewer", TitleName: "Fixture"}
	for _, kind := range []string{"update", "progress", "surface"} {
		s.ObservePlayback(playback.Event{Kind: kind, Session: session, At: time.Now()})
	}
	if strings.Contains(logs.String(), "enqueue notification") {
		t.Fatalf("non-notification playback kind logged an enqueue failure:\n%s", logs.String())
	}
	queued := func() int {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM notify_queue`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := queued(); n != 0 {
		t.Fatalf("queued %d notifications for non-notification playback kinds", n)
	}

	// The registered playback kinds still reach a "*" channel.
	for _, kind := range []string{"start", "failover", "downfailover", "error", "stop"} {
		s.ObservePlayback(playback.Event{Kind: kind, Session: session, At: time.Now()})
	}
	if n := queued(); n != 5 {
		t.Fatalf("queued %d notifications for registered playback kinds, want 5\n%s", n, logs.String())
	}
	if logs.Len() != 0 {
		t.Fatalf("unexpected log output:\n%s", logs.String())
	}
}
