package trackprefs

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
	"github.com/braedonsaunders/ether/internal/store"
)

// TestSelectZhScriptRegion locks the langtag consumer behaviour for
// script+region tags: a zh-TW want must select a zh-Hant-TW track (the
// script "Hant" is not a region) and must not select zh-Hans.
func TestSelectZhScriptRegion(t *testing.T) {
	if got := langtag.MatchTier("zh-TW", "zh-Hant-TW"); got == 0 {
		t.Fatalf("MatchTier(zh-TW, zh-Hant-TW) = 0, want a match")
	}
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	r := Default()
	r.SubtitleLanguages = []string{"zh-TW"}
	r.SubtitleMode = "always"
	if err := s.PutRules(ctx, 7, r); err != nil {
		t.Fatal(err)
	}
	info := core.MediaInfo{Streams: []core.StreamInfo{
		{Type: "audio", Language: "en", Index: 0},
		{Type: "subtitle", Language: "zh-Hans", Index: 1},
		{Type: "subtitle", Language: "zh-Hant-TW", Index: 2},
	}}
	sel, err := s.Select(ctx, 7, core.MovieKey(1), info, "")
	if err != nil {
		t.Fatal(err)
	}
	if sel.Subtitle != 2 {
		t.Fatalf("Select(zh-TW want, [zh-Hans zh-Hant-TW]) subtitle = %d, want 2", sel.Subtitle)
	}
	if got := rankLanguage("zh-Hans", []string{"zh-TW"}); got != 0 {
		t.Fatalf("rankLanguage(zh-Hans, [zh-TW]) = %d, want 0 (distinct regions)", got)
	}
}

func TestSelectExtendedBCP47AndTitleLanguageInference(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)

	// 1. Regional Spanish preference: es-419 beats bare es, which beats es-ES;
	// and a bare "spa" track titled "Español Latino" refines to es-419.
	r := Default()
	r.AudioLanguages = []string{"es-419"}
	r.SubtitleLanguages = []string{"sr-Latn"}
	r.SubtitleMode = "always"
	if err := s.PutRules(ctx, 7, r); err != nil {
		t.Fatal(err)
	}
	info := core.MediaInfo{Streams: []core.StreamInfo{
		{Type: "audio", Language: "es-ES", Title: "Castellano 5.1", Index: 0, Default: true},
		{Type: "audio", Language: "spa", Title: "Spanish 2.0", Index: 1},
		{Type: "audio", Language: "spa", Title: "Español Latino 5.1", Index: 2},
		{Type: "subtitle", Language: "sr-Cyrl", Title: "Serbian (Cyrillic)", Index: 3, Default: true},
		{Type: "subtitle", Language: "srp", Title: "Serbian (Latin)", Index: 4},
	}}
	sel, err := s.Select(ctx, 7, core.MovieKey(10), info, "en")
	if err != nil {
		t.Fatal(err)
	}
	if sel.Audio != 2 {
		t.Fatalf("es-419 want should pick Español Latino track 2, got %d", sel.Audio)
	}
	if sel.Subtitle != 4 {
		t.Fatalf("sr-Latn want should pick Serbian (Latin) track 4 over Cyrillic track 3, got %d", sel.Subtitle)
	}

	// 2. Untagged ("und") audio stream with language in Title matches viewer preference.
	r2 := Default()
	r2.AudioLanguages = []string{"fr"}
	r2.SubtitleMode = "foreign-only"
	if err := s.PutRules(ctx, 8, r2); err != nil {
		t.Fatal(err)
	}
	undInfo := core.MediaInfo{Streams: []core.StreamInfo{
		{Type: "audio", Language: "und", Title: "AC3 5.1 English", Index: 0, Default: true},
		{Type: "audio", Language: "und", Title: "AC3 5.1 French", Index: 1},
		{Type: "subtitle", Language: "fre", Index: 2},
	}}
	sel2, err := s.Select(ctx, 8, core.MovieKey(11), undInfo, "en")
	if err != nil {
		t.Fatal(err)
	}
	if sel2.Audio != 1 || sel2.Subtitle != -1 {
		t.Fatalf("und title inference: want audio=1 subtitle=-1 (not foreign), got %+v", sel2)
	}
}
