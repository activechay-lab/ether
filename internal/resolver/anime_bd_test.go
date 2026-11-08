package resolver

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestAnimeBDPassesIdentityAndImportedQuality(t *testing.T) {
	cases := []struct {
		name, title string
		episode     int
	}{
		{"[Serendipity] Lovely Complex - S01E01 (BD 1080p HEVC Opus 2.0) [Dual Audio] [7BBF1985]", "Freshman Year Summer! I'll Definitely Find a Boyfriend!", 1},
		{"Lovely Complex S01E01 BD 1080p HEVC Opus 2.0 VOSTFR", "Freshman Year Summer! I'll Definitely Find a Boyfriend!", 1},
		{"Lovely.Complex.S01E01.Freshman.Year.Summer.I.ll.Definitely.Find.a.Boyfriend.ITA.ENG.JAP.1080p.BDMux.AC3.2.0.h265", "Freshman Year Summer! I'll Definitely Find a Boyfriend!", 1},
		{"Lovely.Complex.S01E02.The.Ex.Girlfriend.Love.Triangle.ITA.ENG.JAP.1080p.BDMux.AC3.2.0.h265", "The Ex-Girlfriend Love Triangle?!", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			title := &core.Title{Key: core.EpisodeKey(100, 1, tc.episode), Type: core.TypeEpisode, Title: tc.title, ShowTitle: "Lovely★Complex", ShowYear: 2007, Season: 1, Episode: tc.episode, Runtime: 24 * time.Minute}
			cfg := testCfg()
			cfg.Quality.Profiles = []core.QualityProfile{{Name: "imported", AllowedQualities: []string{"bluray-1080"}, MaxHeight: 1080}}
			cfg.Quality.Default = "imported"
			cfg.Quality.Rules = nil
			cfg.Quality.Remote = ""
			r := rel("bd", tc.name, 500)
			idx := &fakeIndexer{releases: []core.Release{r}}
			svc := New(idx, fakeHealth{}, nil, cfg, Options{})
			defer svc.Close()
			got, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: title, Client: core.ClientCaps{MaxHeight: 1080, VideoCodecs: []string{"hevc", "h264"}}})
			if err != nil || got.Release.ID != "bd" {
				t.Fatalf("resolve=%+v err=%v", got, err)
			}
			parsed := withParsed(r)
			req := searchRequestFor(title)
			req.Episode = 3
			if reason := rejectIdentity(parsed, title, req, ""); reason != "season/episode mismatch" {
				t.Fatalf("wrong episode accepted: %q", reason)
			}
		})
	}
}

type fakeAnimeCour struct {
	cour int
	ep   int
}

func (f fakeAnimeCour) LookupCour(_ context.Context, req core.SearchRequest) (int, int, bool, error) {
	if req.IDs.TMDB == 1000 && req.Season == 1 && req.Episode == 13 {
		return f.cour, f.ep, true, nil
	}
	return 0, 0, false, nil
}

func TestAnimeMultiCourResolveAndReject(t *testing.T) {
	titleEp13 := &core.Title{
		Key:                   core.EpisodeKey(1000, 1, 13),
		Type:                  core.TypeEpisode,
		Title:                 "It's Too Late",
		ShowTitle:             "86",
		ShowAlternativeTitles: []string{"Eighty-Six"},
		ShowYear:              2021,
		Season:                1,
		Episode:               13,
		Language:              "ja",
		Genres:                []string{"Animation"},
		Runtime:               24 * time.Minute,
	}
	part1Ep1 := rel("p1e1", "[SubsPlease] Eighty-Six S01 Part 1 - 01 (WEB 1080p) [1234ABCD].mkv", 1400)
	part2Ep1 := rel("p2e1", "[SubsPlease] Eighty-Six S01 Part 2 - 01 (WEB 1080p) [ABCD1234].mkv", 1400)
	part2Ep1.Protocol = core.ProtocolTorrent
	idx := &fakeIndexer{releases: []core.Release{part1Ep1, part2Ep1}}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{AnimeCour: fakeAnimeCour{cour: 2, ep: 1}})
	defer svc.Close()

	got, err := svc.Resolve(context.Background(), core.ResolveRequest{
		Title:  titleEp13,
		Client: core.ClientCaps{MaxHeight: 1080, VideoCodecs: []string{"h264", "hevc"}},
	})
	if err != nil || got.Release.ID != "p2e1" {
		t.Fatalf("expected p2e1 for S01E13 (Cour 2 Ep 1), got %+v err=%v", got, err)
	}
	if got.Release.Target == nil || got.Release.Target.Cour != 2 || got.Release.Target.CourEpisode != 1 {
		t.Fatalf("expected Target Cour=2 CourEpisode=1, got %+v", got.Release.Target)
	}

	// S01E01 (Cour 1) must reject Part 2 Episode 1.
	titleEp1 := *titleEp13
	titleEp1.Key = core.EpisodeKey(1000, 1, 1)
	titleEp1.Episode = 1
	sreqEp1 := svc.searchRequest(context.Background(), &titleEp1)
	if reason := rejectIdentity(withParsed(part2Ep1), &titleEp1, sreqEp1, ""); reason != "season/episode mismatch" {
		t.Fatalf("expected Part 2 Episode 1 to be rejected for S01E01, got %q", reason)
	}
	mapped := svc.searchRequest(context.Background(), titleEp13)
	firstCour := withParsed(part1Ep1).Info
	firstCour.Episodes = []int{13}
	if episodeMatchesRequest(firstCour, mapped) {
		t.Fatal("first cour matched an episode mapped to the second cour")
	}
	pack := withParsed(part2Ep1)
	pack.Info.FullSeason = true
	pack.Info.Episodes = []int{1, 2}
	if reason := rejectIdentity(pack, titleEp13, mapped, ""); reason != "" {
		t.Fatalf("mapped cour episode range rejected: %s", reason)
	}
}
