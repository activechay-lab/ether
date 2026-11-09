package resolver

// The Radarr/Sonarr-standard ranking (docs/quality-ranking.md): the
// comparer order, the streaming gates in front of it, custom format
// scoring as Radarr and Sonarr do it, and an imported TRaSH-style pack
// ranking the way its authors wrote it.

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// arrCand is a candidate of the given quality, ready for compareCandidates.
func arrCand(profile core.QualityProfile, name string, cf int, mutate ...func(*core.Release)) candidate {
	rel := parseRelease(name, 0)
	for _, m := range mutate {
		m(&rel)
	}
	rc := rankContext{profile: profile, now: corpusNow, preferRevision: true}
	return candidate{rel: rel, langOK: true, cf: cf, keys: keysFor(rel, false, rc)}
}

func TestComparerFollowsDownloadDecisionComparer(t *testing.T) {
	profile := core.QualityProfile{QualityOrder: []core.QualityItem{
		{Qualities: []string{"remux-2160"}},
		{Qualities: []string{"bluray-1080"}},
		{Name: "WEB 2160p", Qualities: []string{"webdl-2160", "webrip-2160"}},
		{Qualities: []string{"webdl-1080"}},
	}}
	before := func(a, b candidate, why string) {
		t.Helper()
		if !compareCandidates(a, b) || compareCandidates(b, a) {
			t.Errorf("%s: %q should rank before %q", why, a.rel.Name, b.rel.Name)
		}
	}
	bd1080 := arrCand(profile, "Film.2026.1080p.BluRay.x264-GRP", 0)
	web2160 := arrCand(profile, "Film.2026.2160p.WEB-DL.DDP5.1.HEVC-GRP", 5000)
	before(bd1080, web2160, "quality position beats any custom format score")
	webrip := arrCand(profile, "Film.2026.2160p.WEBRip.x265-GRP", 5001)
	before(webrip, web2160, "qualities in one group tie, so the score decides")
	proper := arrCand(profile, "Film.2026.PROPER.1080p.BluRay.x264-GRP", 0)
	higher := arrCand(profile, "Film.2026.1080p.BluRay.x264-OTHER", 900)
	before(proper, higher, "a revision outranks the score when the profile prefers revisions")
	usenet := arrCand(profile, "Film.2026.1080p.BluRay.x264-GRP", 10)
	torrent := arrCand(profile, "Film.2026.1080p.BluRay.x264-GRP", 10, func(r *core.Release) { r.Protocol = core.ProtocolTorrent })
	preferDebrid := rankContext{profile: profile, now: corpusNow, preferSource: PreferDebrid}
	torrent.keys = keysFor(torrent.rel, false, preferDebrid)
	usenet.keys = keysFor(usenet.rel, false, preferDebrid)
	before(torrent, usenet, "the preferred protocol follows the score")
	pri := func(p int) candidate {
		c := arrCand(profile, "Film.2026.1080p.BluRay.x264-GRP", 10)
		c.keys.priority = p
		return c
	}
	before(pri(1), pri(25), "a lower indexer priority ranks first")
	seeded := func(n int) candidate {
		return arrCand(profile, "Film.2026.1080p.BluRay.x264-GRP", 10, func(r *core.Release) { r.Protocol, r.Seeders = core.ProtocolTorrent, n })
	}
	before(seeded(1000), seeded(10), "more seeders, in orders of magnitude")
	if compareCandidates(seeded(110), seeded(90)) || compareCandidates(seeded(90), seeded(110)) {
		t.Error("seeders within one order of magnitude tie")
	}
	aged := func(age time.Duration) candidate {
		return arrCand(profile, "Film.2026.1080p.BluRay.x264-GRP", 10, func(r *core.Release) { r.PubDate = corpusNow.Add(-age) })
	}
	before(aged(2*time.Hour), aged(400*24*time.Hour), "a newer Usenet post wins")
	sized := func(gb int64) candidate {
		return arrCand(profile, "Film.2026.1080p.BluRay.x264-GRP", 10, func(r *core.Release) { r.Size = gb << 30 })
	}
	before(sized(20), sized(10), "larger wins without a preferred size")
	verified, plain := sized(10), sized(10)
	verified.verified = true
	before(verified, plain, "played fine recently breaks a full tie")
}

