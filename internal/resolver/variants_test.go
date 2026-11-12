package resolver

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// primeSearchCache runs one resolve so the search results are cached, the
// state a detail page's PreResolve creates before Variants is asked.
func primeSearchCache(t *testing.T, svc *Service, caps core.ClientCaps) {
	t.Helper()
	if _, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: caps}); err != nil {
		t.Fatalf("prime resolve: %v", err)
	}
}

func variantReleases() []core.Release {
	return []core.Release{
		rel("uhd", "The.Signal.1999.2160p.UHD.BluRay.REMUX.DV.HEVC.TrueHD.7.1-GRP", 50000),
		rel("uhd2", "The.Signal.1999.2160p.WEB-DL.DV.HDR.H.265-GRP", 20000), // same tier as uhd
		rel("hd", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-GRP", 8000),
		rel("hdhevc", "The.Signal.1999.1080p.BluRay.x265.DTS-HD.MA.5.1-GRP", 12000),
		rel("dc", "The.Signal.1999.Directors.Cut.1080p.BluRay.x264-GRP", 9000),
		rel("sd720", "The.Signal.1999.720p.BluRay.x264-GRP", 4000),
	}
}

func TestVariantsCacheOnlyNoIndexerCalls(t *testing.T) {
	idx := &fakeIndexer{releases: variantReleases()}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{})
	ctx := context.Background()

	// Nothing cached yet: no variants, and no indexer search spent.
	vs, err := svc.Variants(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()}, 4)
	if err != nil || len(vs) != 0 {
		t.Fatalf("uncached variants = %+v err=%v", vs, err)
	}
	if idx.searches.Load() != 0 {
		t.Fatalf("Variants spent indexer budget: searches=%d", idx.searches.Load())
	}

	primeSearchCache(t, svc, appleTVCaps())
	before := idx.searches.Load()
	vs, err = svc.Variants(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if idx.searches.Load() != before {
		t.Fatalf("Variants searched again: %d -> %d", before, idx.searches.Load())
	}
	if len(vs) == 0 {
		t.Fatal("expected variants from cache")
	}
}

func TestVariantsTiersEditionsAndLabels(t *testing.T) {
	idx := &fakeIndexer{releases: variantReleases()}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{})
	ctx := context.Background()
	primeSearchCache(t, svc, appleTVCaps())

	vs, err := svc.Variants(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) > 4 {
		t.Fatalf("too many variants: %d", len(vs))
	}
	// Auto first, and it is the release normal resolution picks.
	if !vs[0].Auto || vs[0].Release.ID != "uhd" || vs[0].Label != "Auto · 4K Dolby Vision Remux · TrueHD 7.1 · GRP" {
		t.Fatalf("auto variant %+v", vs[0])
	}
	// No duplicate releases; a (cut, tier) repeats only as one of Auto's
	// same-quality alternatives (ETHE-5 C), which come right after Auto.
	seenKey := map[string]bool{}
	seenRel := map[string]bool{}
	for _, v := range vs {
		if seenRel[v.Release.ID] {
			t.Errorf("duplicate release %q", v.Release.ID)
		}
		seenRel[v.Release.ID] = true
		k := variantKey(v.Release.Info)
		if seenKey[k] && k != variantKey(vs[0].Release.Info) {
			t.Errorf("duplicate variant key %q", k)
		}
		seenKey[k] = true
		if v.ID != v.Release.ID || v.Label == "" {
			t.Errorf("variant id/label %+v", v)
		}
	}
	// The Director's Cut is offered as its own version.
	var dc *core.ReleaseVariant
	for i := range vs {
		if vs[i].Edition == "Director's Cut" {
			dc = &vs[i]
		}
	}
	if dc == nil || dc.Release.ID != "dc" {
		t.Fatalf("director's cut variant missing: %+v", vs)
	}
	if !strings.HasPrefix(dc.Label, "Director's Cut") {
		t.Errorf("edition label %q", dc.Label)
	}
	// Estimated info is filled in.
	info := vs[0].Info
	if !info.Estimated || info.Video() == nil || info.Video().Height != 2160 || !info.Video().DolbyVision || info.BitrateKbps <= 0 {
		t.Errorf("auto info %+v", info)
	}
	// The same-tier 4K WEB-DL is Auto's alternative, second in the list.
	if len(vs) < 2 || vs[1].Release.ID != "uhd2" || vs[1].Auto {
		t.Errorf("same-tier alternative not second: %+v", vs)
	}
}

