package resolver

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// Identity must use the show's original title while preserving episode, season,
// year and protocol restrictions, including explicit coverage of partial packs.
func TestRussianOriginalTitleIdentity(t *testing.T) {
	title := &core.Title{Type: core.TypeEpisode, Title: "Episode 1", ShowTitle: "Doctor Kracsnov", ShowOriginalTitle: "Доктор Краснов"}
	req := core.SearchRequest{Type: core.TypeEpisode, Title: title.ShowTitle, Year: 2023, Season: 1, Episode: 1}
	for _, tc := range []struct {
		name            string
		episode, season int
		protocol, want  string
	}{
		{"01. Доктор Краснов.2023.WEB-DL 1080p.Files-x", 1, 1, core.ProtocolTorrent, ""},
		{"01.Доктор Краснов.(2023).WEBRip.(AVC).by.Серый1779.Files-x", 1, 1, core.ProtocolTorrent, ""},
		{"01. Доктор Краснов.2023.WEBRip", 1, 1, core.ProtocolTorrent, ""},
		{"Doktor.Krasnov.S01.E01.2023.WEB-DLRip.DenSBK", 1, 1, core.ProtocolTorrent, ""},
		{"Доктор Краснов [S01] (2023) WEBRip от Generalfilm | КПК", 1, 1, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 1 из 16 [2023 WEBRip-AVC]", 1, 1, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 16 из 16 [2023 WEBRip 1080p]", 1, 1, core.ProtocolTorrent, "season/episode mismatch"},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip]", 1, 1, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 2 / Серии: 1-16 из 16 [2025 WEBRip 1080p]", 1, 2, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 2 / Серии: 1-16 из 16 [2025 WEBRip-AVC]", 1, 2, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 2 / Серии: 1-16 из 16 [2025 WEBRip]", 1, 2, core.ProtocolTorrent, ""},
		{"Доктор Краснов [S02] (2025) WEBRip 1080p от Files-x", 1, 2, core.ProtocolTorrent, ""},
		{"Доктор Краснов [02x01-12 из 16] (2025) WEBRip 1080p от Files-x", 1, 2, core.ProtocolTorrent, ""},
		{"Доктор Краснов [02x01-12 из 16] (2025) WEBRip 1080p от Files-x", 12, 2, core.ProtocolTorrent, ""},
		{"Доктор Краснов [02x01-12 из 16] (2025) WEBRip 1080p от Files-x", 13, 2, core.ProtocolTorrent, "season/episode mismatch"},
		{"17.Доктор Краснов.s02.E01.(2025).WEBRip (1080p).by.Nicodem.Files-x", 1, 2, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip-AVC]", 16, 1, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 3-5 из 16 [2023 WEBRip]", 1, 1, core.ProtocolTorrent, "season/episode mismatch"},
		{"Доктор Краснов / Сезон: 1 / Серии: 3-5 из 16 [2023 WEBRip]", 4, 1, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip]", 1, 2, core.ProtocolTorrent, "season/episode mismatch"},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip]", 1, 1, core.ProtocolUsenet, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip]", 1, 1, core.ProtocolHTTP, "season pack (skipped in v1)"},
		{"Doktor.Krasnikov.S01.E01.2023.WEB-DL", 1, 1, core.ProtocolTorrent, `title mismatch (parsed "Doktor Krasnikov"; expected "Doctor Kracsnov" or "Доктор Краснов")`},
		{"Doktor.Krasnov.2024.S01.E01.WEB-DL", 1, 1, core.ProtocolTorrent, "series year mismatch"},
		{"No results! Make sure to use the IMDb ID to search", 1, 1, core.ProtocolTorrent, "indexer placeholder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req.Episode, req.Season, req.EpisodeTitle = tc.episode, tc.season, fmt.Sprintf("Episode %d", tc.episode)
			r := core.Release{ID: "russian", Name: tc.name, Info: releaseparse.Parse(tc.name), Protocol: tc.protocol}
			if got := rejectIdentity(r, title, req, ""); got != tc.want {
				t.Fatalf("rejectIdentity = %q, want %q; parsed %+v", got, tc.want, r.Info)
			}
			if tc.want == "" && r.Info.FullSeason {
				target := withTarget(r, req)
				if target.Target == nil || target.Target.Episode != req.Episode || target.Target.Season != req.Season {
					t.Fatalf("pack missing episode target: %+v", target.Target)
				}
			}
		})
	}
}

type krasnovIndexer struct {
	*fakeIndexer
	fail map[string]error
}

func (k *krasnovIndexer) FetchNZB(_ context.Context, r core.Release) ([]byte, error) {
	if err, ok := k.fail[r.Name]; ok {
		return nil, err
	}
	return []byte("payload:" + r.ID), nil
}