// Season packs compete with single-episode releases like any release:
// Sonarr's pack-first keys are for downloading seasons, not streaming one
// episode. A single episode wins only the final tie-break.
func TestComparerSeasonPacksCompeteLikeAnyRelease(t *testing.T) {
	profile := core.QualityProfile{QualityOrder: []core.QualityItem{{Qualities: []string{"webdl-2160"}}, {Qualities: []string{"webdl-1080"}}}}
	rc := rankContext{profile: profile, title: episodeTitle(), now: corpusNow}
	ep := func(name string, cf int, size int64) candidate {
		rel := parseRelease(name, 0)
		rel.Protocol, rel.Size = core.ProtocolTorrent, size<<30
		return candidate{rel: rel, langOK: true, cf: cf, keys: keysFor(rel, false, rc)}
	}
	single := ep("Boundary.Bay.S01E03.1080p.WEB-DL.H.264-GRP", 10, 2)
	if pack := ep("Boundary.Bay.S01.2160p.WEB-DL.H.265-GRP", 10, 40); !compareCandidates(pack, single) {
		t.Error("a pack of a better quality ranks first")
	}
	if pack := ep("Boundary.Bay.S01.1080p.WEB-DL.H.264-GRP", 20, 40); !compareCandidates(pack, single) {
		t.Error("a pack with the higher custom format score ranks first")
	}
	pack := ep("Boundary.Bay.S01.1080p.WEB-DL.H.264-GRP", 10, 40)
	if !compareCandidates(single, pack) || compareCandidates(pack, single) {
		t.Error("an otherwise equal single episode beats a pack, whose season size never compares")
	}
	double := ep("Boundary.Bay.S01E03E04.1080p.WEB-DL.H.264-GRP", 10, 2)
	if compareCandidates(single, double) || compareCandidates(double, single) {
		t.Error("episode count is not a key")
	}
}

func TestStreamingGatesRankBeforeQuality(t *testing.T) {
	profile := core.QualityProfile{QualityOrder: []core.QualityItem{{Qualities: []string{"remux-2160"}}, {Qualities: []string{"webdl-1080"}}}}
	remux := arrCand(profile, "Film.2026.2160p.BluRay.REMUX.HEVC-GRP", 900)
	web := arrCand(profile, "Film.2026.1080p.WEB-DL.H.264-GRP", 0)
	if !compareCandidates(remux, web) {
		t.Fatal("quality first when every gate passes")
	}
	tooBig := remux
	tooBig.bandwidth = -1
	if !compareCandidates(web, tooBig) {
		t.Error("a release the connection can't carry ranks after one it can")
	}
	foreign := remux
	foreign.langOK = false
	if !compareCandidates(web, foreign) {
		t.Error("a release in no accepted language ranks after one in an accepted language")
	}
	fallback := remux
	fallback.policy.Fallback = 1
	if !compareCandidates(web, fallback) {
		t.Error("an Only-play rule's fallback tier ranks after its primary releases")
	}
}

