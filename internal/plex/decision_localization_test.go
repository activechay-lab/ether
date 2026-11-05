package plex

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/plex/model"
)

func TestDecisionLocalizationPreservesSharedResponse(t *testing.T) {
	original := &model.MediaContainer{Size: 1, Extra: []model.Attr{model.A("directPlayDecisionCode", 3000), model.A("directPlayDecisionText", textDirectPlayDisable), model.A("transcodeDecisionText", textConversionOK), model.A("resourceSession", "player")}}
	r := httptest.NewRequest("GET", "/video/:/transcode/universal/decision", nil)
	r = r.WithContext(withRequest(r.Context(), &Request{Language: "fr-CA"}))
	got := localizeDecisionResponse(r, original).(*model.MediaContainer)
	if got.Extra[1].Value == textDirectPlayDisable || !strings.Contains(got.Extra[1].Value.(string), "désactivée") {
		t.Fatalf("French refusal %v", got.Extra)
	}
	if got.Extra[0].Value != 3000 || got.Extra[3].Value != "player" || original.Extra[1].Value != textDirectPlayDisable {
		t.Fatalf("shared response mutated: original=%v localized=%v", original.Extra, got.Extra)
	}
	original.Extra[1].Value = "Diagnostic 42: source-specific detail"
	got = localizeDecisionResponse(r, original).(*model.MediaContainer)
	if got.Extra[1].Value != original.Extra[1].Value {
		t.Fatal("unknown detail discarded")
	}
	r.URL.Path = "/library/metadata/1"
	if localizeDecisionResponse(r, original) != original {
		t.Fatal("non-decision response copied")
	}
}

func TestDecisionLanguageNegotiation(t *testing.T) {
	for _, tc := range []struct{ x, accept, want string }{
		{"fr-CA", "de", "fr"}, {"unknown", "fr", ""}, {"", "ca,fr;q=0.8", "fr"}, {"", "ca,nn", "es"}, {"", "nn,ca", "no"},
		{"", "zh-TW,fr;q=0.5", "fr"}, {"", "zh-Hant-HK,ca;q=0.8", "es"}, {"pt-PT", "", "pt-br"}, {"nb-NO", "", "no"},
		{"", "fr;q=0,de;q=0.8", "de"}, {"", "de;q=invalid,fr;q=0.5", "fr"}, {"en-GB", "fr", "en"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", tc.accept)
		r = r.WithContext(withRequest(r.Context(), &Request{Language: tc.x}))
		if got := decisionLanguage(r); got != tc.want {
			t.Errorf("X=%s Accept=%s: %s want %s", tc.x, tc.accept, got, tc.want)
		}
	}
}

func TestDecisionLocalizationHTTP(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	path := "/video/:/transcode/universal/decision?path=/library/metadata/1000000000000550&hasMDE=1"
	english := f.get(t, path, "Accept", "application/json")
	french := f.get(t, path, "Accept", "application/json", "X-Plex-Language", "fr")
	if english.Code != 200 || french.Code != 200 {
		t.Fatalf("status en=%d fr=%d", english.Code, french.Code)
	}
	en, fr := decodeJSON(t, english), decodeJSON(t, french)
	if en["mdeDecisionCode"] != fr["mdeDecisionCode"] || en["mdeDecisionText"] == fr["mdeDecisionText"] {
		t.Fatalf("English=%v French=%v", en, fr)
	}
}