func TestVariantsCapabilityGating(t *testing.T) {
	idx := &fakeIndexer{releases: variantReleases()}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{})
	ctx := context.Background()
	primeSearchCache(t, svc, appleTVCaps())

	// A 1080p SDR H.264-only client gets no 4K/DV/HEVC versions.
	vs, err := svc.Variants(ctx, core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 || vs[0].Release.ID != "hd" || !vs[0].Auto {
		t.Fatalf("web variants %+v", vs)
	}
	for _, v := range vs {
		if v.Release.Info.Height > 1080 || v.Release.Info.DolbyVision || v.Release.Info.VideoCodec == "hevc" {
			t.Errorf("unplayable variant offered to web client: %+v", v.Release.Info)
		}
	}
	// The 4K-capable client does get a 4K version.
	vs, err = svc.Variants(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()}, 4)
	if err != nil {
		t.Fatal(err)
	}
	has4K := false
	for _, v := range vs {
		if v.Release.Info.Height >= 2160 {
			has4K = true
		}
	}
	if !has4K {
		t.Errorf("no 4K variant for Apple TV: %+v", vs)
	}
}

func TestVariantsEpisode(t *testing.T) {
	idx := &fakeIndexer{releases: []core.Release{
		rel("ep4k", "Boundary.Bay.S01E01.2160p.WEB-DL.DV.HEVC-GRP", 8000),
		rel("ephd", "Boundary.Bay.S01E01.1080p.WEB-DL.DDP5.1.H.264-GRP", 3000),
		rel("ep720", "Boundary.Bay.S01E01.720p.HDTV.x264-GRP", 1500),
	}}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{})
	ctx := context.Background()
	title := &core.Title{
		Key: core.EpisodeKey(1396, 1, 1), Type: core.TypeEpisode,
		ShowTitle: "Boundary Bay", Season: 1, Episode: 1, Runtime: 49 * time.Minute,
		ShowIDs: core.IDs{TVDB: 81189},
	}
	if _, err := svc.Resolve(ctx, core.ResolveRequest{Title: title, Client: appleTVCaps()}); err != nil {
		t.Fatal(err)
	}
	vs, err := svc.Variants(ctx, core.ResolveRequest{Title: title, Client: appleTVCaps()}, 4)
	if err != nil || len(vs) < 2 {
		t.Fatalf("episode variants %+v err=%v", vs, err)
	}
	if !vs[0].Auto {
		t.Errorf("episode auto not first: %+v", vs[0])
	}
}

func TestVariantsMaxAndBadReleasesExcluded(t *testing.T) {
	idx := &fakeIndexer{releases: variantReleases()}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{})
	ctx := context.Background()
	primeSearchCache(t, svc, appleTVCaps())

	vs, err := svc.Variants(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()}, 2)
	if err != nil || len(vs) != 2 {
		t.Fatalf("max=2 variants %+v err=%v", vs, err)
	}
	if err := svc.MarkBad(ctx, "uhd", "broken"); err != nil {
		t.Fatal(err)
	}
	vs, err = svc.Variants(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()}, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		if v.Release.ID == "uhd" {
			t.Errorf("marked-bad release offered as variant")
		}
	}
	if len(vs) == 0 || !vs[0].Auto {
		t.Fatalf("auto missing after MarkBad: %+v", vs)
	}
}

