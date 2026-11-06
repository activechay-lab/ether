package nodeauthority

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodesource"
	"github.com/braedonsaunders/ether/internal/playback"
)

// Minimal offline engine drives real playback preparation and SourceAccess.
// Embedded interfaces fail loudly if this fixture unexpectedly uses more APIs.
type fixtureCatalog struct{ core.Catalog }

func (fixtureCatalog) Get(_ context.Context, k core.RatingKey) (*core.Title, error) {
	return &core.Title{Key: k, Type: core.TypeMovie, Title: "Fixture"}, nil
}

type fixtureResolver struct{ core.Resolver }

func (fixtureResolver) Resolve(_ context.Context, r core.ResolveRequest) (*core.Resolution, error) {
	return &core.Resolution{Title: r.Title.Key, Release: core.Release{ID: "fixture", Name: "fixture.mkv"}, NZB: []byte("fixture")}, nil
}

type fixtureEngine struct {
	core.Engine
	src *fixtureBytes
}

func (f fixtureEngine) Open(context.Context, string, []byte) (core.MediaSource, error) {
	return f.src, nil
}
func (fixtureEngine) Prefetch(context.Context, string, []byte, int64, int64) error { return nil }

type fixtureTranscoder struct{ core.Transcoder }

func (fixtureTranscoder) Probe(context.Context, string) (*core.MediaInfo, error) {
	return &core.MediaInfo{Size: 10, Container: "mkv", Duration: time.Minute, Streams: []core.StreamInfo{{Type: "video", Codec: "h264", Height: 720}}}, nil
}
func (fixtureTranscoder) Session(string) (core.HLSSession, bool) { return nil, false }

type fixtureBytes struct {
	size   atomic.Int64
	closes atomic.Int64
}

func (f *fixtureBytes) Size() int64     { return f.size.Load() }
func (*fixtureBytes) Name() string      { return "fixture.mkv" }
func (*fixtureBytes) ReleaseID() string { return "fixture" }
func (f *fixtureBytes) Close() error    { f.closes.Add(1); return nil }
func (f *fixtureBytes) ReadAt(b []byte, off int64) (int, error) {
	return f.ReadAtContext(context.Background(), b, off)
}
func (*fixtureBytes) ReadAtContext(_ context.Context, b []byte, _ int64) (int, error) {
	clear(b)
	return 0, io.EOF
}

func TestRealSourceAccessBindingAndErrorOwnership(t *testing.T) {
	ctx := context.Background()
	src := &fixtureBytes{}
	src.size.Store(10)
	m, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{src: src}, Transcoder: fixtureTranscoder{}, SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	ps, outcome, err := m.PrepareSurfaceSource(ctx, &core.User{ID: 9}, core.MovieKey(1), core.ClientCaps{ClientID: "fixture"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	access, err := playback.NewSourceAccess(m, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	rev := core.SourceRevision(&core.PlaySession{Key: ps.Key, Source: src})
	handle, err := access.Capture(ctx, 9, ps.ID, outcome, m.InputURL(ps.ID), rev)
	if err != nil {
		t.Fatal(err)
	}
	a, db, _, _, now := fixture(t)
	// Reuse the placement-shaped authoritative dependency with a fresh registry.
	a, err = New(a.o)
	if err != nil {
		t.Fatal(err)
	}
	scope := originalScope(db.record)
	scope.Revision = rev
	stable, err := a.Bind(ctx, scope, handle, 10)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := a.Issue(ctx, stable, now.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Verify(ctx, nodesource.Peer{NodeID: "worker", Boot: "wb"}, lease.Grant().Credential); err != nil {
		t.Fatal(err)
	}
	tracker := &core.ReadRetirement{}
	held, err := a.Acquire(core.WithReadRetirement(ctx, tracker), scope)
	if err != nil || held.Release == nil {
		t.Fatal("real acquisition", err)
	}
	// A fresh size mismatch fails AFTER manager retention; Release must survive.
	src.size.Store(11)
	failed, err := a.Acquire(core.WithReadRetirement(ctx, &core.ReadRetirement{}), scope)
	if err == nil || failed.Release == nil || failed.Source != nil {
		t.Fatal("error hold lost")
	}
	if err = a.Revoke(stable); err != nil {
		t.Fatal(err)
	}
	if handle.Active() {
		t.Fatal("revoke missed manager barrier")
	}
	if _, err = a.Acquire(core.WithReadRetirement(ctx, &core.ReadRetirement{}), scope); err == nil {
		t.Fatal("acquired after revoke")
	}
	if src.closes.Load() != 0 {
		t.Fatal("registry closed backend")
	}
	held.Release()
	failed.Release()
	if n := access.Reconcile(); n != 0 {
		t.Fatal("hold reconciliation", n)
	}
	if src.closes.Load() != 0 {
		t.Fatal("request release retired owner source")
	}
	if err = m.RetireSource(ps.ID, outcome); err != nil {
		t.Fatal(err)
	}
	select {
	case <-outcome.Done():
	case <-time.After(time.Second):
		t.Fatal("trusted retirement did not drain")
	}
}