// Issue #335: when a 1080p candidate passes the strict 720p/1080p floor during
// ranking and then fails validation (permanently via infringing_file or
// transiently via ErrNotCached), FallbackBelowMin must still step down to
// playable unlabelled WEBRip (480p floor) releases; meanwhile synthetic
// "No results! Make sure to use the IMDb ID to search" rows are stripped.
func TestFallbackBelowMinAfterStrictCandidateFailsAndPlaceholderFiltered(t *testing.T) {
	epS01 := &core.Title{
		Key: core.EpisodeKey(234444, 1, 1), ShowKey: core.ShowKey(234444), Type: core.TypeEpisode,
		Title: "Серия 1", ShowTitle: "Doctor Kracsnov", ShowOriginalTitle: "Доктор Краснов",
		ShowYear: 2023, Season: 1, Episode: 1, Runtime: 48 * time.Minute,
	}
	epS02 := &core.Title{
		Key: core.EpisodeKey(234444, 2, 1), ShowKey: core.ShowKey(234444), Type: core.TypeEpisode,
		Title: "Серия 1", ShowTitle: "Doctor Kracsnov", ShowOriginalTitle: "Доктор Краснов",
		ShowYear: 2023, Season: 2, Episode: 1, Runtime: 48 * time.Minute,
	}

	for _, tc := range []struct {
		name     string
		title    *core.Title
		releases []core.Release
		fail     map[string]error
		wantName string
	}{
		{
			name:  "S01E01 1080p infringing_file falls back to cached WEBRip",
			title: epS01,
			releases: []core.Release{
				torrent(rel("placeholder", "No results! Make sure to use the IMDb ID to search", 0)),
				torrent(rel("hd-infringing", "01. Доктор Краснов.2023.WEB-DL 1080p.Files-x", 1600)),
				torrent(rel("sd-webdlrip", "Doktor.Krasnov.S01.E01.2023.WEB-DLRip.DenSBK", 700)),
			},
			fail: map[string]error{
				"01. Доктор Краснов.2023.WEB-DL 1080p.Files-x": fmt.Errorf("infringing_file: %w", core.ErrReleaseRejected),
			},
			wantName: "Doktor.Krasnov.S01.E01.2023.WEB-DLRip.DenSBK",
		},
		{
			name:  "S02E01 1080p uncached falls back to cached WEBRip pack",
			title: epS02,
			releases: []core.Release{
				torrent(rel("s2-1080", "Доктор Краснов / Сезон: 2 / Серии: 1-16 из 16 [2025 WEBRip 1080p]", 14000)),
				torrent(rel("s2-sd", "Доктор Краснов / Сезон: 2 / Серии: 1-16 из 16 [2025 WEBRip]", 6500)),
			},
			fail: map[string]error{
				"Доктор Краснов / Сезон: 2 / Серии: 1-16 из 16 [2025 WEBRip 1080p]": fmt.Errorf("debrid: %w", core.ErrNotCached),
			},
			wantName: "Доктор Краснов / Сезон: 2 / Серии: 1-16 из 16 [2025 WEBRip]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testCfg()
			cfg.Debrid.CachedOnly = true
			for i := range cfg.Quality.Profiles {
				cfg.Quality.Profiles[i].MinHeight = 720
				cfg.Quality.Profiles[i].FallbackBelowMin = true
			}
			idx := &krasnovIndexer{fakeIndexer: &fakeIndexer{releases: tc.releases}, fail: tc.fail}
			svc := New(idx, fakeHealth{}, nil, cfg, Options{})
			defer svc.Close()

			search, err := svc.SearchReleases(t.Context(), tc.title, SearchOptions{Fresh: true})
			if err != nil {
				t.Fatalf("SearchReleases: %v", err)
			}
			for _, cr := range search.Releases {
				if strings.Contains(cr.Release.Name, "Make sure to use the IMDb ID") {
					t.Fatalf("placeholder leaked into SearchReleases: %+v", cr)
				}
			}

			res, err := svc.Resolve(t.Context(), core.ResolveRequest{Title: tc.title, Client: webCaps()})
			if err != nil {
				t.Fatalf("Resolve failed: %v; traces=%+v", err, svc.Traces())
			}
			if res.Release.Name != tc.wantName || res.QualityFallback == "" {
				t.Fatalf("got release=%q fallback=%q; want %q with QualityFallback", res.Release.Name, res.QualityFallback, tc.wantName)
			}
			for _, ct := range svc.Traces()[0].Candidates {
				if strings.Contains(ct.Name, "Make sure to use the IMDb ID") {
					t.Fatalf("placeholder leaked into trace: %+v", ct)
				}
			}
		})
	}
}
