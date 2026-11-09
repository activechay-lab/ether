package playback

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestSubtitleSourceLeaseRequiresPlayingAndFencesRetirement(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, time.Minute)}, []string{"A"})
	ps := h.prepare(owner, movieKey, "viewer")
	ctx := context.Background()
	revision := core.SourceRevision(ps)
	called := false
	if err := h.m.WithSourceResource(ctx, ps.ID, revision+"stale", func(context.Context, io.ReaderAt, int64) error {
		called = true
		return nil
	}); !errors.Is(err, core.ErrSourceChanged) || called {
		t.Fatal("stale resource lease admitted", err)
	}
	if err := h.m.WithSourceResource(ctx, ps.ID, revision, func(readCtx context.Context, src io.ReaderAt, size int64) error {
		if size <= 0 || src == nil || readCtx.Err() != nil {
			t.Fatal("invalid resource lease")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := h.m.LeaseSubtitleSource(ctx, ps.ID, revision); !errors.Is(err, core.ErrSourceChanged) {
		t.Fatal("prepare acquired lease", err)
	}
	if err := h.m.SourcePlaybackProgress(ctx, ps.ID, revision, ps.UserID, ps.ClientID, ps.Key, time.Second, "playing"); err != nil {
		t.Fatal(err)
	}
	lease, err := h.m.LeaseSubtitleSource(ctx, ps.ID, revision)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	st := core.StreamInfo{Type: "subtitle", Codec: "srt", Language: "en", External: true, ExternalURL: "ether-subtitle:fixture"}
	attached, err := lease.Attach(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	again, err := lease.Attach(ctx, st)
	if err != nil || again.Index != attached.Index {
		t.Fatal("duplicate attachment", again, err)
	}
	live, ok := h.m.Session(ps.ID)
	if !ok {
		t.Fatal("session missing")
	}
	found := false
	for _, track := range live.Info.Streams {
		if track.ExternalURL == st.ExternalURL {
			found = true
		}
	}
	if !found {
		t.Fatal("track not in live session")
	}
	if r, _, done, ok := lease.Hold(); !ok || r == nil {
		t.Fatal("correction lease unavailable")
	} else {
		done()
		done()
	}

	if err := h.m.WithSourceSelection(ctx, ps.ID, revision+"stale", func(core.MediaInfo) error { t.Fatal("stale selection admitted"); return nil }); !errors.Is(err, core.ErrSourceChanged) {
		t.Fatal(err)
	}
	if err := h.m.WithSourceSelection(ctx, ps.ID, revision, func(info core.MediaInfo) error {
		for _, st := range info.Streams {
			if st.Index == attached.Index && st.ExternalURL == attached.ExternalURL {
				return nil
			}
		}
		t.Fatal("selection fence omitted attached track")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.m.WithSourceResource(ctx, ps.ID, revision, func(readCtx context.Context, src io.ReaderAt, _ int64) error {
		h.m.Stop(ps.ID)
		if _, err := src.ReadAt(make([]byte, 1), 0); err == nil {
			t.Fatal("retired resource still readable")
		}
		return nil
	}); err == nil {
		t.Fatal("retired artifact published")
	}

	h.m.Stop(ps.ID)
	if err := h.m.WithSourceSelection(ctx, ps.ID, revision, func(core.MediaInfo) error { t.Fatal("stopped selection admitted"); return nil }); !errors.Is(err, core.ErrSourceChanged) {
		t.Fatal(err)
	}

	if lease.Context.Err() == nil {
		t.Fatal("retirement did not cancel")
	}
	if _, err := lease.Attach(ctx, st); !errors.Is(err, core.ErrSourceChanged) {
		t.Fatal("stale attach", err)
	}
	if _, _, _, ok := lease.Hold(); ok {
		t.Fatal("retired correction reader")
	}
	lease.Close()
}