func TestResolvePreferWinsOverRanking(t *testing.T) {
	idx := &fakeIndexer{releases: movieReleases()}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{})
	ctx := context.Background()

	// Normal ranking picks uhd; the explicit pick must win.
	res, err := svc.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps(), Prefer: "sd720"})
	if err != nil || res.Release.ID != "sd720" {
		t.Fatalf("prefer res=%+v err=%v", res, err)
	}
	if len(res.Alternates) == 0 {
		t.Error("alternates kept empty on preferred resolve")
	}
	// A 4K pick on the 1080p remote profile stays inside the ceiling unless
	// the owner opted in. With no device rule, remote Apple TV selects
	// 1080p; the client can play HEVC, so the only gate is height.
	noRules := testCfg()
	noRules.Quality.Rules = nil
	remoteSvc := New(idx, fakeHealth{}, nil, noRules, Options{})
	defer remoteSvc.Close()
	remote := appleTVCaps()
	remote.Remote = true
	res, err = remoteSvc.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: remote, Prefer: "uhd"})
	if err != nil || res.Release.ID != "hd" {
		t.Fatalf("prefer above profile height res=%+v err=%v", res, err)
	}
	cfg := testCfg()
	cfg.Quality.Rules = nil
	cfg.Resolver.OfferHigherVersions = true
	opted := New(idx, fakeHealth{}, nil, cfg, Options{})
	defer opted.Close()
	res, err = opted.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: remote, Prefer: "uhd"})
	if err != nil || res.Release.ID != "uhd" {
		t.Fatalf("opted-in prefer res=%+v err=%v", res, err)
	}
}

func TestResolvePreferFallsBackWhenUnhealthyOrMissing(t *testing.T) {
	idx := &fakeIndexer{releases: movieReleases()}
	svc := New(idx, fakeHealth{unhealthy: map[string]bool{"sd720": true}}, nil, testCfg(), Options{})
	ctx := context.Background()

	res, err := svc.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps(), Prefer: "sd720"})
	if err != nil || res.Release.ID != "uhd" {
		t.Fatalf("unhealthy prefer res=%+v err=%v", res, err)
	}
	res, err = svc.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps(), Prefer: "no-such-release"})
	if err != nil || res.Release.ID != "uhd" {
		t.Fatalf("missing prefer res=%+v err=%v", res, err)
	}
}

func TestResolvePreferAlternatesPreferSameEdition(t *testing.T) {
	idx := &fakeIndexer{releases: []core.Release{
		rel("uhd", "The.Signal.1999.2160p.UHD.BluRay.REMUX.DV.HEVC.TrueHD.7.1-GRP", 50000),
		rel("dc1080web", "The.Signal.1999.Directors.Cut.1080p.WEB-DL.x264-GRP", 5000),
		rel("dc", "The.Signal.1999.Directors.Cut.1080p.BluRay.x264-GRP", 9000),
		rel("hd", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-GRP", 8000),
	}}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{})
	res, err := svc.Resolve(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps(), Prefer: "dc"})
	if err != nil || res.Release.ID != "dc" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(res.Alternates) == 0 || res.Alternates[0].ID != "dc1080web" {
		t.Fatalf("alternates should fail over within the cut first: %+v", res.Alternates)
	}
}

func TestEstimateVariantInfoDoesNotInvent1080pEAC3(t *testing.T) {
	info := estimateVariantInfo(matrixTitle(), rel("x", "Abzurdah 2015", 8000), nil)
	if info.Video() != nil {
		t.Fatalf("unparsed release advertised a picture: %+v", info.Video())
	}
	for _, st := range info.Streams {
		if st.Type == "audio" {
			t.Fatalf("unparsed release advertised audio %+v", st)
		}
	}
}

func TestEstimateVariantInfo(t *testing.T) {
	title := matrixTitle()
	r := rel("x", "ignored", 8000)
	r.Info.Height = 1080
	r.Info.VideoCodec = "h264"
	r.Info.AudioCodec = "eac3"
	r.Info.Channels = "5.1"
	info := estimateVariantInfo(title, r, nil)
	if info.Video() == nil || info.Video().Width != 1920 || info.Video().Height != 1080 {
		t.Fatalf("video %+v", info.Video())
	}
	// 8000 MiB over 136 min ≈ 8235 kbps total.
	if info.BitrateKbps < 7500 || info.BitrateKbps > 9000 {
		t.Errorf("bitrate %d", info.BitrateKbps)
	}
	if info.Duration != title.Runtime || info.Size != r.Size || !info.Estimated || info.Container != "mkv" {
		t.Errorf("info %+v", info)
	}
	audio := info.Streams[1]
	if audio.Channels != 6 || audio.Codec != "eac3" {
		t.Errorf("audio %+v", audio)
	}
}

