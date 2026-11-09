package livetv

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// Graphics rendering is viewer-driven: an enabled channel nobody is
// watching - clock, blocks or plain lineup - plans no graphics work at
// all. The explicit look path (console preview) still answers.
func TestGraphicsRequestsRequireAViewer(t *testing.T) {
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	s := &Service{
		o:        Options{DB: db, Now: func() time.Time { return now }},
		sessions: map[string]*session{},
	}
	c := Channel{
		ID: "decades", Name: "Decades", Kind: "virtual", Enabled: true,
		Timezone: "UTC", Epoch: now.Add(-24 * time.Hour),
		Look:  &ChannelLook{LookID: "showcase-80", Ident: true, UpNext: true, OffAir: true},
		Clock: []ClockSlot{{SegmentID: "plugin:weather", EveryMinutes: 60, Minute: 0, From: 0, Until: 1440}},
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO livetv_channels(id,data) VALUES(?,?)`, c.ID, b); err != nil {
		t.Fatal(err)
	}
	if got := s.GraphicsRequests(t.Context(), ""); len(got) != 0 {
		t.Fatalf("unwatched clock channel planned %d graphics requests, want none", len(got))
	}
	// The console's explicit look path is not viewer-gated.
	if got := s.GraphicsRequests(t.Context(), "showcase-80"); len(got) == 0 {
		t.Fatal("explicit look request planned no graphics")
	}
	// Once watched, the channel plans its graphics again.
	s.mu.Lock()
	s.noteWatchedLocked(c.ID)
	s.mu.Unlock()
	if got := s.GraphicsRequests(t.Context(), ""); len(got) == 0 {
		t.Fatal("watched channel planned no graphics")
	}
}