// Formats score as Radarr and Sonarr score them: languages aggregated like
// AggregateLanguages, each app's release-title form, unlisted formats 0.
func TestCustomFormatsScoreLikeRadarrAndSonarr(t *testing.T) {
	english := &core.Title{Title: "Obsession", Language: "en"}
	defs := []core.CustomFormat{{ID: "en", Name: "English", App: AppRadarr, Scores: map[string]int{"default": 10},
		Specifications: []core.FormatSpecification{{Name: "english", Implementation: "LanguageSpecification", Fields: map[string]json.RawMessage{"value": []byte("1")}}}}}
	s, err := NewFormatScorer(defs)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name  string
		score int
		langs []string
	}{
		{"Obsession.2026.MULTi.1080p.WEB-DL.H.264-GRP", 10, []string{"english"}},
		{"Obsession.2026.German.DL.1080p.BluRay.x264-GRP", 10, []string{"german", "english"}},
		{"Obsession.2026.GERMAN.ML.1080p.WEB.x264-GRP", 10, []string{"german", "english"}},
		{"Obsession.2026.GERMAN.WEB.DL.1080p.x264-GRP", 0, []string{"german"}}, // WEB-DL is not a dual-language tag
		{"Obsession.2026.1080p.BluRay.x264-GRP", 10, []string{"english"}},
		{"Obsession.2026.FRENCH.1080p.BluRay.x264-GRP", 0, []string{"french"}},
	} {
		rel := parseRelease(tt.name, 0)
		if got := formatLanguages(rel, english); !slices.Equal(got, tt.langs) {
			t.Errorf("%s: languages %v want %v", tt.name, got, tt.langs)
		}
		if got, _, _ := s.ScoreForProfile(rel, map[string]int{"en": 10}, english); got != tt.score {
			t.Errorf("%s: score %d want %d", tt.name, got, tt.score)
		}
		if got, _, _ := s.ScoreForProfile(rel, nil, english); got != 0 {
			t.Errorf("%s: a format the profile does not list scored %d", tt.name, got)
		}
	}
	// Radarr hides the movie's own title behind "A.Movie"; Sonarr matches
	// the release title as it is.
	rel := parseRelease("The.Matrix.1999.1080p.BluRay.x264-DON.mkv", 0)
	for app, want := range map[string]int{AppRadarr: 0, AppSonarr: 1} {
		s, err := NewFormatScorer([]core.CustomFormat{{ID: "m", Name: "M", App: app,
			Specifications: []core.FormatSpecification{{Name: "m", Implementation: "ReleaseTitleSpecification", Fields: map[string]json.RawMessage{"value": []byte(`"matrix"`)}}}}})
		if err != nil {
			t.Fatal(err)
		}
		if got, _, _ := s.ScoreForProfile(rel, map[string]int{"m": 1}, nil); got != want {
			t.Errorf("%s title input: %d want %d", app, got, want)
		}
	}
}

