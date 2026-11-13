package resolver

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestNamesakeRequiresCorroborationWhenAvailable(t *testing.T) {
	title := &core.Title{Type: core.TypeEpisode, ShowTitle: "Who's the Boss?", ShowYear: 1984, ShowIDs: core.IDs{TMDB: 634}, Title: "Samantha's Growing Up", Season: 1, Episode: 11, Runtime: 24 * time.Minute}
	req := searchRequestFor(title)
	cfg := testCfg()
	svc := New(&fakeIndexer{}, fakeHealth{}, nil, cfg, Options{SeriesNamesake: func(context.Context, core.RatingKey, string, int) (bool, error) { return true, nil }})
	profile := SelectProfileForMedia(cfg.Quality, webCaps(), nil, titleMedia(title))
	wrong := rel("namesake", "Whos.the.Boss.S01E11.1080p.VIKI.WEB-DL.AAC2.0.H.264-FUZEER", 568)
	wrong.Info = releaseparse.Parse(wrong.Name)
	for _, tc := range []struct {
		name string
		ids  core.IDs
	}{
		{"Who's the Boss S1E11 Samantha s growing up 1080p", core.IDs{}},
		{"Whos.the.Boss-S01E11.Samanthas.Growing.Up", core.IDs{}},
		{"Whos.the.Boss.1984.S01E11.720p.WEB.H264-GRP", core.IDs{}},
		{"Whos.the.Boss.S01E11.720p.WEB.H264-GRP", core.IDs{TMDB: 634}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			right := rel("correct", tc.name, 295)
			right.Info, right.TitleIDs = releaseparse.Parse(right.Name), tc.ids
			if episodeIdentityEvidence(wrong, req) || !episodeIdentityEvidence(right, req) {
				t.Fatal("incorrect evidence classification")
			}
			trace := &Trace{}
			got, _, err := svc.rankCandidates(context.Background(), core.ResolveRequest{Title: title, Client: webCaps()}, req, profile, []core.Release{wrong, right}, trace, "")
			if err != nil || len(got) != 1 || got[0].rel.ID != "correct" {
				t.Fatalf("namesake remained eligible: %+v %v trace=%+v", got, err, trace)
			}
			got, _, err = svc.rankCandidates(context.Background(), core.ResolveRequest{Title: title, Client: webCaps(), Exclude: []string{"correct"}}, req, profile, []core.Release{wrong, right}, nil, "")
			if err != nil || len(got) != 0 {
				t.Fatalf("failover promoted namesake: %+v %v", got, err)
			}
		})
	}
	for _, name := range []string{"Whos.the.Boss.S01E11.1080p.WEB.H264-GRP", "Whos.the.Boss.2024.S01E11.1080p.WEB.H264-GRP", "Whos.the.Boss.S01E12.Samanthas.Growing.Up.1080p.WEB.H264-GRP"} {
		r := rel("other", name, 568)
		r.Info = releaseparse.Parse(name)
		if rejectIdentity(r, title, req, "") == "" && episodeIdentityEvidence(r, req) {
			t.Fatalf("wrong show/episode corroborated: %s", name)
		}
	}
}