// A DUAL release of an English film, as a Brazilian viewer sees it: the
// Play Version estimate lists both the release's languages (the original
// English and the Brazilian Portuguese dub) and defaults to the track
// playback picks for that viewer, not the title's original language.
func TestEstimateVariantInfoListsDeclaredLanguages(t *testing.T) {
	title := &core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Language: "en", Runtime: 100 * time.Minute}
	rel := withParsed(rel("x", "O.Peso.da.Liberdade.2026.1080p.AMZN.WEB-DL.DUAL.DDP5.1.H.264-BiOMA", 8000))
	if !slices.Contains(rel.Info.Languages, releaseparse.LanguageDual) {
		t.Fatalf("test release should parse as DUAL: %v", rel.Info.Languages)
	}
	info := estimateVariantInfo(title, rel, []string{"pt-BR"})
	got := map[string]bool{}
	for _, st := range info.Streams {
		if st.Type != "audio" {
			continue
		}
		got[releaseparse.LanguageBase(releaseparse.LanguageName(st.Language))] = st.Default
	}
	if len(got) != 2 || got["english"] || !got["portuguese"] {
		t.Fatalf("audio streams = %+v (want english+portuguese, portuguese default)", info.Streams)
	}
	if def := estimatedDefaultAudio(estimateAudioLanguages(rel.Info, title.Language, []string{"pt-BR"}), []string{"pt-BR"}, title.Language); def < 0 {
		t.Fatal("no default audio")
	}
	// An English viewer watching the same release keeps the original default.
	en := estimateVariantInfo(title, rel, []string{"en"})
	for _, st := range en.Streams {
		if st.Type == "audio" && st.Default && releaseparse.LanguageName(st.Language) != "english" {
			t.Fatalf("english viewer default = %q, want english", st.Language)
		}
	}
}

func TestVariantsDoNotWaitForInFlightPlaybackPreparation(t *testing.T) {
	idx := &fakeIndexer{releases: movieReleases(), delay: time.Hour}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{})
	defer svc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolved := make(chan struct{})
	defer func() {
		cancel()
		select {
		case <-resolved:
		case <-time.After(5 * time.Second):
			t.Error("cancelled preparation did not end")
		}
	}()
	go func() {
		defer close(resolved)
		_, _ = svc.Resolve(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for idx.searches.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("playback preparation never started")
		}
		time.Sleep(time.Millisecond)
	}
	started := time.Now()
	vs, err := svc.Variants(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()}, 4)
	if err != nil || len(vs) != 0 {
		t.Fatalf("cold version picker: %+v %v", vs, err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("version picker waited for source resolution")
	}
	if idx.searches.Load() != 1 {
		t.Fatal("rendering started another indexer search")
	}
}