// An imported TRaSH-style pack ranks with its own quality order and format
// scores: Bluray-1080p above the WEB group, tiers by score within it, the
// minimum score rejecting what the pack's -10000 formats mark, and its
// Repack/Proper format replacing revision preference ("Do not prefer").
func TestImportedPackRanksAsItsAuthorsWrote(t *testing.T) {
	pack := []byte(`{"name":"HD Bluray + WEB","trash_id":"hd","language":"Original","minFormatScore":0,
 "items":[{"name":"Bluray-1080p","allowed":true},{"name":"WEB 1080p","allowed":true,"items":["WEBDL-1080p","WEBRip-1080p"]},{"name":"Bluray-720p","allowed":false}],
 "formatItems":{"WEB Tier 01":"web-t1","WEB Tier 02":"web-t2","LQ":"lq","Repack/Proper":"repack"}}`)
	imp, err := ImportQualityProfile(pack)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.QualityItem{{Name: "Bluray-1080p", Qualities: []string{"bluray-1080"}}, {Name: "WEB 1080p", Qualities: []string{"webdl-1080", "webrip-1080"}}}
	if !slices.EqualFunc(imp.Profile.QualityOrder, want, func(a, b core.QualityItem) bool { return a.Name == b.Name && slices.Equal(a.Qualities, b.Qualities) }) {
		t.Fatalf("quality order %+v", imp.Profile.QualityOrder)
	}
	cfg := testCfg()
	cfg.Resolver.CustomFormats = []core.CustomFormat{
		{ID: "web-t1", Name: "WEB Tier 01", App: AppRadarr, Specifications: []core.FormatSpecification{{Name: "g", Implementation: "ReleaseGroupSpecification", Fields: map[string]json.RawMessage{"value": []byte(`"^(FLUX|NTb)$"`)}}}},
		{ID: "web-t2", Name: "WEB Tier 02", App: AppRadarr, Specifications: []core.FormatSpecification{{Name: "g", Implementation: "ReleaseGroupSpecification", Fields: map[string]json.RawMessage{"value": []byte(`"^(SiGMA)$"`)}}}},
		{ID: "lq", Name: "LQ", App: AppRadarr, Specifications: []core.FormatSpecification{{Name: "g", Implementation: "ReleaseGroupSpecification", Fields: map[string]json.RawMessage{"value": []byte(`"^(YIFY)$"`)}}}},
		{ID: "repack", Name: "Repack/Proper", App: AppRadarr, Specifications: []core.FormatSpecification{{Name: "r", Implementation: "ReleaseTitleSpecification", Fields: map[string]json.RawMessage{"value": []byte(`"\\b(repack\\d?|proper)\\b"`)}}}},
	}
	profile := imp.Profile
	profile.MediaType = core.MediaMovie
	for id, v := range map[string]int{"web-t1": 1700, "web-t2": 1650, "lq": -10000, "repack": 5} {
		profile.CustomFormatScores[id] = v
	}
	cfg.Quality.Profiles = []core.QualityProfile{profile}
	cfg.Quality.Default = profile.Name
	s := New(&fakeIndexer{}, fakeHealth{}, nil, cfg, Options{Now: func() time.Time { return corpusNow }})
	defer s.Close()
	caps := appleTV4K()
	selected := SelectProfileForMedia(cfg.Quality, caps, nil, core.MediaMovie)
	title := signal2026()
	releases := []core.Release{
		rel("web-sigma", "The.Signal.2026.1080p.WEB-DL.DDP5.1.H.264-SiGMA", 6<<10),
		rel("web-flux", "The.Signal.2026.1080p.WEB-DL.DDP5.1.H.264-FLUX", 6<<10),
		rel("bluray", "The.Signal.2026.1080p.BluRay.DD5.1.x264-GRP", 9<<10),
		rel("lq", "The.Signal.2026.1080p.BluRay.x264-YIFY", 2<<10),
		rel("web-flux-proper", "The.Signal.2026.PROPER.1080p.WEBRip.DDP5.1.x264-SiGMA", 6<<10),
	}
	for i := range releases {
		releases[i] = withParsed(releases[i])
		releases[i].PubDate = corpusNow.Add(-30 * 24 * time.Hour)
	}
	cands, _, err := s.rankCandidates(context.Background(), core.ResolveRequest{Title: title, Client: caps}, searchRequestFor(title), selected, releases, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range cands {
		ids = append(ids, c.rel.ID)
	}
	// WEB Tier 01 (1700) > Tier 02 + Repack (1655) > Tier 02 (1650); LQ is
	// below the minimum of 0. The PROPER doesn't jump its tier: the pack
	// scores revisions with its own format.
	if want := []string{"bluray", "web-flux", "web-flux-proper", "web-sigma"}; !slices.Equal(ids, want) {
		t.Fatalf("ranked %v, want %v", ids, want)
	}
}

func TestUsenetAgeBuckets(t *testing.T) {
	now := time.Now()
	for _, tt := range []struct {
		age  time.Duration
		want float64
	}{{30 * time.Minute, 1000}, {5 * time.Hour, 100}, {3 * 24 * time.Hour, 10}, {100 * 24 * time.Hour, -2}, {1000 * 24 * time.Hour, -3}} {
		if got := usenetAgeScore(now.Add(-tt.age), now); got != tt.want {
			t.Errorf("age %s: %v want %v", tt.age, got, tt.want)
		}
	}
}
