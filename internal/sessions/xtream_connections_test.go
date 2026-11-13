package sessions

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http/httptest"
	"testing"
	"time"
)

func TestXtreamConnectionLimitSharedAcrossNodes(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,username,title,is_owner,active,created_at,updated_at) VALUES(1,'viewer','Viewer',0,1,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Now: func() time.Time { return now }}}
	one := &lazyStreams{db: db, broker: b}
	two := &lazyStreams{db: db, broker: b}
	r := httptest.NewRequest("GET", "http://ether.test/file", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	_, release, err := one.acquireXtreamConnection(r, "grant", 1, "movie/1")
	if err != nil {
		t.Fatal(err)
	}
	release()
	_, release, err = two.acquireXtreamConnection(r, "grant", 1, "movie/1")
	if err != nil {
		t.Fatal("range reuse", err)
	}
	release()
	if _, release, err = two.acquireXtreamConnection(r, "grant", 1, "movie/2"); err == nil {
		release()
		t.Fatal("second item admitted")
	}
	other := r.Clone(ctx)
	other.RemoteAddr = "192.0.2.2:1234"
	if _, release, err = two.acquireXtreamConnection(other, "grant", 1, "movie/1"); err == nil {
		release()
		t.Fatal("second client admitted")
	}
	now = now.Add(31 * time.Second)
	_, release, err = two.acquireXtreamConnection(other, "grant", 1, "movie/2")
	if err != nil {
		t.Fatal("expired slot", err)
	}
	release()
}

type xtreamGrants struct{}

func (xtreamGrants) GrantReference(context.Context, string, string) (string, *core.Viewer, error) {
	return "grant", &core.Viewer{ID: 1}, nil
}
func (xtreamGrants) ValidateGrantReference(context.Context, string, string, int64) error { return nil }
func TestXtreamPrimarySwitchFencesOldSegments(t *testing.T) {
	db := store.OpenTest(t)
	_, err := db.Exec(`INSERT INTO auth_users(id,username,title,is_owner,active,created_at,updated_at) VALUES(1,'viewer','Viewer',0,1,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	viewer := &core.Viewer{ID: 1}
	b := &Broker{deps: BrokerDeps{SurfacePolicy: testSessionPolicy, Now: time.Now, ActiveViewer: func(context.Context, int64) (*core.Viewer, error) { return viewer, nil }}}
	s := &lazyStreams{protocol: "xtream", db: db, broker: b, grants: xtreamGrants{}}
	r := httptest.NewRequest("GET", "http://ether.test/live", nil)
	r.RemoteAddr = "192.0.2.1:1234"
	req := frontend.PlanRequest{Protocol: "xtream", Viewer: viewer, Device: core.Device{ID: "login"}, Channel: "one"}
	if err := s.PrepareAppConnection(r, "password", req); err != nil {
		t.Fatal(err)
	}
	req.Channel = "two"
	if err := s.PrepareAppConnection(r, "password", req); err != nil {
		t.Fatal("switch blocked", err)
	}
	if _, release, err := s.acquireXtreamConnection(r, "grant", 1, "0/one"); err == nil {
		release()
		t.Fatal("stale segment stole slot")
	}
	_, release, err := s.acquireXtreamConnection(r, "grant", 1, "0/two")
	if err != nil {
		t.Fatal("new channel rejected", err)
	}
	release()
	other := r.Clone(context.Background())
	other.RemoteAddr = "192.0.2.2:2345"
	if err := s.PrepareAppConnection(other, "password", req); err == nil {
		t.Fatal("competing peer admitted")
	}
}