// ETHE-5 C: Auto first, then up to two same-cut/resolution/HDR alternatives
// in rank order, then today's tiers, with a label that tells the releases
// apart. Names are real scene-style episode releases.
func TestPickVariantsSameQualityAlternatives(t *testing.T) {
	ep := &core.Title{Key: core.EpisodeKey(1, 1, 1), Type: core.TypeEpisode, Runtime: 45 * time.Minute}
	mk := func(id, name string, sizeMB int64) candidate {
		return candidate{rel: withParsed(rel(id, name, sizeMB))}
	}
	pool := []candidate{
		mk("auto", "Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-NTb", 2300),
		mk("dupe", "Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-NTb", 2300), // same file from another source
		mk("flux", "Show.S01E01.1080p.WEB-DL.DDP5.1.H.264-FLUX", 2400),
		mk("edith", "Show.S01E01.1080p.WEB-DL.AAC2.0.H.264-EDITH", 1600),
		mk("third", "Show.S01E01.1080p.WEB-DL.AAC2.0.H.264-KiNGS", 1500), // over the cap of two
		mk("hevc4k", "Show.S01E01.2160p.WEB-DL.DV.HDR.H.265-GRP", 9000),
		mk("hd720", "Show.S01E01.720p.WEB-DL.H.264-GRP", 900),
	}
	caps := core.ClientCaps{MaxHeight: 2160}
	profile := core.QualityProfile{MaxHeight: 2160, AllowHDR: true, AllowDolbyVision: true}
	out := pickVariants(ep, caps, profile, pool[0].rel, pool, pool, 2, maxVariants, true, nil)
	var ids []string
	for _, v := range out {
		ids = append(ids, v.ID)
	}
	want := []string{"auto", "flux", "edith", "hevc4k", "hd720"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("versions %v, want %v", ids, want)
	}
	if !out[0].Auto || out[1].Auto || out[2].Auto {
		t.Errorf("only the first version is Auto: %+v", out)
	}
	if got := out[1].Label; got != "1080p WEB-DL · DD+ 5.1 · 2.3 GB · FLUX" {
		// 2400 MB is 2.34 GB, which rounds to 2.3
		t.Errorf("alternative label %q", got)
	}
	if got := out[2].Label; got != "1080p WEB-DL · AAC 2.0 · 1.6 GB · EDITH" {
		t.Errorf("alternative label %q", got)
	}
	// A smaller cap never drops a tier to make room for an alternative.
	small := pickVariants(ep, caps, profile, pool[0].rel, pool, pool, 2, 4, true, nil)
	var got []string
	for _, v := range small {
		got = append(got, v.ID)
	}
	if strings.Join(got, ",") != "auto,flux,hevc4k,hd720" {
		t.Errorf("max=4 versions %v", got)
	}
	// A pool where only Auto and its copy exist yields no alternatives.
	if out := pickVariants(ep, caps, profile, pool[0].rel, pool[:2], pool[:2], 2, maxVariants, true, nil); len(out) != 1 {
		t.Errorf("duplicate offered as alternative: %+v", out)
	}
}

