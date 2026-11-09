package sessions

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

type rememberingPlayback struct {
	*fakePlayback
	release string
	exact   bool
}

func (p *rememberingPlayback) PrepareSurface(ctx context.Context, u *core.User, key core.RatingKey, c core.ClientCaps, release string, exact bool) (*core.PlaySession, error) {
	p.release, p.exact = release, exact
	ps, err := p.fakePlayback.Prepare(ctx, u, key, c)
	if err != nil {
		return nil, err
	}
	if release != "" {
		ps.Resolution = &core.Resolution{Release: core.Release{ID: release}}
	}
	return ps, nil
}

func TestExpiredDeliveryAuthorityDoesNotBlockPlayback(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	viewer := &core.Viewer{ID: 8}
	device := core.Device{ID: "living-room"}
	item := core.MovieKey(42)
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	playback := &rememberingPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}}
	bus := NewEventBus(nil, active)
	t.Cleanup(bus.Close)
	b, err := NewBroker(BrokerDeps{
		DB: db, Events: bus, Playback: playback, Transcoder: fakeBrokerTranscoder{}, Progress: &fakeProgress{},
		ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit: func(*core.Viewer) int { return 0 },
		OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: key}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(context.Background()) })
	const sessionID = "11111111-1111-4111-8111-111111111111"
	plan, err := b.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "silo", Item: item, Intent: frontend.PlayIntent{ClientSessionID: sessionID, Options: core.DecideOptions{DirectPlay: true}}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "silo", PlanID: plan.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.StartStream(ctx, frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "silo"}, session.ID, frontend.StreamRequest{Format: frontend.StreamDirect}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var row deliveryAuthority
	var ok bool
	for {
		row, ok, err = lookupDeliveryAuthority(ctx, db, viewer.ID, int64(item), "silo", device.ID)
		if err != nil || (ok && row.ReleaseID != "") || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil || !ok || row.Digest == "" {
		t.Fatalf("authority %+v ok=%v err=%v", row, ok, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE capability_headers SET expires=1 WHERE namespace=? AND digest=?`, deliveryNamespace, row.Digest); err != nil {
		t.Fatal(err)
	}
	again := &rememberingPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}}
	restarted, err := NewBroker(BrokerDeps{
		DB: db, Events: bus, Playback: again, Transcoder: fakeBrokerTranscoder{}, Progress: &fakeProgress{},
		ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit: func(*core.Viewer) int { return 0 },
		OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: key}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close(context.Background()) })
	if _, err = restarted.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "silo", Item: item, Intent: frontend.PlayIntent{ClientSessionID: sessionID, Options: core.DecideOptions{DirectPlay: true}}}); err != nil {
		t.Fatal(err)
	}
	if again.exact || again.release != "" {
		t.Fatalf("expired authority still pinned release %q exact %v", again.release, again.exact)
	}
}

func TestDeliveryAuthoritySurvivesRestartAndRejectsStaleSession(t *testing.T) {
	testDeliveryAuthority(t, store.OpenTest(t))
}

func TestDeliveryAuthorityPostgres(t *testing.T) {
	raw := os.Getenv(store.TestPostgresEnv)
	if raw == "" {
		t.Skip("set " + store.TestPostgresEnv + " to run PostgreSQL tests")
	}
	db, err := store.OpenTestPostgres(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	testDeliveryAuthority(t, db)
}

func testDeliveryAuthority(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	viewer := &core.Viewer{ID: 8}
	device := core.Device{ID: "living-room"}
	item := core.MovieKey(42)
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	newBroker := func(playback *rememberingPlayback) *Broker {
		t.Helper()
		bus := NewEventBus(nil, active)
		t.Cleanup(bus.Close)
		b, err := NewBroker(BrokerDeps{
			DB: db, Events: bus, Playback: playback, Transcoder: fakeBrokerTranscoder{}, Progress: &fakeProgress{},
			ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true },
			BitrateLimit: func(*core.Viewer) int { return 0 },
			OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
			Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
				return &core.Title{Key: key}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.Close(context.Background()) })
		return b
	}
	openStream := func(b *Broker, client string) string {
		t.Helper()
		plan, err := b.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", Item: item, Intent: frontend.PlayIntent{ClientSessionID: client, Options: core.DecideOptions{DirectPlay: true}}})
		if err != nil {
			t.Fatal(err)
		}
		session, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", PlanID: plan.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = b.StartStream(ctx, frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "jellyfin"}, session.ID, frontend.StreamRequest{Format: frontend.StreamDirect}); err != nil {
			t.Fatal(err)
		}
		return session.ID
	}
	firstPlayback := &rememberingPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}}
	first := newBroker(firstPlayback)
	originalSession := openStream(first, "client-a")
	var original deliveryAuthority
	var ok bool
	var err error
	deadline := time.Now().Add(2 * time.Second)
	for {
		original, ok, err = lookupDeliveryAuthority(ctx, db, viewer.ID, int64(item), "jellyfin", device.ID)
		if err != nil || (ok && original.SessionID == "client-a" && original.ReleaseID != "" && original.Token > 0) || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil || !ok || original.SessionID != "client-a" || original.ReleaseID == "" || original.Token <= 0 {
		t.Fatalf("published authority %+v ok=%v err=%v", original, ok, err)
	}
	restartedPlayback := &rememberingPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}}
	restarted := newBroker(restartedPlayback)
	if _, err = restarted.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", Item: item, Intent: frontend.PlayIntent{ClientSessionID: "client-a", Options: core.DecideOptions{DirectPlay: true}}}); err != nil {
		t.Fatal(err)
	}
	if !restartedPlayback.exact || restartedPlayback.release != original.ReleaseID {
		t.Fatalf("restart prepared release %q exact %v, authority %q", restartedPlayback.release, restartedPlayback.exact, original.ReleaseID)
	}
	openStream(first, "client-b")
	var newer deliveryAuthority
	deadline = time.Now().Add(2 * time.Second)
	for {
		newer, ok, err = lookupDeliveryAuthority(ctx, db, viewer.ID, int64(item), "jellyfin", device.ID)
		if err != nil || (ok && newer.SessionID == "client-b" && newer.Token > original.Token) || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil || !ok || newer.SessionID != "client-b" || newer.Token <= original.Token {
		t.Fatalf("newer authority %+v ok=%v err=%v", newer, ok, err)
	}
	// The original session may already have been superseded. Either way its
	// older token must not become the authority again.
	_, _ = first.StartStream(ctx, frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "jellyfin"}, originalSession, frontend.StreamRequest{Format: frontend.StreamDirect})
	stale := original
	stale.Generation++
	published, err := publishDeliveryAuthority(ctx, db, stale, time.Now())
	if err != nil || published {
		t.Fatalf("stale token publish = %v err=%v", published, err)
	}
	kept, ok, err := lookupDeliveryAuthority(ctx, db, viewer.ID, int64(item), "jellyfin", device.ID)
	if err != nil || !ok || kept.SessionID != "client-b" || kept.Token != newer.Token || kept.ReleaseID != newer.ReleaseID {
		t.Fatalf("authority after stale publish %+v ok=%v err=%v", kept, ok, err)
	}
}

// Expired grants are retired as new ones are published, so a viewer's
// distinct titles on a surface never exhaust admission, and a live grant
// is never retired.
func TestDeliveryAuthorityRetiresOnlyExpiredGrants(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	t0 := time.UnixMilli(1_800_000_000_000)
	publish := func(item int64, device string, at time.Time) {
		t.Helper()
		token, err := reserveDeliveryToken(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := publishDeliveryAuthority(ctx, db, deliveryAuthority{ViewerID: 8, ItemID: item, Surface: "silo", DeviceID: device,
			SessionID: "s", Generation: 1, ReleaseID: "r", Revision: "v", Token: token}, at)
		if err != nil || !ok {
			t.Fatalf("publish item %d at %v: ok=%v err=%v", item, at, ok, err)
		}
	}
	for item := int64(1); item <= 256; item++ {
		publish(item, "tv", t0)
	}
	live := t0.Add(150 * time.Minute)
	publish(1000, "phone", live)
	for item := int64(257); item <= 300; item++ {
		publish(item, "tv", t0.Add(3*time.Hour))
	}
	if _, ok, err := lookupDeliveryAuthority(ctx, db, 8, 1000, "silo", "phone"); err != nil || !ok {
		t.Fatalf("live grant retired: ok=%v err=%v", ok, err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM capability_headers WHERE namespace=?`, deliveryNamespace).Scan(&n); err != nil || n > 256 {
		t.Fatalf("headers=%d err=%v", n, err)
	}
}

// Prune keeps the newest tokens and any token an authority row names, and
// a token reserved afterwards still orders after every earlier one.
func TestDeliveryTokenPruneKeepsReferencedAndNewest(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	var tokens []int64
	for i := 0; i < 5; i++ {
		token, err := reserveDeliveryToken(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	if ok, err := publishDeliveryAuthority(ctx, db, deliveryAuthority{ViewerID: 8, ItemID: 1, Surface: "silo", DeviceID: "tv",
		SessionID: "s", Generation: 1, ReleaseID: "r", Revision: "v", Token: tokens[0]}, time.Now()); err != nil || !ok {
		t.Fatalf("publish: ok=%v err=%v", ok, err)
	}
	if n, err := pruneDeliveryTokens(ctx, db, 2); err != nil || n != 2 {
		t.Fatalf("pruned %d %v, want 2", n, err)
	}
	for i, token := range tokens {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions_delivery_tokens WHERE id=?`, token).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if want := map[bool]int{true: 1, false: 0}[i == 0 || i >= 3]; n != want {
			t.Fatalf("token %d kept=%d, want %d", i, n, want)
		}
	}
	if next, err := reserveDeliveryToken(ctx, db); err != nil || next <= tokens[4] {
		t.Fatalf("next token %d after %d: %v", next, tokens[4], err)
	}
}
