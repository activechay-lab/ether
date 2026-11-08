package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// The ranking corpus records which releases Ether picked, in order, for a
// deterministic set of realistic release pools, profiles and devices. The
// golden file was written by the ranking that profiles used before the
// Radarr/Sonarr-standard model (docs/quality-ranking.md); the stored
// profile fixtures below are in that older format and reach the ranker
// through config.Open, so every run converts them exactly as an upgrading
// install does. ETHER_WRITE_RANKING_GOLDEN=1 rewrites the golden file.

const rankingGoldenPath = "testdata/ranking_corpus_golden.json"

// corpusNow is the fixed clock every corpus release is aged against.
var corpusNow = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

// corpusProfiles are stored configs as installs hold them: the shipped
// 4K/1080p/720p profiles plus the variants owners commonly made.
var corpusProfiles = []struct{ name, doc string }{
	{"4K", `{"quality":{"default":"4K","profiles":[{"name":"4K","mediaType":"movie","maxHeight":2160,"minHeight":720,"allowHdr":true,"allowDolbyVision":true,"preferredSources":["remux","bluray","webdl","webrip"],"preferredCodecs":["hevc","av1","h264"],"preferredAudio":["truehd","eac3","dtshd","ac3","aac"],"minSizeMbPerMin":15,"maxSizeMbPerMin":1000,"preferredLanguages":null,"allowCam":false,"ranking":["language","connection","resolution","releaseType","hdr","customScore","verified","videoCodec","audioFormat","size"]}]}}`},
	{"1080p", `{"quality":{"default":"1080p","profiles":[{"name":"1080p","mediaType":"movie","maxHeight":1080,"minHeight":720,"allowHdr":false,"allowDolbyVision":false,"preferredSources":["webdl","bluray","webrip","hdtv"],"preferredCodecs":["h264","hevc"],"preferredAudio":["eac3","ac3","aac"],"minSizeMbPerMin":5,"maxSizeMbPerMin":150,"preferredLanguages":null,"allowCam":false,"ranking":["language","connection","resolution","releaseType","hdr","customScore","verified","videoCodec","audioFormat","size"]}]}}`},
	{"720p", `{"quality":{"default":"720p","profiles":[{"name":"720p","mediaType":"movie","maxHeight":720,"minHeight":480,"allowHdr":false,"allowDolbyVision":false,"preferredSources":["webdl","webrip","bluray","hdtv"],"preferredCodecs":["h264"],"preferredAudio":["aac","ac3","eac3"],"minSizeMbPerMin":2,"maxSizeMbPerMin":60,"preferredLanguages":null,"allowCam":false,"ranking":["language","connection","resolution","releaseType","hdr","customScore","verified","videoCodec","audioFormat","size"]}]}}`},
	{"4K-french", `{"quality":{"default":"4K","profiles":[{"name":"4K","mediaType":"movie","maxHeight":2160,"minHeight":720,"allowHdr":true,"allowDolbyVision":true,"preferredSources":["remux","bluray","webdl","webrip"],"preferredCodecs":["hevc","av1","h264"],"preferredAudio":["truehd","eac3","dtshd","ac3","aac"],"minSizeMbPerMin":15,"maxSizeMbPerMin":1000,"preferredLanguages":["fr"],"allowCam":false,"ranking":["language","connection","resolution","releaseType","hdr","customScore","verified","videoCodec","audioFormat","size"]}]}}`},
	{"4K-source-first", `{"quality":{"default":"4K","profiles":[{"name":"4K","mediaType":"movie","maxHeight":2160,"minHeight":720,"allowHdr":true,"allowDolbyVision":true,"preferredSources":["remux","bluray","webdl","webrip"],"preferredCodecs":["hevc","av1","h264"],"preferredAudio":["truehd","eac3","dtshd","ac3","aac"],"minSizeMbPerMin":15,"maxSizeMbPerMin":1000,"preferredLanguages":null,"allowCam":false,"ranking":["language","connection","releaseType","resolution","hdr","customScore","verified","videoCodec","audioFormat","size"]}]}}`},
	{"4K-codec-audio", `{"quality":{"default":"4K","profiles":[{"name":"4K","mediaType":"movie","maxHeight":2160,"minHeight":720,"allowHdr":true,"allowDolbyVision":true,"preferredSources":["remux","bluray","webdl","webrip"],"preferredCodecs":["hevc","av1","h264"],"preferredAudio":["truehd","eac3","dtshd","ac3","aac"],"minSizeMbPerMin":15,"maxSizeMbPerMin":1000,"preferredLanguages":null,"allowCam":false,"ranking":["language","connection","resolution","releaseType","hdr","audioFormat","videoCodec","customScore","verified","size"]}]}}`},
	{"4K-owner-formats", `{"resolver":{"customFormats":[{"id":"good-groups","name":"Good groups","specifications":[{"name":"groups","implementation":"ReleaseGroupSpecification","fields":{"value":"^(FLUX|NTb|BYNDR|FraMeSToR)$"}}]},{"id":"lq","name":"LQ","specifications":[{"name":"lq","implementation":"ReleaseGroupSpecification","fields":{"value":"^(YIFY|YTS|RARBG)$"}}]},{"id":"x265-hd","name":"x265 (HD)","specifications":[{"name":"x265","implementation":"ReleaseTitleSpecification","fields":{"value":"[xh][ ._-]?265|HEVC"}},{"name":"not 2160","implementation":"ResolutionSpecification","negate":true,"required":true,"fields":{"value":2160}}]}],"customFormatScores":{"good-groups":40,"lq":-500}},"quality":{"default":"4K","profiles":[{"name":"4K","mediaType":"movie","maxHeight":2160,"minHeight":720,"allowHdr":true,"allowDolbyVision":true,"preferredSources":["remux","bluray","webdl","webrip"],"preferredCodecs":["hevc","av1","h264"],"preferredAudio":["truehd","eac3","dtshd","ac3","aac"],"minSizeMbPerMin":15,"maxSizeMbPerMin":1000,"preferredLanguages":null,"allowCam":false,"customFormatScores":{"x265-hd":-30},"ranking":["language","connection","resolution","releaseType","hdr","customScore","verified","videoCodec","audioFormat","size"]}]}}`},
}