// ETHE-5 C: alternatives come from the strict profile pool, so a 4K release
// on a 1080p profile is never an alternative, and rendering stays cache-only.
func TestVariantsAlternativesFromStrictPoolCacheOnly(t *testing.T) {
	idx := &fakeIndexer{releases: []core.Release{
		rel("a", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-TOP", 8000),
		rel("b", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-FLUX", 7000),
		rel("c", "The.Signal.1999.1080p.WEB-DL.AAC2.0.H.264-EDITH", 6000),
		rel("d", "The.Signal.1999.1080p.WEB-DL.AAC2.0.H.264-KiNGS", 5000),
		rel("uhd", "The.Signal.1999.2160p.WEB-DL.DV.HDR.H.265-GRP", 20000),
	}}
	cfg := testCfg()
	cfg.Resolver.SameQualityAlternatives = 2
	svc := New(idx, fakeHealth{}, nil, cfg, Options{})
	primeSearchCache(t, svc, webCaps())
	before := idx.searches.Load()
	vs, err := svc.Variants(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}, core.MaxVariants)
	if err != nil {
		t.Fatal(err)
	}
	if idx.searches.Load() != before {
		t.Fatalf("Variants spent indexer budget")
	}
	if len(vs) < 3 || !vs[0].Auto {
		t.Fatalf("versions %+v", vs)
	}
	alts := 0
	for _, v := range vs[1:] {
		if v.Release.Info.Height == 2160 {
			t.Errorf("4K release offered on a 1080p profile: %+v", v)
		}
		if variantKey(v.Release.Info) == variantKey(vs[0].Release.Info) {
			alts++
		}
	}
	if alts != 2 {
		t.Errorf("%d alternatives, want 2: %+v", alts, vs)
	}
}

// The owner chooses how many same-quality alternatives are listed (0-3);
// the picker never exceeds core.MaxVariants.
func TestVariantsAlternativeCountSetting(t *testing.T) {
	releases := []core.Release{
		rel("a", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-TOP", 8000),
		rel("b", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-FLUX", 7000),
		rel("c", "The.Signal.1999.1080p.WEB-DL.AAC2.0.H.264-EDITH", 6000),
		rel("d", "The.Signal.1999.1080p.WEB-DL.AAC2.0.H.264-KiNGS", 5000),
		rel("e", "The.Signal.1999.1080p.WEB-DL.AAC2.0.H.264-NTb", 4000),
	}
	for _, tc := range []struct{ setting, want int }{{0, 0}, {1, 1}, {3, 3}, {9, 3}, {-2, 0}} {
		cfg := testCfg()
		cfg.Resolver.SameQualityAlternatives = tc.setting
		svc := New(&fakeIndexer{releases: releases}, fakeHealth{}, nil, cfg, Options{})
		primeSearchCache(t, svc, webCaps())
		vs, err := svc.Variants(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}, core.MaxVariants)
		if err != nil {
			t.Fatal(err)
		}
		got := 0
		for _, v := range vs[1:] {
			if variantKey(v.Release.Info) == variantKey(vs[0].Release.Info) {
				got++
			}
		}
		if got != tc.want || len(vs) > core.MaxVariants {
			t.Errorf("setting %d: %d alternatives (want %d) in %d versions", tc.setting, got, tc.want, len(vs))
		}
		svc.Close()
	}
}

// Every version names its source and group, so viewers can tell which
// release they would play (#188); Auto carries no size.
func TestVariantLabelsNameSourceAndGroup(t *testing.T) {
	flux := withParsed(rel("x", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-FLUX", 2300))
	for kind, want := range map[string]string{
		"auto":   "Auto · 1080p WEB-DL · DD+ 5.1 · FLUX",
		"tier":   "1080p WEB-DL · FLUX",
		"4k":     "1080p WEB-DL · FLUX",
		"compat": "1080p WEB-DL · FLUX (High Compatibility)",
		"alt":    "1080p WEB-DL · DD+ 5.1 · 2.2 GB · FLUX",
	} {
		if got := variantLabel(flux, kind); got != want {
			t.Errorf("%s label %q, want %q", kind, got, want)
		}
	}
	dc := withParsed(rel("dc", "The.Signal.1999.Directors.Cut.1080p.BluRay.x264-AMIABLE", 9000))
	if got := variantLabel(dc, "edition"); got != "Director's Cut — 1080p Blu-ray · AMIABLE" {
		t.Errorf("edition label %q", got)
	}
	nogroup := withParsed(rel("n", "The Signal 1999 1080p WEB-DL", 2000))
	nogroup.Info.Group = ""
	if got := variantLabel(nogroup, "auto"); got != "Auto · 1080p WEB-DL" {
		t.Errorf("label without group or audio %q", got)
	}
}

// After a play, Auto is named after the release that actually played when
// that is not one of the explicit versions (Auto resolved past its first
// candidate); an explicit version keeps Auto's own name.
func TestAutoLabelFollowsPlayedRelease(t *testing.T) {
	idx := &fakeIndexer{releases: append(variantReleases(),
		rel("played", "The.Signal.1999.2160p.UHD.BluRay.REMUX.DV.HEVC.DTS-HD.MA.5.1-FraMeSToR", 52000))}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{})
	ctx := context.Background()
	primeSearchCache(t, svc, appleTVCaps())
	base, err := svc.Variants(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps()}, 4)
	if err != nil || len(base) < 2 {
		t.Fatalf("variants %v %v", base, err)
	}
	played := ""
	inList := map[string]bool{}
	for _, v := range base {
		inList[v.Release.ID] = true
	}
	for _, id := range []string{"played", "uhd", "uhd2"} {
		if !inList[id] {
			played = id
			break
		}
	}
	if played == "" {
		t.Skip("every candidate release is an explicit version")
	}
	vs, err := svc.Variants(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps(), PlayedRelease: played}, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := ""
	for _, r := range idx.releases {
		if r.ID == played {
			want = autoLabel(withParsed(r))
		}
	}
	if vs[0].Label != want || vs[0].Label == base[0].Label {
		t.Fatalf("auto label after a play of %s: %q, want %q", played, vs[0].Label, want)
	}
	// When Auto played the second-row same-tier alternative (base[1]),
	// Auto's label, release, and bitrate/attributes follow that row.
	sameTierAlt := base[1]
	vs, err = svc.Variants(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps(), PlayedRelease: sameTierAlt.Release.ID}, 4)
	if err != nil {
		t.Fatal(err)
	}
	wantAltLabel := autoLabel(sameTierAlt.Release)
	if vs[0].Label != wantAltLabel || vs[0].Info.BitrateKbps != sameTierAlt.Info.BitrateKbps || vs[0].Release.ID != sameTierAlt.Release.ID {
		t.Fatalf("auto after playing second-row alternative %s: label=%q (want %q) bitrate=%d (want %d) id=%s",
			sameTierAlt.Release.ID, vs[0].Label, wantAltLabel, vs[0].Info.BitrateKbps, sameTierAlt.Info.BitrateKbps, vs[0].Release.ID)
	}

	// An explicit version in another tier or cut keeps Auto's own name.
	explicit := base[2].Release.ID
	vs, err = svc.Variants(ctx, core.ResolveRequest{Title: matrixTitle(), Client: appleTVCaps(), PlayedRelease: explicit}, 4)
	if err != nil || vs[0].Label != base[0].Label {
		t.Fatalf("auto relabelled for an explicit version: %q (err %v)", vs[0].Label, err)
	}
}

func TestResolutionVariantsBalanceAndBounds(t *testing.T) {
	mk := func(id, name string, size int64) candidate { return candidate{rel: withParsed(rel(id, name, size))} }
	pool := []candidate{
		mk("auto", "The.Signal.1999.1080p.WEB-DL.H.264-TOP", 8000),
		mk("copy", "The.Signal.1999.1080p.WEB-DL.H.264-TOP", 8000),
		mk("hd", "The.Signal.1999.1080p.WEB-DL.H.264-ALT", 7000),
		mk("uhd", "The.Signal.1999.2160p.WEB-DL.H.265-TOP", 16000),
		mk("uhd2", "The.Signal.1999.2160p.WEB-DL.H.265-ALT", 14000),
		mk("uhd3", "The.Signal.1999.2160p.WEB-DL.H.265-THIRD", 13000),
		mk("sd", "The.Signal.1999.720p.WEB-DL.H.264-TOP", 4000),
		mk("sd2", "The.Signal.1999.720p.WEB-DL.H.264-ALT", 3000),
	}
	profile := core.QualityProfile{MaxHeight: 2160}
	for _, tc := range []struct {
		name                       string
		alternatives, cap, ceiling int
		want                       string
	}{
		{"two per resolution", 1, 7, 2160, "auto,hd,uhd,uhd2,sd,sd2"},
		{"coverage before duplicates", 3, 3, 2160, "auto,uhd,sd"},
		{"profile ceiling", 1, 7, 1080, "auto,hd,sd,sd2"},
		{"no alternatives", 0, 7, 2160, "auto,uhd,sd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := profile
			p.MaxHeight = tc.ceiling
			got := pickResolutionVariants(matrixTitle(), p, pool[0].rel, pool, tc.alternatives, tc.cap, false, true, false, nil)
			var ids []string
			for i, v := range got {
				if v.Auto != (i == 0) {
					t.Fatal("Auto moved")
				}
				ids = append(ids, v.ID)
			}
			if strings.Join(ids, ",") != tc.want {
				t.Fatalf("%v, want %s", ids, tc.want)
			}
		})
	}
	// Exercise the public cache-only path and the actual device/profile filter.
	cfg := testCfg()
	cfg.Resolver.AlternativesPerResolution = true
	cfg.Resolver.SameQualityAlternatives = 1
	idx := &fakeIndexer{}
	for _, c := range pool {
		idx.releases = append(idx.releases, c.rel)
	}
	svc := New(idx, fakeHealth{}, nil, cfg, Options{})
	primeSearchCache(t, svc, webCaps())
	searches := idx.searches.Load()
	got, err := svc.Variants(context.Background(), core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}, core.MaxVariants)
	if err != nil || len(got) == 0 || !got[0].Auto || idx.searches.Load() != searches {
		t.Fatalf("cache-only mode: %v %v", got, err)
	}
	for _, v := range got {
		if v.Release.Info.Height > 1080 {
			t.Fatalf("device/profile ceiling bypass: %+v", v)
		}
	}
}

