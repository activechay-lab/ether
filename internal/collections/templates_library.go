package collections

import (
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
)

// Library templates are the facet collections Ether used to create on every
// install (one per film genre and TV network). A fresh install now creates
// none; the owner adds them from these templates or their bundles. Each is a
// local catalogue facet, so it builds from the facet index without TMDB.

var (
	catGenres   = extend.T("Genres").Source
	catNetworks = extend.T("TV networks").Source
	catStudios  = extend.T("Studios").Source

	descGenre   = extend.T("Every film in this genre from your catalogue, most popular first.").Source
	descNetwork = extend.T("Every series from this network in your catalogue, most popular first.").Source
	descStudio  = extend.T("Every film from this studio in your catalogue, most popular first.").Source
)

// facetTpl matches one catalogue facet (genre or studio/network). Values are
// alternative spellings of the same facet, such as a renamed network.
func facetTpl(name, media, field string, values ...string) core.CollectionSpec {
	sp := tpl(name, media, catalogSrc())
	sp.LocalOnly, sp.Limit, sp.Refresh = true, 0, RefreshLive
	if len(values) == 1 {
		sp.Rules = []core.CollectionRule{mkRule(field, "is", values[0])}
	} else {
		sp.Rules = []core.CollectionRule{mkRule(field, "in", values)}
	}
	return sp
}

func genreDef(id, name, genre string) templateDef {
	return templateDef{"genre-movie-" + id, name, descGenre, catGenres, facetTpl(name, MediaMovie, "genre", genre)}
}

func networkDef(id, name string, values ...string) templateDef {
	return templateDef{"network-" + id, name, descNetwork, catNetworks, facetTpl(name, MediaShow, "studio", values...)}
}

func studioDef(id, name string, values ...string) templateDef {
	return templateDef{"movie-studio-" + id, name, descStudio, catStudios, facetTpl(name, MediaMovie, "studio", values...)}
}

