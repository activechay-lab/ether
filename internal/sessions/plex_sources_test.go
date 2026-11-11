package sessions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type sourceAdmissionProbe struct {
	core.Playback
	waited, admitted int
	waitErr          error
	release          string
}

func (p *sourceAdmissionProbe) PrepareWithin(ctx context.Context, wait time.Duration, u *core.User, key core.RatingKey, caps core.ClientCaps, release string, exact bool) (*core.PlaySession, error) {
	p.waited++
	p.release = release
	return nil, p.waitErr
}
func (p *sourceAdmissionProbe) Prepare(ctx context.Context, u *core.User, key core.RatingKey, caps core.ClientCaps) (*core.PlaySession, error) {
	p.admitted++
	return &core.PlaySession{ID: "auto", UserID: u.ID, Key: key, ClientID: caps.ClientID}, nil
}
func (p *sourceAdmissionProbe) PrepareVariant(ctx context.Context, u *core.User, key core.RatingKey, caps core.ClientCaps, release string) (*core.PlaySession, error) {
	p.admitted++
	p.release = release
	return &core.PlaySession{ID: release, UserID: u.ID, Key: key, ClientID: caps.ClientID}, nil
}
func TestPlexSourcesPreserveBoundedAdmission(t *testing.T) {
	p := &sourceAdmissionProbe{waitErr: context.Canceled}
	source := PartSources{Playback: p}
	viewer := &core.User{ID: 7}
	caps := core.ClientCaps{ClientID: "device"}
	if _, err := source.Prepare(context.Background(), viewer, core.MovieKey(1), caps, "picked", time.Second); !errors.Is(err, context.Canceled) || p.admitted != 0 {
		t.Fatalf("wait failure fell through: %v %+v", err, p)
	}
	p.waitErr = nil
	ps, err := source.Prepare(context.Background(), viewer, core.MovieKey(1), caps, "picked", time.Second)
	if err != nil || ps.ID != "picked" || p.admitted != 1 || p.waited != 2 {
		t.Fatalf("variant admission: %+v %v %+v", ps, err, p)
	}
	// A base-only manager intentionally retains automatic fallback.
	source.Playback = struct{ core.Playback }{p}
	ps, err = source.Prepare(context.Background(), viewer, core.MovieKey(1), caps, "picked", time.Second)
	if err != nil || ps.ID != "auto" || p.admitted != 2 {
		t.Fatalf("automatic fallback: %+v %v", ps, err)
	}
}

// kindSourcePlayback implements both core.Playback (via the embedded probe) and
// core.KindPlayback, so PartSources routes a plugin key through PrepareKind
// rather than the catalogue Prepare path.
type kindSourcePlayback struct {
	core.Playback
	kindPrepared int
	kindSession  *core.PlaySession
}

func (k *kindSourcePlayback) SessionFor(int64, string, core.RatingKey) (*core.PlaySession, bool) {
	return nil, false
}

func (k *kindSourcePlayback) PrepareKind(_ context.Context, u *core.User, in core.KindInput, caps core.ClientCaps) (*core.PlaySession, error) {
	k.kindPrepared++
	if in.Title == nil {
		return nil, core.ErrNotAvailable
	}
	k.kindSession = &core.PlaySession{ID: "kind", UserID: u.ID, Key: in.Title.Key, ClientID: caps.ClientID}
	return k.kindSession, nil
}

func (k *kindSourcePlayback) SessionForKind(u int64, client string, key core.RatingKey) (*core.PlaySession, bool) {
	if k.kindSession != nil && k.kindSession.UserID == u && k.kindSession.ClientID == client && k.kindSession.Key == key {
		return k.kindSession, true
	}
	return nil, false
}

func TestPartSourcesRoutesPluginKeyThroughKindPlayback(t *testing.T) {
	key := core.PluginKey(7)
	play := &kindSourcePlayback{}
	source := PartSources{
		Playback: play,
		KindSource: func(_ context.Context, _ *core.Viewer, k core.RatingKey) (*core.KindInput, error) {
			if k != key {
				return nil, core.ErrNotFound
			}
			title := &core.Title{Key: key, Type: core.TypeEpisode}
			return &core.KindInput{Title: title, SourceID: k.String()}, nil
		},
	}
	ps, err := source.Prepare(context.Background(), &core.User{ID: 7}, key, core.ClientCaps{ClientID: "device"}, "", time.Second)
	if err != nil || ps == nil || ps.ID != "kind" {
		t.Fatalf("plugin Prepare = %v %v", ps, err)
	}
	if play.kindPrepared != 1 {
		t.Fatalf("KindPlayback.PrepareKind calls = %d, want 1", play.kindPrepared)
	}
	got := source.Part(7, "device", key, "")
	if !got.Matches || got.Session == nil || got.Session.ID != "kind" {
		t.Fatalf("Part plugin = %+v", got)
	}
	// A plugin key with a version release is refused: plugin items have no
	// catalogue variants.
	if _, err := source.Prepare(context.Background(), &core.User{ID: 7}, key, core.ClientCaps{ClientID: "device"}, "v1", time.Second); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("plugin Prepare with release = %v, want ErrNotAvailable", err)
	}
}
