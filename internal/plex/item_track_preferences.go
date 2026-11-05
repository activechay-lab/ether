package plex

import (
	"context"
	"sort"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
	"github.com/braedonsaunders/ether/internal/plex/model"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// Track language controls inherit through the series. Only cached information
// for this item is consulted: a detail request never enumerates a series/library.
func (s *Server) itemTrackPreferences(ctx context.Context, pr *Request, t *core.Title) *model.TrackPreferences {
	if t.Type != core.TypeShow && t.Type != core.TypeSeason {
		return nil
	}
	inherit := "Account default"
	if t.Type == core.TypeSeason {
		inherit = "Series default"
	}
	if translated, ok := preferenceInheritance[primaryLang(pr.Language)]; ok {
		inherit = translated[0]
		if t.Type == core.TypeSeason {
			inherit = translated[1]
		}
	}
	entries := []string{":" + inherit}
	languageNames := display.Self.Name
	if pr.Language != "" {
		languageNames = display.Languages(language.Make(pr.Language)).Name
	}
	seen := map[string]bool{}
	info, _ := s.mediaInfoFor(ctx, pr, t)
	for _, kind := range []string{"audio", "subtitle"} {
		var codes []string
		for _, st := range info.Streams {
			tag, _ := langtag.ParseBCP47(st.Language)
			code := tag.Primary
			if st.Type == kind && code != "" && code != "und" && !seen[code] {
				seen[code] = true
				codes = append(codes, code)
			}
		}
		sort.Strings(codes)
		for _, code := range codes {
			entries = append(entries, code+":"+languageNames(language.Make(code)))
		}
	}
	// Regional tags are the server's finite language-choice vocabulary.
	for _, code := range strings.Fields("ca-ES cs-CZ da-DK de-DE et-EE en-US en-AU en-CA en-GB es-ES es-MX fr-FR fr-CA hr-HR id-ID is-IS it-IT lv-LV lt-LT hu-HU nl-NL nb-NO pl-PL pt-BR pt-PT ro-RO sk-SK fi-FI sv-SE vi-VN tr-TR el-GR bg-BG ru-RU uk-UA he-IL ar-SA fa-IR hi-IN th-TH ko-KR zh-CN zh-HK zh-TW ja-JP") {
		entries = append(entries, code+":"+languageNames(language.Make(code)))
	}
	enum := strings.Join(entries, "|")
	return &model.TrackPreferences{Setting: []model.Setting{
		{ID: "audioLanguage", Label: "Preferred audio language", Type: "text", Default: "", Value: "", EnumValues: enum},
		{ID: "subtitleLanguage", Label: "Preferred subtitle language", Type: "text", Default: "", Value: "", EnumValues: enum},
		{ID: "subtitleMode", Label: "Auto-select subtitle mode", Type: "int", Default: "-1", Value: "-1", EnumValues: "-1:" + inherit + "|0:Manually selected|1:Shown with foreign audio|2:Always enabled"},
	}}
}

// Protocol setting labels are independent of console messages.
var preferenceInheritance = map[string][2]string{
	"de": {"Kontostandard", "Serienstandard"},
	"fr": {"Valeur par défaut du compte", "Valeur par défaut de la série"},
	"es": {"Predeterminado de la cuenta", "Predeterminado de la serie"},
	"it": {"Predefinito account", "Predefinito serie"},
	"pt": {"Padrão da conta", "Padrão da série"},
	"nl": {"Accountstandaard", "Seriestandaard"},
	"da": {"Kontostandard", "Seriestandard"},
	"sv": {"Kontostandard", "Seriestandard"},
	"nb": {"Kontostandard", "Seriestandard"},
	"fi": {"Tilin oletus", "Sarjan oletus"},
	"pl": {"Domyślne ustawienie konta", "Domyślne ustawienie serialu"},
	"cs": {"Výchozí nastavení účtu", "Výchozí nastavení seriálu"},
	"ru": {"По умолчанию для аккаунта", "По умолчанию для сериала"},
	"uk": {"Типово для облікового запису", "Типово для серіалу"},
	"ja": {"アカウントのデフォルト", "シリーズのデフォルト"},
	"ko": {"계정 기본값", "시리즈 기본값"},
	"zh": {"帐户默认值", "剧集默认值"},
	"tr": {"Hesap varsayılanı", "Dizi varsayılanı"},
	"hu": {"Fiók alapértelmezése", "Sorozat alapértelmezése"},
	"ro": {"Implicit pentru cont", "Implicit pentru serial"},
}