// corpusClients are the devices the corpus is ranked for.
var corpusClients = []struct {
	name string
	caps core.ClientCaps
}{
	{"appletv-4k-dv", core.ClientCaps{Product: "Plex for Apple TV", MaxHeight: 2160, HDR10: true, DolbyVision: true,
		VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3", "eac3", "truehd"}}},
	{"tv-4k-sdr", core.ClientCaps{Product: "Plex for Smart TV", MaxHeight: 2160,
		VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3", "eac3"}}},
	{"phone-1080", core.ClientCaps{Product: "Plex for iOS", MaxHeight: 1080, HDR10: true,
		VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3", "eac3"}}},
	{"browser-remote-8mbps", core.ClientCaps{Product: "Plex Web", MaxHeight: 1080, MaxBitrateKbps: 8000,
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}}},
}

type corpusTitle struct {
	name   string
	prefix string // release-name prefix
	title  *core.Title
}

func corpusTitles() []corpusTitle {
	return []corpusTitle{
		{"movie-en", "The.Signal.2026", &core.Title{Key: core.MovieKey(9001), Type: core.TypeMovie, IDs: core.IDs{TMDB: 9001},
			Title: "The Signal", Year: 2026, Runtime: 120 * time.Minute, Language: "en"}},
		{"movie-foreign", "El.Faro.2024", &core.Title{Key: core.MovieKey(9002), Type: core.TypeMovie, IDs: core.IDs{TMDB: 9002},
			Title: "El Faro", Year: 2024, Runtime: 110 * time.Minute, Language: "es"}},
		{"movie-animation", "Paper.Moon.2025", &core.Title{Key: core.MovieKey(9003), Type: core.TypeMovie, IDs: core.IDs{TMDB: 9003},
			Title: "Paper Moon", Year: 2025, Runtime: 95 * time.Minute, Language: "en", Genres: []string{"Animation"}, Popularity: 80}},
		{"episode", "Boundary.Bay.S01E03", &core.Title{Key: core.EpisodeKey(1396, 1, 3), Type: core.TypeEpisode,
			ShowTitle: "Boundary Bay", Season: 1, Episode: 3, Runtime: 49 * time.Minute, ShowIDs: core.IDs{TVDB: 81189}, Language: "en"}},
	}
}

// corpusRelease draws one realistic release for a title: resolution,
// source, dynamic range, codecs, languages, revision, group, protocol, age
// and a size from a bitrate that ranges from starved to generous.
func corpusRelease(rng *rand.Rand, ct corpusTitle, id int) core.Release {
	pick := func(xs ...string) string { return xs[rng.IntN(len(xs))] }
	type res struct {
		tag  string
		mbps float64
	}
	r := []res{{"2160p", 30}, {"1080p", 10}, {"720p", 4}, {"480p", 1.5}, {"", 6}}[rng.IntN(5)]
	source := pick("BluRay.REMUX", "BluRay", "WEB-DL", "WEBRip", "HDTV", "AMZN.WEB-DL", "UHD.BluRay", "")
	mult := map[string]float64{"BluRay.REMUX": 2.5, "UHD.BluRay": 1.2, "BluRay": 1.0, "WEB-DL": 0.7, "AMZN.WEB-DL": 0.7, "WEBRip": 0.5, "HDTV": 0.6, "": 0.6}[source]
	hdr := ""
	if r.tag == "2160p" || rng.IntN(6) == 0 {
		hdr = pick("", "HDR", "DV", "DV.HDR", "HDR10+", "HDR")
	}
	codec := pick("x264", "H.264", "x265", "HEVC", "H.265", "AV1")
	if source == "BluRay.REMUX" {
		codec = pick("AVC", "HEVC")
	}
	audio := pick("AAC2.0", "DDP5.1", "DDP5.1.Atmos", "TrueHD.Atmos.7.1", "DTS-HD.MA.5.1", "DD5.1", "AC3", "DTS")
	lang := ""
	switch ct.name {
	case "movie-foreign":
		lang = pick("", "", "MULTi", "ENGLISH", "SPANISH", "LATINO", "FRENCH")
	default:
		lang = pick("", "", "", "", "MULTi", "FRENCH", "GERMAN")
	}
	rev := pick("", "", "", "", "", "", "REPACK", "PROPER")
	group := pick("FLUX", "NTb", "BYNDR", "FraMeSToR", "GRP", "YIFY", "RARBG", "SPARKS", "playWEB", "EDITH")
	parts := []string{ct.prefix}
	for _, p := range []string{lang, rev, r.tag, source, hdr, audio, codec} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	name := strings.Join(parts, ".") + "-" + group
	quality := 0.25 + rng.Float64()*1.75 // starved through generous
	mbps := r.mbps * mult * quality
	size := int64(mbps * 1e6 / 8 * ct.title.Runtime.Seconds())
	rel := core.Release{
		ID: fmt.Sprintf("%s-%03d", ct.name, id), GUID: fmt.Sprintf("g%d", id), Indexer: pick("alpha", "beta"),
		Name: name, NZBURL: "x", Size: size, Grabs: rng.IntN(400),
		PubDate: corpusNow.Add(-time.Duration(rng.IntN(2000*24)) * time.Hour),
	}
	if rng.IntN(3) == 0 {
		rel.Protocol = core.ProtocolTorrent
		rel.InfoHash = fmt.Sprintf("%040x", id+1)
		rel.Seeders = rng.IntN(2000)
		rel.Peers = rng.IntN(300)
	}
	return withParsed(rel)
}

// corpusPools builds the deterministic pools: per title, 40 searches of 4
// to 14 releases drawn from one 120-release catalogue.
func corpusPools() map[string][]core.Release {
	out := map[string][]core.Release{}
	for ti, ct := range corpusTitles() {
		rng := rand.New(rand.NewPCG(uint64(ti)+1, 0x5eed))
		all := make([]core.Release, 120)
		for i := range all {
			all[i] = corpusRelease(rng, ct, i)
		}
		for p := 0; p < 40; p++ {
			n := 4 + rng.IntN(11)
			idx := rng.Perm(len(all))[:n]
			pool := make([]core.Release, n)
			for i, j := range idx {
				pool[i] = all[j]
			}
			out[fmt.Sprintf("%s/%02d", ct.name, p)] = pool
		}
	}
	return out
}

// corpusConfig opens a stored config fixture the way an install starts.
func corpusConfig(t *testing.T, doc string) config.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return st.Get()
}

// rankCorpus ranks every pool for every profile fixture and client.
func rankCorpus(t *testing.T) map[string][]string {
	t.Helper()
	pools := corpusPools()
	titles := map[string]*core.Title{}
	for _, ct := range corpusTitles() {
		titles[ct.name] = ct.title
	}
	out := map[string][]string{}
	for _, pf := range corpusProfiles {
		cfg := corpusConfig(t, pf.doc)
		cfg.Resolver.SearchCacheTTL = config.Duration(30 * time.Minute)
		s := New(&fakeIndexer{}, fakeHealth{}, nil, cfg, Options{Now: func() time.Time { return corpusNow }})
		for _, cl := range corpusClients {
			for key, pool := range pools {
				title := titles[strings.Split(key, "/")[0]]
				media := core.MediaMovie
				if title.Type == core.TypeEpisode {
					media = core.MediaTV
				}
				q := cfg.Quality
				for i := range q.Profiles {
					q.Profiles[i].MediaType = media
				}
				profile := SelectProfileForMedia(q, cl.caps, nil, media)
				cands, _, err := s.rankCandidates(context.Background(), core.ResolveRequest{Title: title, Client: cl.caps}, searchRequestFor(title), profile, append([]core.Release(nil), pool...), nil, "")
				if err != nil {
					t.Fatal(err)
				}
				ids := make([]string, len(cands))
				for i, c := range cands {
					ids[i] = strings.TrimPrefix(c.rel.ID, strings.Split(key, "/")[0]+"-")
				}
				out[pf.name+"|"+cl.name+"|"+key] = ids
			}
		}
		s.Close()
	}
	return out
}

func TestRankingCorpusMatchesGolden(t *testing.T) {
	got := rankCorpus(t)
	if os.Getenv("ETHER_WRITE_RANKING_GOLDEN") == "1" {
		b, err := json.MarshalIndent(got, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rankingGoldenPath, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d cases", len(got))
		return
	}
	b, err := os.ReadFile(rankingGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string][]string
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	compareCorpus(t, want, got)
}

// compareCorpus reports top-pick and full-order agreement per profile.
func compareCorpus(t *testing.T, want, got map[string][]string) {
	t.Helper()
	type tally struct{ cases, top, full, accepted int }
	per := map[string]*tally{}
	for k, w := range want {
		g := got[k]
		name := strings.Split(k, "|")[0]
		if per[name] == nil {
			per[name] = &tally{}
		}
		tl := per[name]
		tl.cases++
		if len(w) != len(g) {
			t.Errorf("%s: accepted %d releases, golden %d (%v vs %v)", k, len(g), len(w), g, w)
			continue
		}
		tl.accepted++
		if len(w) == 0 || w[0] == g[0] {
			tl.top++
		} else {
			t.Logf("top pick differs %s: got %v golden %v", k, g, w)
		}
		if strings.Join(w, ",") == strings.Join(g, ",") {
			tl.full++
		}
	}
	for name, tl := range per {
		t.Logf("%-18s cases %4d  same accepted set %4d  same first pick %4d (%.1f%%)  same full order %4d (%.1f%%)",
			name, tl.cases, tl.accepted, tl.top, 100*float64(tl.top)/float64(tl.cases), tl.full, 100*float64(tl.full)/float64(tl.cases))
	}
}