// TestAlternativesPerSourceOffersSourceAtEachResolution is the lasting test
// for the AlternativesPerSource setting: cached results hold several remuxes
// and one WEB-DL at 2160p and 1080p. With the setting on, the menu keeps Auto
// (the 4K remux) and still shows a WEB-DL at each resolution, instead of the
// per-resolution slots filling with more remuxes. Off, today's behaviour is
// unchanged, and Auto is identical either way.
func TestAlternativesPerSourceOffersSourceAtEachResolution(t *testing.T) {
	mk := func(id, name string, size int64) candidate { return candidate{rel: withParsed(rel(id, name, size))} }
	pool := []candidate{
		mk("auto", "The.Signal.1999.2160p.UHD.BluRay.REMUX.HEVC.TrueHD.7.1-TOP", 50000),
		mk("r2", "The.Signal.1999.2160p.UHD.BluRay.REMUX.HEVC.DTS-HD.5.1-ALT", 48000),
		mk("uhdweb", "The.Signal.1999.2160p.WEB-DL.H.265-WEB", 20000),
		mk("hd1", "The.Signal.1999.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-TOP", 30000),
		mk("hd2", "The.Signal.1999.1080p.BluRay.x265.DTS-HD.MA.5.1-ALT", 12000),
		mk("hdweb", "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-WEB", 8000),
	}
	profile := core.QualityProfile{MaxHeight: 2160}
	heights := func(vs []core.ReleaseVariant) map[string]int {
		m := map[string]int{}
		for _, v := range vs {
			m[v.Release.ID] = v.Release.Info.Height
		}
		return m
	}

	on := pickResolutionVariants(matrixTitle(), profile, pool[0].rel, pool, 1, core.MaxVariants, false, false, true, nil)
	if len(on) == 0 || !on[0].Auto || on[0].Release.ID != "auto" {
		t.Fatalf("Auto moved with per-source on: %+v", on)
	}
	byID := heights(on)
	for _, want := range []struct {
		id     string
		height int
	}{{"uhdweb", 2160}, {"hdweb", 1080}} {
		if byID[want.id] != want.height {
			t.Fatalf("per-source on: %s (height %d) missing from %v", want.id, want.height, byID)
		}
	}
	if byID["r2"] != 0 {
		t.Fatalf("per-source on surfaced an extra remux: %v", byID)
	}

	// Off: the per-resolution slots fill with remuxes and no WEB-DL shows,
	// which is exactly why the setting exists.
	off := pickResolutionVariants(matrixTitle(), profile, pool[0].rel, pool, 1, core.MaxVariants, false, false, false, nil)
	if len(off) == 0 || !off[0].Auto || off[0].Release.ID != "auto" {
		t.Fatalf("Auto moved with per-source off: %+v", off)
	}
	if byID := heights(off); byID["uhdweb"] != 0 || byID["hdweb"] != 0 {
		t.Fatalf("per-source off unexpectedly surfaced a WEB-DL: %v", byID)
	}

	// Auto is identical either way.
	if on[0].Release.ID != off[0].Release.ID || on[0].Label != off[0].Label || on[0].Info.BitrateKbps != off[0].Info.BitrateKbps {
		t.Fatalf("Auto changed with the setting: on=%+v off=%+v", on[0], off[0])
	}
}
