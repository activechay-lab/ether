package core

import "testing"

func TestRatingKeyRoundTrip(t *testing.T) {
	cases := []struct {
		k       RatingKey
		kind    KeyKind
		tmdb    int64
		season  int
		episode int
	}{
		{MovieKey(603), KindMovie, 603, 0, 0},
		{ShowKey(1399), KindShow, 1399, 0, 0},
		{SeasonKey(1399, 0), KindSeason, 1399, 0, 0},
		{SeasonKey(1399, 8), KindSeason, 1399, 8, 0},
		{EpisodeKey(1399, 8, 6), KindEpisode, 1399, 8, 6},
		{EpisodeKey(99_999_999, 999, 9999), KindEpisode, 99_999_999, 999, 9999},
		{CollectionKey(87359), KindCollection, 87359, 0, 0},
		{ExtraKey(42), KindExtra, 42, 0, 0},
	}
	for _, c := range cases {
		if c.k.Kind() != c.kind || c.k.TMDB() != c.tmdb || c.k.Season() != c.season || c.k.Episode() != c.episode {
			t.Errorf("%d: got kind=%d tmdb=%d s=%d e=%d", c.k, c.k.Kind(), c.k.TMDB(), c.k.Season(), c.k.Episode())
		}
		if int64(c.k) >= 1<<53 {
			t.Errorf("%d exceeds 2^53", c.k)
		}
		p, err := ParseRatingKey(c.k.String())
		if err != nil || p != c.k {
			t.Errorf("parse %s: %v %v", c.k, p, err)
		}
	}
	if EpisodeKey(1399, 8, 6).Parent() != SeasonKey(1399, 8) || SeasonKey(1399, 8).Parent() != ShowKey(1399) {
		t.Error("parent")
	}
	if ExtraKey(123_456_789).ExtraID() != 123_456_789 || MovieKey(1).ExtraID() != 0 {
		t.Error("extra id")
	}
	if ExtraKey(999_999_999_999_999) >= RatingKey(1<<53) {
		t.Error("extra key exceeds 2^53")
	}
	if _, err := ParseRatingKey(ExtraKey(7).String()); err != nil {
		t.Errorf("parse extra key: %v", err)
	}
}

func TestEphemeralSmartKeysAvoidYearInReview(t *testing.T) {
	if k := EphemeralSmartKey(1); !k.IsEphemeral() || k.Kind() != KindSmart {
		t.Fatalf("EphemeralSmartKey(1) = %d not ephemeral", k)
	}
	if k := EphemeralSmartKey(EphemeralSmartEnd - EphemeralSmartBase - 1); !k.IsEphemeral() {
		t.Fatalf("last ephemeral key %d not ephemeral", k)
	}
	// personalize's Year in Review keys start at payload 9*10^14
	// (base + year*10 + kind) and must never look ephemeral.
	review := SmartKey(EphemeralSmartEnd + 2026*10 + 1)
	if review.IsEphemeral() {
		t.Fatalf("Year in Review key %d reported ephemeral", review)
	}
	if SmartKey(42).IsEphemeral() || MovieKey(42).IsEphemeral() {
		t.Fatal("database smart key or movie key reported ephemeral")
	}
}

func TestPluginKeyAndLocalKeyGuard(t *testing.T) {
	first := PluginKey(1)
	last := PluginKey(PluginKeyMaxSeq - 1)
	if !first.IsPluginKey() || first.PluginSeq() != 1 || first.Kind() != KindLocal || !first.Valid() {
		t.Fatalf("PluginKey(1) = %d, isPlugin=%v seq=%d", first, first.IsPluginKey(), first.PluginSeq())
	}
	if !last.IsPluginKey() || last.PluginSeq() != PluginKeyMaxSeq-1 || int64(last) >= 1<<53 {
		t.Fatalf("PluginKey(max) = %d exceeds 2^53 or invalid", last)
	}
	if p, err := ParseRatingKey(first.String()); err != nil || p != first || !p.IsPluginKey() {
		t.Fatalf("ParseRatingKey(PluginKey(1)) = %v, %v", p, err)
	}
	local := LocalKey(PluginKeyBase - 1)
	if local.IsPluginKey() || local.PluginSeq() != 0 {
		t.Fatalf("LocalKey(%d) reported as plugin key", local)
	}
	if MovieKey(1).IsPluginKey() {
		t.Fatal("MovieKey reported as plugin key")
	}
	assertPanics := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s did not panic", name)
			}
		}()
		fn()
	}
	assertPanics("LocalKey(PluginKeyBase)", func() { LocalKey(PluginKeyBase) })
	assertPanics("PluginKey(0)", func() { PluginKey(0) })
	assertPanics("PluginKey(PluginKeyMaxSeq)", func() { PluginKey(PluginKeyMaxSeq) })
}

func TestItemRefParse(t *testing.T) {
	ref := NewItemRef("com.example.books", "audiobook", "book-01_a.2~z")
	if !ref.Valid() || ref.Plugin() != "com.example.books" || ref.Kind() != "audiobook" || ref.External() != "book-01_a.2~z" {
		t.Fatalf("ItemRef(%q) parsed wrong: plugin=%q kind=%q ext=%q", ref, ref.Plugin(), ref.Kind(), ref.External())
	}
	if got, err := ParseItemRef(ref.String()); err != nil || got != ref {
		t.Fatalf("ParseItemRef(%q) = %q, %v", ref, got, err)
	}
	for _, bad := range []string{
		"",
		"pk:",
		"pk:plugin:kind:",
		"pk::kind:id",
		"pk:plugin::id",
		"pk:plugin:kind:bad/slash",
		"pk:plugin:kind:bad:colon",
		"pk:plugin:kind:-starts-with-dash",
		"other:plugin:kind:id",
	} {
		if _, err := ParseItemRef(bad); err == nil || ItemRef(bad).Valid() {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}