var libraryTemplateDefs = []templateDef{
	genreDef("action", extend.T("Action Movies").Source, "Action"),
	genreDef("adventure", extend.T("Adventure Movies").Source, "Adventure"),
	genreDef("animation", extend.T("Animated Movies").Source, "Animation"),
	genreDef("comedy", extend.T("Comedy Movies").Source, "Comedy"),
	genreDef("crime", extend.T("Crime Movies").Source, "Crime"),
	genreDef("documentary", extend.T("Documentaries").Source, "Documentary"),
	genreDef("drama", extend.T("Drama Movies").Source, "Drama"),
	genreDef("family", extend.T("Family Movies").Source, "Family"),
	genreDef("fantasy", extend.T("Fantasy Movies").Source, "Fantasy"),
	genreDef("history", extend.T("Historical Movies").Source, "History"),
	genreDef("horror", extend.T("Horror Movies").Source, "Horror"),
	genreDef("music", extend.T("Music Movies").Source, "Music"),
	genreDef("mystery", extend.T("Mystery Movies").Source, "Mystery"),
	genreDef("romance", extend.T("Romance Movies").Source, "Romance"),
	genreDef("science-fiction", extend.T("Science Fiction Movies").Source, "Science Fiction"),
	genreDef("tv-movie", extend.T("TV Movies").Source, "TV Movie"),
	genreDef("thriller", extend.T("Thrillers").Source, "Thriller"),
	genreDef("war", extend.T("War Movies").Source, "War"),
	genreDef("western", extend.T("Westerns").Source, "Western"),

	networkDef("hbo", extend.T("HBO").Source, "HBO"),
	networkDef("netflix", extend.T("Netflix").Source, "Netflix"),
	networkDef("prime-video", extend.T("Prime Video").Source, "Prime Video"),
	networkDef("disney-plus", extend.T("Disney+").Source, "Disney+"),
	networkDef("apple-tv", extend.T("Apple TV").Source, "Apple TV+", "Apple TV"),
	networkDef("hulu", extend.T("Hulu").Source, "Hulu"),
	networkDef("hbo-max", extend.T("HBO Max").Source, "HBO Max", "Max"),
	networkDef("paramount-plus", extend.T("Paramount+").Source, "Paramount+"),
	networkDef("peacock", extend.T("Peacock").Source, "Peacock"),
	networkDef("amc", extend.T("AMC").Source, "AMC"),
	networkDef("fx", extend.T("FX").Source, "FX"),
	networkDef("showtime", extend.T("Showtime").Source, "Showtime"),
	networkDef("starz", extend.T("Starz").Source, "Starz"),
	networkDef("nbc", extend.T("NBC").Source, "NBC"),
	networkDef("cbs", extend.T("CBS").Source, "CBS"),
	networkDef("abc", extend.T("ABC").Source, "ABC"),
	networkDef("fox", extend.T("FOX").Source, "FOX"),
	networkDef("the-cw", extend.T("The CW").Source, "The CW"),
	networkDef("pbs", extend.T("PBS").Source, "PBS"),
	networkDef("bbc-one", extend.T("BBC One").Source, "BBC One"),
	networkDef("bbc-two", extend.T("BBC Two").Source, "BBC Two"),
	networkDef("channel-4", extend.T("Channel 4").Source, "Channel 4"),
	networkDef("itv", extend.T("ITV").Source, "ITV1", "ITV"),
	networkDef("adult-swim", extend.T("Adult Swim").Source, "Adult Swim"),
	networkDef("comedy-central", extend.T("Comedy Central").Source, "Comedy Central"),
	networkDef("cartoon-network", extend.T("Cartoon Network").Source, "Cartoon Network"),
	networkDef("nickelodeon", extend.T("Nickelodeon").Source, "Nickelodeon"),
	networkDef("disney-channel", extend.T("Disney Channel").Source, "Disney Channel"),
	networkDef("syfy", extend.T("Syfy").Source, "Syfy"),
	networkDef("usa-network", extend.T("USA Network").Source, "USA Network"),
	networkDef("tnt", extend.T("TNT").Source, "TNT"),
	networkDef("crunchyroll", extend.T("Crunchyroll").Source, "Crunchyroll"),
	networkDef("discovery", extend.T("Discovery").Source, "Discovery"),
	networkDef("national-geographic", extend.T("National Geographic").Source, "National Geographic"),

	studioDef("warner-bros", extend.T("Warner Bros.").Source, "Warner Bros. Pictures"),
	studioDef("universal", extend.T("Universal").Source, "Universal Pictures"),
	studioDef("paramount", extend.T("Paramount").Source, "Paramount Pictures"),
	studioDef("columbia", extend.T("Columbia Pictures").Source, "Columbia Pictures"),
	studioDef("disney", extend.T("Walt Disney Pictures").Source, "Walt Disney Pictures"),
	studioDef("20th-century", extend.T("20th Century Studios").Source, "20th Century Fox", "20th Century Studios"),
	studioDef("marvel", extend.T("Marvel Studios").Source, "Marvel Studios"),
	studioDef("lucasfilm", extend.T("Lucasfilm").Source, "Lucasfilm Ltd.", "Lucasfilm"),
	studioDef("dreamworks-animation", extend.T("DreamWorks Animation").Source, "DreamWorks Animation"),
	studioDef("illumination", extend.T("Illumination").Source, "Illumination", "Illumination Entertainment"),
	studioDef("legendary", extend.T("Legendary Pictures").Source, "Legendary Pictures"),
	studioDef("new-line", extend.T("New Line Cinema").Source, "New Line Cinema"),
	studioDef("mgm", extend.T("MGM").Source, "Metro-Goldwyn-Mayer"),
	studioDef("lionsgate", extend.T("Lionsgate").Source, "Lionsgate", "Lions Gate Films"),
	studioDef("blumhouse", extend.T("Blumhouse").Source, "Blumhouse Productions"),
	studioDef("focus-features", extend.T("Focus Features").Source, "Focus Features"),
	studioDef("searchlight", extend.T("Searchlight Pictures").Source, "Searchlight Pictures", "Fox Searchlight Pictures"),
	studioDef("amblin", extend.T("Amblin Entertainment").Source, "Amblin Entertainment"),
}

func init() {
	templateDefs = append(templateDefs, libraryTemplateDefs...)
	library := extend.T("Library").Source
	bundles := []TemplateBundle{
		{ID: "movie-genres", Name: extend.T("Movie genres").Source, Category: library,
			Description: extend.T("One collection per film genre, from Action to Westerns, built from your own catalogue.").Source,
			TemplateIDs: libraryTemplateIDs("genre-movie-")},
		{ID: "tv-networks", Name: extend.T("TV networks").Source, Category: library,
			Description: extend.T("One collection per major TV network and streaming service, such as HBO, Netflix and the BBC.").Source,
			TemplateIDs: libraryTemplateIDs("network-")},
		{ID: "movie-studios", Name: extend.T("Movie studios").Source, Category: library,
			Description: extend.T("One collection per major film studio, from Warner Bros. and Universal to Pixar and Studio Ghibli.").Source,
			TemplateIDs: libraryTemplateIDs("movie-studio-", "pixar", "studio-ghibli", "a24")},
	}
	// The library bundles follow Core Defaults: they replace what every
	// install used to get automatically.
	templateBundleDefs = append(templateBundleDefs[:1:1], append(bundles, templateBundleDefs[1:]...)...)
}

func libraryTemplateIDs(prefix string, extra ...string) []string {
	var out []string
	for _, d := range libraryTemplateDefs {
		if strings.HasPrefix(d.id, prefix) {
			out = append(out, d.id)
		}
	}
	return append(out, extra...)
}
