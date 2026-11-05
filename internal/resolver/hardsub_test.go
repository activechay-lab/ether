package resolver

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestHardsubMemoryAndSelection(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	title := matrixTitle()
	clean := rel("clean", "The.Signal.1999.720p.BluRay.x264-CLEAN", 4000)
	burned := rel("burned", "The.Signal.1999.1080p.BluRay.HC.x264-GROUP", 8000)
	idx := &fakeIndexer{releases: []core.Release{burned, clean}}
	s := New(idx, fakeHealth{}, db, testCfg(), Options{})
	defer s.Close()
	req := core.ResolveRequest{Title: title, Client: webCaps()}
	got, err := s.Resolve(ctx, req)
	if err != nil || got.Release.ID != "clean" {
		t.Fatalf("clean should win: %v %v", got, err)
	}
	// Evidence learned on another title/viewer is shared, including after restart.
	if err = s.RememberSubtitleVerdict(ctx, clean.ID, true); err != nil {
		t.Fatal(err)
	}
	if err = s.RejectRelease(ctx, title.Key, burned); err != nil {
		t.Fatal(err)
	}
	restarted := New(idx, fakeHealth{}, db, testCfg(), Options{})
	defer restarted.Close()
	if v, known := restarted.SubtitleVerdict(ctx, clean.ID); !known || !v {
		t.Fatal("lost subtitle verdict")
	}
	for _, duplicate := range []core.Release{
		{ID: "repost", Name: burned.Name + ".mkv"},
		{ID: "reencode", Name: "other", Size: burned.Size, Info: core.ReleaseInfo{Group: "GROUP"}},
	} {
		if !restarted.TitleReleaseRejected(ctx, title.Key, duplicate) {
			t.Fatalf("lost duplicate: %+v", duplicate)
		}
		if restarted.TitleReleaseRejected(ctx, core.MovieKey(999), duplicate) {
			t.Fatal("rejection crossed title boundary")
		}
	}
	if err = restarted.forgetRejection(ctx, burned.ID); err != nil {
		t.Fatal(err)
	}
	if restarted.TitleReleaseRejected(ctx, title.Key, burned) {
		t.Fatal("unblock did not restore release")
	}
	// A last-resort release remains playable when nothing else exists.
	alone := New(&fakeIndexer{releases: []core.Release{burned}}, fakeHealth{}, nil, testCfg(), Options{})
	defer alone.Close()
	if got, err = alone.Resolve(ctx, req); err != nil || got.Release.ID != burned.ID {
		t.Fatalf("last resort: %v %v", got, err)
	}
}
func TestHardsubNearRelease(t *testing.T) {
	base := core.Release{ID: "a", InfoHash: "abc", SourceHash: "source"}
	for _, r := range []core.Release{{ID: "a"}, {InfoHash: "ABC"}, {SourceHash: "source"}} {
		if !nearRelease(base, r) {
			t.Fatal(r)
		}
	}
	if nearRelease(base, core.Release{ID: "b"}) {
		t.Fatal("empty identities matched")
	}
}

func TestHardsubExplicitExclusion(t *testing.T) {
	cfg := testCfg()
	cfg.Resolver.HardSubtitles = "exclude"
	r := rel("hidden", "The.Signal.1999.1080p.BluRay.x264-GROUP", 8000)
	s := New(&fakeIndexer{releases: []core.Release{r}}, fakeHealth{}, nil, cfg, Options{})
	defer s.Close()
	if err := s.RememberSubtitleVerdict(t.Context(), r.ID, true); err != nil {
		t.Fatal(err)
	}
	if !s.HardSubtitleBlocked(t.Context(), r) {
		t.Fatal("explicit exclusion ignored content evidence")
	}
	if _, err := s.Resolve(t.Context(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}); err == nil {
		t.Fatal("blocked only option played")
	}
}

func TestHardsubKeywordIndependent(t *testing.T) {
	cfg := testCfg()
	cfg.Resolver.BlockedTerms = []string{"sub"}
	r := rel("hc", "The.Signal.1999.1080p.HC.WEBRip.x264-GROUP", 8000)
	s := New(&fakeIndexer{releases: []core.Release{r}}, fakeHealth{}, nil, cfg, Options{})
	defer s.Close()
	if _, err := s.Resolve(t.Context(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}); err != nil {
		t.Fatalf("sub must not alias HC: %v", err)
	}
}
func TestHardsubCheckClaimPersists(t *testing.T) {
	db := openTestDB(t)
	s := New(nil, nil, db, testCfg(), Options{})
	defer s.Close()
	ok, err := s.ClaimSubtitleCheck(t.Context(), "same")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	other := New(nil, nil, db, testCfg(), Options{})
	defer other.Close()
	ok, err = other.ClaimSubtitleCheck(t.Context(), "same")
	if err != nil || ok {
		t.Fatal("duplicate probe", ok, err)
	}
}

func TestHardsubAllowPolicy(t *testing.T) {
	cfg := testCfg()
	cfg.Resolver.HardSubtitles = "allow"
	hc := rel("hc", "The.Signal.1999.1080p.HC.WEBRip.x264-GROUP", 8000)
	clean := rel("clean", "The.Signal.1999.720p.WEBRip.x264-GROUP", 4000)
	s := New(&fakeIndexer{releases: []core.Release{hc, clean}}, fakeHealth{}, nil, cfg, Options{})
	defer s.Close()
	got, err := s.Resolve(t.Context(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()})
	if err != nil || got.Release.ID != "hc" {
		t.Fatal("allow should preserve quality ranking", got, err)
	}
}
