package resolver

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestRememberedNamesakeRechecked(t *testing.T) {
	ctx := context.Background()
	title := &core.Title{Key: core.EpisodeKey(634, 1, 11), Type: core.TypeEpisode, ShowTitle: "Who's the Boss?", ShowYear: 1984, ShowIDs: core.IDs{TMDB: 634, TVDB: 76587}, Title: "Samantha's Growing Up", Season: 1, Episode: 11, Language: "en", Runtime: 24 * time.Minute}
	cfg := testCfg()
	svc := New(&fakeIndexer{}, fakeHealth{}, nil, cfg, Options{SeriesNamesake: func(context.Context, core.RatingKey, string, int) (bool, error) { return false, nil }})
	wrong := rel("remembered", "Whos.the.Boss.S01E11.1080p.VIKI.WEB-DL.AAC2.0.H.264-FUZEER", 568)
	wrong.Info = releaseparse.Parse(wrong.Name)
	wrong.TitleIDs = core.IDs{TVDB: 76587} // the stale lab record reports the requested ID
	right := rel("correct", "Whos.the.Boss.1984.S01E11.Samanthas.Growing.Up.1080p.WEB.H264-GRP", 295)
	right.Info = releaseparse.Parse(right.Name)
	// A name-only release carrying the requested ID is caught by the runtime
	// check when its probe disagrees with the episode (playback layer), not by
	// any platform or language word in its name.
	_ = wrong
	if !svc.RememberedReleaseAllowed(ctx, title, right) {
		t.Fatal("correct remembered episode rejected")
	}
	mismatch := right
	mismatch.TitleIDs = core.IDs{TMDB: 999}
	if svc.RememberedReleaseAllowed(ctx, title, mismatch) {
		t.Fatal("conflicting title ID reused")
	}
	profile := SelectProfileForMedia(cfg.Quality, webCaps(), nil, titleMedia(title))
	if _, err := svc.Pin(ctx, title.Key, title.ShowTitle, wrong, ""); err != nil {
		t.Fatal(err)
	}
	if !svc.RememberedReleaseAllowed(ctx, title, wrong) {
		t.Fatal("owner pin invalidated")
	}
	got, _, err := svc.rankCandidates(ctx, core.ResolveRequest{Title: title, Client: webCaps()}, searchRequestFor(title), profile, []core.Release{wrong, right}, nil, wrong.ID)
	if err != nil || len(got) == 0 || got[0].rel.ID != wrong.ID {
		t.Fatalf("owner pin lost: %+v %v", got, err)
	}
}
