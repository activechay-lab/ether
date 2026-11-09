package collections

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
)

// Templates are curated, complete specs the Collection Studio starts from.
// They name TMDB entities by id and label; labels are resolved by name if
// an id ever changes. Each previews well on a real catalogue: rules lean on
// what the catalogue mirror knows (genres, years, ratings, votes,
// languages), and anything it only partly knows (keywords, runtime,
// certifications, streaming services) comes from TMDB.
func Templates() []core.CollectionTemplate {
	out := make([]core.CollectionTemplate, 0, len(templateDefs))
	for _, d := range templateDefs {
		sp := d.spec
		sp.TemplateID = d.id
		if sp.Description == "" {
			sp.Description = d.desc
		}
		Normalize(&sp)
		out = append(out, core.CollectionTemplate{ID: d.id, Name: d.name, Description: d.desc, Category: d.cat, Spec: cloneSpec(sp)})
	}
	return out
}

type templateDef struct {
	id, name, desc, cat string
	spec                core.CollectionSpec
}

var (
	catFranchise = extend.T("Franchises & universes").Source
	catDirectors = extend.T("Directors").Source
	catActors    = extend.T("Actors").Source
	catDecades   = extend.T("Decades").Source
	catAcclaimed = extend.T("Critically acclaimed").Source
	catMoods     = extend.T("Moods").Source
	catStreaming = extend.T("Streaming services").Source
	catSeasonal  = extend.T("Seasonal").Source
	catTop       = extend.T("Top lists").Source
	catWorld     = extend.T("World cinema").Source
	catFamily    = extend.T("Family").Source
	catTV        = extend.T("TV").Source
	catNew       = extend.T("New releases").Source
	catForYou    = extend.T("For each viewer").Source
)

// tpl builds a template spec with sensible publishing defaults.
func tpl(name, media string, sources []core.CollectionSource, rules ...core.CollectionRule) core.CollectionSpec {
	return core.CollectionSpec{
		Name: name, MediaType: media, Sources: sources, Match: MatchAll, Rules: rules, Exclude: []core.CollectionRule{},
		Sort: "popularity", SortDesc: true, Limit: 100, Refresh: RefreshDaily, Enabled: true,
		Artwork: core.CollectionArtwork{Mode: "auto"},
		Targets: core.CollectionTargets{CollectionsTab: true, Users: core.AllUsers()},
	}
}

func catalogSrc() []core.CollectionSource { return []core.CollectionSource{{Kind: "catalog"}} }

func coll(id int64, label string) core.CollectionSource {
	return core.CollectionSource{Kind: "tmdbCollection", ID: id, Label: label}
}

func company(id int64, label string) core.CollectionSource {
	return core.CollectionSource{Kind: "tmdbCompany", ID: id, Label: label}
}

func keyword(label string) core.CollectionSource {
	return core.CollectionSource{Kind: "tmdbKeyword", Label: label}
}

func person(id int64, label, role string) core.CollectionSource {
	return core.CollectionSource{Kind: "tmdbPerson", ID: id, Label: label, Role: role}
}

func provider(id int64, label string) core.CollectionSource {
	return core.CollectionSource{Kind: "watchProvider", ID: id, Label: label}
}

func keywordID(id int64, label string) core.CollectionSource {
	return core.CollectionSource{Kind: "tmdbKeyword", ID: id, Label: label}
}

func mdblistSrc(listURL string) []core.CollectionSource {
	return []core.CollectionSource{{Kind: "mdblist", URL: listURL}}
}

func mdbTpl(name, media, listURL, color string) core.CollectionSpec {
	sp := with(tpl(name, media, mdblistSrc(listURL)), sortBy("source", false), refresh(RefreshDaily))
	if color != "" {
		sp.Artwork.Accent = color
	}
	return sp
}

func srcs(s ...core.CollectionSource) []core.CollectionSource { return s }

// streaming is a "what's on <service>" collection: the service's most
// popular films and series with an audience, without talk/news/reality.
func streaming(name, color string, services ...core.CollectionSource) core.CollectionSpec {
	return with(tpl(name, MediaMixed, services, mkRule("votes", "gte", 100)), accent(color), excl(notTV))
}

// with applies small tweaks inline.
func with(sp core.CollectionSpec, fns ...func(*core.CollectionSpec)) core.CollectionSpec {
	for _, fn := range fns {
		fn(&sp)
	}
	return sp
}

func sortBy(mode string, desc bool) func(*core.CollectionSpec) {
	return func(sp *core.CollectionSpec) { sp.Sort, sp.SortDesc = mode, desc }
}

func limit(n int) func(*core.CollectionSpec) { return func(sp *core.CollectionSpec) { sp.Limit = n } }

func refresh(r string) func(*core.CollectionSpec) {
	return func(sp *core.CollectionSpec) { sp.Refresh = r }
}

func excl(rules ...core.CollectionRule) func(*core.CollectionSpec) {
	return func(sp *core.CollectionSpec) { sp.Exclude = append(sp.Exclude, rules...) }
}

func accent(hex string) func(*core.CollectionSpec) {
	return func(sp *core.CollectionSpec) { sp.Artwork.Accent = hex }
}

func rotateDaily(n int) func(*core.CollectionSpec) {
	return func(sp *core.CollectionSpec) { sp.Rotate = &core.CollectionRotate{Size: n, Every: "daily"} }
}

func homeRow() func(*core.CollectionSpec) {
	return func(sp *core.CollectionSpec) { sp.Targets.HomeRow = true }
}

var (
	wellLiked = []core.CollectionRule{mkRule("rating", "gte", 6.8), mkRule("votes", "gte", 300)}
	notGrim   = mkRule("genre", "in", []string{"Horror", "War", "Crime", "Thriller"})
	// notTV drops talk, news, reality and soap programming from show lists.
	notTV      = mkRule("genre", "in", []string{"Talk", "News", "Reality", "Soap"})
	chronology = sortBy("released", false)
)

func joinRules(rs ...[]core.CollectionRule) []core.CollectionRule {
	var out []core.CollectionRule
	for _, x := range rs {
		out = append(out, x...)
	}
	return out
}

func ruleList(rs ...core.CollectionRule) []core.CollectionRule { return rs }

var templateDefs = []templateDef{
	// --- Franchises & universes -------------------------------------------
	{"star-wars", extend.T("Star Wars Saga").Source, extend.T("Every Star Wars film, in release order.").Source, catFranchise,
		with(tpl(extend.T("Star Wars Saga").Source, MediaMovie, srcs(coll(10, "Star Wars Collection"))), chronology, accent("#d4a017"))},
	{"middle-earth", extend.T("Middle-earth").Source, extend.T("The Lord of the Rings and The Hobbit trilogies.").Source, catFranchise,
		with(tpl(extend.T("Middle-earth").Source, MediaMovie, srcs(coll(119, "The Lord of the Rings Collection"), coll(121938, "The Hobbit Collection"))), chronology)},
	{"wizarding-world", extend.T("The Wizarding World").Source, extend.T("Harry Potter and Fantastic Beasts.").Source, catFranchise,
		with(tpl(extend.T("The Wizarding World").Source, MediaMovie, srcs(coll(1241, "Harry Potter Collection"), coll(435259, "Fantastic Beasts Collection"))), chronology)},
	{"marvel-cinematic-universe", extend.T("Marvel Cinematic Universe").Source, extend.T("Every MCU film, from Iron Man on, in release order.").Source, catFranchise,
		with(tpl(extend.T("Marvel Cinematic Universe").Source, MediaMovie, srcs(keywordID(180547, "marvel cinematic universe (mcu)")), mkRule("votes", "gte", 300)), chronology, limit(0), accent("#c8102e"))},
	{"james-bond", extend.T("James Bond 007").Source, extend.T("Every official Bond film.").Source, catFranchise,
		with(tpl(extend.T("James Bond 007").Source, MediaMovie, srcs(coll(645, "James Bond Collection"))), chronology)},
	{"jurassic", extend.T("Jurassic Park & World").Source, extend.T("Life finds a way.").Source, catFranchise,
		with(tpl(extend.T("Jurassic Park & World").Source, MediaMovie, srcs(coll(328, "Jurassic Park Collection"))), chronology)},
	{"fast-and-furious", extend.T("Fast & Furious").Source, extend.T("Every film in the Fast & Furious saga, in release order.").Source, catFranchise,
		with(tpl(extend.T("Fast & Furious").Source, MediaMovie, srcs(coll(9485, "The Fast and the Furious Collection"))), chronology, accent("#ff5722"))},
	{"mission-impossible", extend.T("Mission: Impossible").Source, extend.T("Ethan Hunt and the IMF across every Mission: Impossible film.").Source, catFranchise,
		with(tpl(extend.T("Mission: Impossible").Source, MediaMovie, srcs(coll(87359, "Mission: Impossible Collection"))), chronology, accent("#b71c1c"))},
	{"pirates-of-the-caribbean", extend.T("Pirates of the Caribbean").Source, extend.T("Captain Jack Sparrow's swashbuckling pirate saga.").Source, catFranchise,
		with(tpl(extend.T("Pirates of the Caribbean").Source, MediaMovie, srcs(coll(295, "Pirates of the Caribbean Collection"))), chronology, accent("#6d4c41"))},
	{"monsterverse", extend.T("MonsterVerse").Source, extend.T("Godzilla and the Titans across Legendary's MonsterVerse.").Source, catFranchise,
		with(tpl(extend.T("MonsterVerse").Source, MediaMovie, srcs(coll(535313, "Godzilla Collection"))), chronology, accent("#00acc1"))},
	{"john-wick", extend.T("John Wick").Source, extend.T("Every chapter in the John Wick action saga.").Source, catFranchise,
		with(tpl(extend.T("John Wick").Source, MediaMovie, srcs(coll(404609, "John Wick Collection"))), chronology, accent("#263238"))},
	{"the-matrix", extend.T("The Matrix").Source, extend.T("Every film in The Matrix saga.").Source, catFranchise,
		with(tpl(extend.T("The Matrix").Source, MediaMovie, srcs(coll(2344, "The Matrix Collection"))), chronology, accent("#00c853"))},
	{"alien-saga", extend.T("Alien Saga").Source, extend.T("In space no one can hear you scream.").Source, catFranchise,
		with(tpl(extend.T("Alien Saga").Source, MediaMovie, srcs(coll(8091, "Alien Collection"))), chronology, accent("#1b5e20"))},
	{"back-to-the-future", extend.T("Back to the Future").Source, extend.T("The complete time-travelling DeLorean trilogy.").Source, catFranchise,
		with(tpl(extend.T("Back to the Future").Source, MediaMovie, srcs(coll(264, "Back to the Future Collection"))), chronology, accent("#ff6f00"))},
	{"indiana-jones", extend.T("Indiana Jones").Source, extend.T("Every adventure of Dr. Henry Walton Jones Jr.").Source, catFranchise,
		with(tpl(extend.T("Indiana Jones").Source, MediaMovie, srcs(coll(84, "Indiana Jones Collection"))), chronology, accent("#8d6e63"))},
	{"spider-verse", extend.T("Spider-Verse").Source, extend.T("Miles Morales across the animated Spider-Verse.").Source, catFranchise,
		with(tpl(extend.T("Spider-Verse").Source, MediaMovie, srcs(coll(573436, "Spider-Man: Spider-Verse Collection"))), chronology, accent("#d50000"))},
	{"dune-saga", extend.T("Dune").Source, extend.T("Denis Villeneuve's adaptation of Frank Herbert's Dune.").Source, catFranchise,
		with(tpl(extend.T("Dune").Source, MediaMovie, srcs(coll(726871, "Dune Collection"))), chronology, accent("#c68400"))},
	{"pixar", extend.T("Pixar").Source, extend.T("Every Pixar feature.").Source, catFranchise,
		with(tpl(extend.T("Pixar").Source, MediaMovie, srcs(company(3, "Pixar")), mkRule("genre", "is", "Animation"), mkRule("runtime", "gte", 60), mkRule("votes", "gte", 200)), sortBy("released", true), limit(0))},
	{"studio-ghibli", extend.T("Studio Ghibli").Source, extend.T("Hand-drawn wonder from Studio Ghibli.").Source, catFranchise,
		with(tpl(extend.T("Studio Ghibli").Source, MediaMovie, srcs(company(10342, "Studio Ghibli")), mkRule("genre", "is", "Animation"), mkRule("runtime", "gte", 60), mkRule("votes", "gte", 100)), sortBy("released", true), limit(0))},
	{"a24", extend.T("A24").Source, extend.T("Distinctive films from A24.").Source, catFranchise,
		with(tpl(extend.T("A24").Source, MediaMovie, srcs(company(41077, "A24")), wellLiked...), sortBy("released", true))},

	// --- Directors --------------------------------------------------------
	{"director-christopher-nolan", extend.T("Christopher Nolan").Source, extend.T("Every film directed by Christopher Nolan.").Source, catDirectors,
		with(tpl(extend.T("Directed by Christopher Nolan").Source, MediaMovie, srcs(person(525, "Christopher Nolan", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-denis-villeneuve", extend.T("Denis Villeneuve").Source, extend.T("Every film directed by Denis Villeneuve.").Source, catDirectors,
		with(tpl(extend.T("Directed by Denis Villeneuve").Source, MediaMovie, srcs(person(137427, "Denis Villeneuve", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-quentin-tarantino", extend.T("Quentin Tarantino").Source, extend.T("Every film directed by Quentin Tarantino.").Source, catDirectors,
		with(tpl(extend.T("Directed by Quentin Tarantino").Source, MediaMovie, srcs(person(138, "Quentin Tarantino", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-steven-spielberg", extend.T("Steven Spielberg").Source, extend.T("Blockbusters and classics from Steven Spielberg.").Source, catDirectors,
		with(tpl(extend.T("Directed by Steven Spielberg").Source, MediaMovie, srcs(person(488, "Steven Spielberg", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-martin-scorsese", extend.T("Martin Scorsese").Source, extend.T("Every film directed by Martin Scorsese.").Source, catDirectors,
		with(tpl(extend.T("Directed by Martin Scorsese").Source, MediaMovie, srcs(person(1032, "Martin Scorsese", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-stanley-kubrick", extend.T("Stanley Kubrick").Source, extend.T("The complete Kubrick.").Source, catDirectors,
		with(tpl(extend.T("Directed by Stanley Kubrick").Source, MediaMovie, srcs(person(240, "Stanley Kubrick", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-hayao-miyazaki", extend.T("Hayao Miyazaki").Source, extend.T("Every feature by Hayao Miyazaki.").Source, catDirectors,
		with(tpl(extend.T("Directed by Hayao Miyazaki").Source, MediaMovie, srcs(person(608, "Hayao Miyazaki", "director")), mkRule("genre", "is", "Animation"), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-bong-joon-ho", extend.T("Bong Joon-ho").Source, extend.T("From Memories of Murder to Parasite.").Source, catDirectors,
		with(tpl(extend.T("Directed by Bong Joon-ho").Source, MediaMovie, srcs(person(21684, "Bong Joon-ho", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-wes-anderson", extend.T("Wes Anderson").Source, extend.T("Symmetry, pastel and deadpan.").Source, catDirectors,
		with(tpl(extend.T("Directed by Wes Anderson").Source, MediaMovie, srcs(person(5655, "Wes Anderson", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-greta-gerwig", extend.T("Greta Gerwig").Source, extend.T("Films directed by Greta Gerwig.").Source, catDirectors,
		with(tpl(extend.T("Directed by Greta Gerwig").Source, MediaMovie, srcs(person(45400, "Greta Gerwig", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},

	// --- Actors -------------------------------------------------------------
	{"actor-tom-hanks", extend.T("Tom Hanks").Source, extend.T("The best-loved films starring Tom Hanks.").Source, catActors,
		with(tpl(extend.T("Starring Tom Hanks").Source, MediaMovie, srcs(person(31, "Tom Hanks", "cast")), mkRule("votes", "gte", 500)), limit(40))},
	{"actor-meryl-streep", extend.T("Meryl Streep").Source, extend.T("Films starring Meryl Streep.").Source, catActors,
		with(tpl(extend.T("Starring Meryl Streep").Source, MediaMovie, srcs(person(5064, "Meryl Streep", "cast")), mkRule("votes", "gte", 300)), limit(40))},
	{"actor-denzel-washington", extend.T("Denzel Washington").Source, extend.T("Films starring Denzel Washington.").Source, catActors,
		with(tpl(extend.T("Starring Denzel Washington").Source, MediaMovie, srcs(person(5292, "Denzel Washington", "cast")), mkRule("votes", "gte", 300)), limit(40))},
	{"actor-keanu-reeves", extend.T("Keanu Reeves").Source, extend.T("From Speed to John Wick.").Source, catActors,
		with(tpl(extend.T("Starring Keanu Reeves").Source, MediaMovie, srcs(person(6384, "Keanu Reeves", "cast")), mkRule("votes", "gte", 300)), limit(40))},
	{"actor-leonardo-dicaprio", extend.T("Leonardo DiCaprio").Source, extend.T("Films starring Leonardo DiCaprio.").Source, catActors,
		with(tpl(extend.T("Starring Leonardo DiCaprio").Source, MediaMovie, srcs(person(6193, "Leonardo DiCaprio", "cast")), mkRule("votes", "gte", 300)), limit(40))},

	// --- Decades ------------------------------------------------------------
	{"decade-1970s", extend.T("Best of the 70s").Source, extend.T("Well-loved films from the 1970s.").Source, catDecades,
		tpl(extend.T("Best of the 70s").Source, MediaMovie, catalogSrc(), mkRule("decade", "is", 1970), mkRule("votes", "gte", 500), mkRule("rating", "gte", 6.8))},
	{"decade-1980s", extend.T("Best of the 80s").Source, extend.T("Well-loved films from the 1980s.").Source, catDecades,
		tpl(extend.T("Best of the 80s").Source, MediaMovie, catalogSrc(), mkRule("decade", "is", 1980), mkRule("votes", "gte", 800), mkRule("rating", "gte", 6.8))},
	{"decade-1990s", extend.T("Best of the 90s").Source, extend.T("Well-loved films from the 1990s.").Source, catDecades,
		tpl(extend.T("Best of the 90s").Source, MediaMovie, catalogSrc(), mkRule("decade", "is", 1990), mkRule("votes", "gte", 1000), mkRule("rating", "gte", 6.8))},
	{"decade-2000s", extend.T("Best of the 2000s").Source, extend.T("Well-loved films from the 2000s.").Source, catDecades,
		tpl(extend.T("Best of the 2000s").Source, MediaMovie, catalogSrc(), mkRule("decade", "is", 2000), mkRule("votes", "gte", 1500), mkRule("rating", "gte", 6.8))},
	{"decade-2010s", extend.T("Best of the 2010s").Source, extend.T("Well-loved films from the 2010s.").Source, catDecades,
		tpl(extend.T("Best of the 2010s").Source, MediaMovie, catalogSrc(), mkRule("decade", "is", 2010), mkRule("votes", "gte", 1500), mkRule("rating", "gte", 6.8))},

	// --- Critically acclaimed & awards -------------------------------------
	{"acclaimed-movies", extend.T("Critically Acclaimed").Source, extend.T("Films with outstanding ratings from thousands of viewers.").Source, catAcclaimed,
		with(tpl(extend.T("Critically Acclaimed").Source, MediaMovie, catalogSrc(), mkRule("rating", "gte", 7.8), mkRule("votes", "gte", 3000)), sortBy("rating", true))},
	{"acclaimed-series", extend.T("Acclaimed Series").Source, extend.T("The best-rated series.").Source, catAcclaimed,
		with(tpl(extend.T("Acclaimed Series").Source, MediaShow, catalogSrc(), mkRule("rating", "gte", 8.0), mkRule("votes", "gte", 1500)), sortBy("rating", true), excl(notTV))},
	{"top-100-series", extend.T("Top 100 Series").Source, extend.T("The highest-rated series with a broad audience.").Source, catAcclaimed,
		with(tpl(extend.T("Top 100 Series").Source, MediaShow, catalogSrc(), mkRule("votes", "gte", 2000), mkRule("rating", "gte", 7.5)), sortBy("rating", true), refresh(RefreshWeekly), excl(notTV))},
	{"top-250-movies", extend.T("Top 250 Movies").Source, extend.T("The 250 highest-rated films with a broad audience.").Source, catAcclaimed,
		with(tpl(extend.T("Top 250 Movies").Source, MediaMovie, catalogSrc(), mkRule("votes", "gte", 5000), mkRule("rating", "gte", 7.0)), sortBy("rating", true), limit(250), refresh(RefreshWeekly))},
	{"hidden-gems", extend.T("Hidden Gems").Source, extend.T("Excellent films far fewer people have seen.").Source, catAcclaimed,
		with(tpl(extend.T("Hidden Gems").Source, MediaMovie, catalogSrc(), mkRule("rating", "gte", 7.3), mkRule("votes", "between", []int{150, 1500}), mkRule("released", "lte", "-1y")),
			sortBy("rating", true), limit(60), rotateDaily(20), excl(mkRule("genre", "in", []string{"Documentary", "Music", "TV Movie"})))},
	{"modern-classics", extend.T("Modern Classics").Source, extend.T("Acclaimed films from 2000 to 2015.").Source, catAcclaimed,
		with(tpl(extend.T("Modern Classics").Source, MediaMovie, catalogSrc(), mkRule("year", "between", []int{2000, 2015}), mkRule("rating", "gte", 7.8), mkRule("votes", "gte", 3000)), sortBy("rating", true))},
	{"awards-oscar-winners", extend.T("Oscar Winners").Source, extend.T("Academy Award winning films across categories, synced from MDBList.").Source, catAcclaimed,
		with(mdbTpl(extend.T("Oscar Winners").Source, MediaMovie, "https://mdblist.com/lists/atalante421/oscar-winners/json", "#d4af37"), refresh(RefreshWeekly))},
	{"awards-golden-globes-winners", extend.T("Golden Globes Winners").Source, extend.T("Golden Globe winning films across categories, synced from MDBList.").Source, catAcclaimed,
		with(mdbTpl(extend.T("Golden Globes Winners").Source, MediaMovie, "https://mdblist.com/lists/atalante421/golden-globe-winners/json", "#c5a028"), refresh(RefreshWeekly))},
	{"best-of-2023", extend.T("Best of 2023").Source, extend.T("Top-rated 2023 releases from Rotten Tomatoes end-of-year picks.").Source, catAcclaimed,
		with(mdbTpl(extend.T("Best of 2023").Source, MediaMovie, "https://mdblist.com/lists/siskourso/rotten-tomatoes-best-of-2023/json", "#fa320a"), refresh(RefreshWeekly))},
	{"best-of-2024", extend.T("Best of 2024").Source, extend.T("Top-rated 2024 releases from Rotten Tomatoes end-of-year picks.").Source, catAcclaimed,
		with(mdbTpl(extend.T("Best of 2024").Source, MediaMovie, "https://mdblist.com/lists/siskourso/rotten-tomatoes-best-of-2024-ed84tni24k/json", "#fa320a"), refresh(RefreshWeekly))},
	{"best-of-2025", extend.T("Best of 2025").Source, extend.T("Top-rated 2025 releases from Rotten Tomatoes end-of-year picks.").Source, catAcclaimed,
		with(mdbTpl(extend.T("Best of 2025").Source, MediaMovie, "https://mdblist.com/lists/siskourso/rotten-tomatoes-best-of-2025/json", "#fa320a"), refresh(RefreshWeekly))},
	{"criterion-collection", extend.T("The Criterion Collection").Source, extend.T("Landmark cinema in The Criterion Collection, synced in full from MDBList.").Source, catAcclaimed,
		with(mdbTpl(extend.T("The Criterion Collection").Source, MediaMovie, "https://mdblist.com/lists/reptauros/the-criterion-collection/json", "#1c1c1c"), limit(0), refresh(RefreshWeekly))},
	{"ifc-films", extend.T("IFC Films").Source, extend.T("Independent cinema distributed by IFC Films, synced from MDBList.").Source, catAcclaimed,
		with(mdbTpl(extend.T("IFC Films").Source, MediaMovie, "https://mdblist.com/lists/notamongo5/ifc/json", "#1565c0"), refresh(RefreshWeekly))},
	{"genre-top-rated-action", extend.T("Top Rated Action").Source, extend.T("The highest-rated action films with a broad audience.").Source, catAcclaimed,
		with(tpl(extend.T("Top Rated Action").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Action"), mkRule("rating", "gte", 7.2), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-animation", extend.T("Top Rated Animation").Source, extend.T("The highest-rated animated features.").Source, catAcclaimed,
		with(tpl(extend.T("Top Rated Animation").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Animation"), mkRule("rating", "gte", 7.3), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-comedy", extend.T("Top Rated Comedy").Source, extend.T("The best-rated comedies with lasting appeal.").Source, catAcclaimed,
		with(tpl(extend.T("Top Rated Comedy").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Comedy"), mkRule("rating", "gte", 7.2), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-crime", extend.T("Top Rated Crime").Source, extend.T("The most acclaimed crime and heist films.").Source, catAcclaimed,
		with(tpl(extend.T("Top Rated Crime").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Crime"), mkRule("rating", "gte", 7.3), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-drama", extend.T("Top Rated Drama").Source, extend.T("Character-led, story-first dramas with top viewer scores.").Source, catAcclaimed,
		with(tpl(extend.T("Top Rated Drama").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Drama"), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-horror", extend.T("Top Rated Horror").Source, extend.T("The highest-rated horror films with a strong vote floor.").Source, catAcclaimed,
		with(tpl(extend.T("Top Rated Horror").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Horror"), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-scifi", extend.T("Top Rated Science Fiction").Source, extend.T("Science fiction films at the top of viewer ratings.").Source, catAcclaimed,
		with(tpl(extend.T("Top Rated Science Fiction").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Science Fiction"), mkRule("rating", "gte", 7.3), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-thriller", extend.T("Top Rated Thriller").Source, extend.T("Suspense and thriller films with top audience ratings.").Source, catAcclaimed,
		with(tpl(extend.T("Top Rated Thriller").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Thriller"), mkRule("rating", "gte", 7.3), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},

	// --- Moods ----------------------------------------------------------------
	{"mood-cosy", extend.T("Cosy Nights In").Source, extend.T("Warm, gentle comedies, romances and family films under two hours.").Source, catMoods,
		with(tpl(extend.T("Cosy Nights In").Source, MediaMovie, catalogSrc(), joinRules(ruleList(mkRule("genre", "in", []string{"Comedy", "Romance", "Family", "Animation"}), mkRule("runtime", "lte", 115)), wellLiked)...),
			excl(notGrim), rotateDaily(30), accent("#b5651d"))},
	{"mood-mind-bending", extend.T("Mind-Bending").Source, extend.T("Twists, loops and puzzles that reward a second watch.").Source, catMoods,
		with(tpl(extend.T("Mind-Bending").Source, MediaMovie, srcs(keyword("plot twist"), keyword("time loop"), keyword("nonlinear timeline"), keyword("alternate reality"), keyword("dream")),
			mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 500)), sortBy("rating", true), accent("#5b2a86"))},
	{"mood-feel-good", extend.T("Feel-Good Favourites").Source, extend.T("Guaranteed to leave you smiling.").Source, catMoods,
		with(tpl(extend.T("Feel-Good Favourites").Source, MediaMovie, catalogSrc(), mkRule("genre", "in", []string{"Comedy", "Family", "Music", "Animation"}), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 500)),
			excl(notGrim), rotateDaily(30), accent("#f2a900"))},
	{"mood-edge-of-your-seat", extend.T("Edge of Your Seat").Source, extend.T("Taut thrillers that never let go.").Source, catMoods,
		with(tpl(extend.T("Edge of Your Seat").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Thriller"), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 1000)),
			excl(mkRule("genre", "in", []string{"Animation", "Family"})), accent("#8b0000"))},
	{"mood-tearjerkers", extend.T("Tearjerkers").Source, extend.T("Keep the tissues close.").Source, catMoods,
		with(tpl(extend.T("Tearjerkers").Source, MediaMovie, srcs(keyword("tearjerker"), keyword("tragedy")), mkRule("genre", "is", "Drama"), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 300)), accent("#4a6fa5"))},
	{"mood-date-night", extend.T("Date Night").Source, extend.T("Romantic comedies and love stories.").Source, catMoods,
		with(tpl(extend.T("Date Night").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Romance"), mkRule("rating", "gte", 6.8), mkRule("votes", "gte", 500)), rotateDaily(30), accent("#c2185b"))},
	{"mood-epic-weekend", extend.T("Epic Weekend Watches").Source, extend.T("Acclaimed films over two and a half hours, for when you have the time.").Source, catMoods,
		with(tpl(extend.T("Epic Weekend Watches").Source, MediaMovie, catalogSrc(), mkRule("runtime", "gte", 150), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 2000)), sortBy("rating", true))},
	{"mood-true-stories", extend.T("Based on a True Story").Source, extend.T("Remarkable true stories.").Source, catMoods,
		with(tpl(extend.T("Based on a True Story").Source, MediaMovie, srcs(keyword("based on true story")), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 500)))},
	{"short-under-90", extend.T("Short on Time").Source, extend.T("Well-reviewed films under 90 minutes.").Source, catMoods,
		with(tpl(extend.T("Short on Time").Source, MediaMovie, catalogSrc(), mkRule("runtime", "lte", 90), mkRule("rating", "gte", 6.5), mkRule("votes", "gte", 500)), rotateDaily(30))},

	// --- Streaming services & Originals -------------------------------------
	{"streaming-netflix", extend.T("What's on Netflix").Source, extend.T("Popular titles streaming on Netflix in your region.").Source, catStreaming,
		streaming(extend.T("What's on Netflix").Source, "#e50914", provider(8, "Netflix"))},
	{"streaming-disney-plus", extend.T("What's on Disney+").Source, extend.T("Popular titles streaming on Disney+ in your region.").Source, catStreaming,
		streaming(extend.T("What's on Disney+").Source, "#113ccf", provider(337, "Disney Plus"))},
	{"streaming-prime-video", extend.T("What's on Prime Video").Source, extend.T("Popular titles streaming on Prime Video in your region.").Source, catStreaming,
		streaming(extend.T("What's on Prime Video").Source, "#00a8e1", provider(9, "Amazon Prime Video"))},
	{"streaming-max", extend.T("What's on HBO Max").Source, extend.T("Popular titles streaming on HBO Max in your region.").Source, catStreaming,
		streaming(extend.T("What's on HBO Max").Source, "#002be7", provider(1899, "HBO Max"))},
	{"streaming-apple-tv", extend.T("What's on Apple TV").Source, extend.T("Popular titles streaming on Apple TV (formerly Apple TV+) in your region.").Source, catStreaming,
		streaming(extend.T("What's on Apple TV").Source, "#333333", provider(350, "Apple TV"))},
	{"streaming-hulu", extend.T("What's on Hulu").Source, extend.T("Popular titles streaming on Hulu.").Source, catStreaming,
		streaming(extend.T("What's on Hulu").Source, "#1ce783", provider(15, "Hulu"))},
	{"streaming-paramount-plus", extend.T("What's on Paramount+").Source, extend.T("Popular titles streaming on Paramount+.").Source, catStreaming,
		streaming(extend.T("What's on Paramount+").Source, "#0064ff", provider(2303, "Paramount Plus Premium"), provider(2616, "Paramount Plus Essential"))},
	{"streaming-apple-tv-originals", extend.T("Apple TV+ Originals").Source, extend.T("Apple TV+ original films and series, curated by the Kometa team on MDBList.").Source, catStreaming,
		mdbTpl(extend.T("Apple TV+ Originals").Source, MediaMixed, "https://mdblist.com/lists/k0meta/appletv-originals/json", "#333333")},
	{"streaming-disney-plus-originals", extend.T("Disney+ Originals").Source, extend.T("Disney+ original films and series, curated by the Kometa team on MDBList.").Source, catStreaming,
		mdbTpl(extend.T("Disney+ Originals").Source, MediaMixed, "https://mdblist.com/lists/k0meta/disney-originals/json", "#113ccf")},
	{"streaming-max-originals", extend.T("HBO Max Originals").Source, extend.T("HBO and Max original films and series, curated by the Kometa team on MDBList.").Source, catStreaming,
		mdbTpl(extend.T("HBO Max Originals").Source, MediaMixed, "https://mdblist.com/lists/k0meta/max-originals/json", "#002be7")},
	{"streaming-hulu-originals", extend.T("Hulu Originals").Source, extend.T("Hulu original films and series, curated by the Kometa team on MDBList.").Source, catStreaming,
		mdbTpl(extend.T("Hulu Originals").Source, MediaMixed, "https://mdblist.com/lists/k0meta/hulu-originals/json", "#1ce783")},
	{"streaming-netflix-originals", extend.T("Netflix Originals").Source, extend.T("Netflix-produced films and series, curated by the Kometa team on MDBList.").Source, catStreaming,
		mdbTpl(extend.T("Netflix Originals").Source, MediaMixed, "https://mdblist.com/lists/k0meta/netflix-originals/json", "#e50914")},
	{"streaming-peacock-originals", extend.T("Peacock Originals").Source, extend.T("Peacock original films and series, curated by the Kometa team on MDBList.").Source, catStreaming,
		mdbTpl(extend.T("Peacock Originals").Source, MediaMixed, "https://mdblist.com/lists/k0meta/peacock-originals/json", "#ffb300")},
	{"streaming-prime-video-originals", extend.T("Prime Video Originals").Source, extend.T("Amazon Prime Video original films and series, curated by the Kometa team on MDBList.").Source, catStreaming,
		mdbTpl(extend.T("Prime Video Originals").Source, MediaMixed, "https://mdblist.com/lists/k0meta/amazon-originals/json", "#00a8e1")},
	{"streaming-shudder", extend.T("Shudder Horror & Thrillers").Source, extend.T("Recent horror and thriller titles streaming on Shudder, synced from MDBList.").Source, catStreaming,
		mdbTpl(extend.T("Shudder Horror & Thrillers").Source, MediaMovie, "https://mdblist.com/lists/snoak/latest-shudder-movies/json", "#b71c1c")},

	// --- Seasonal -------------------------------------------------------------
	{"seasonal-halloween", extend.T("Halloween").Source, extend.T("Spooky-season favourites.").Source, catSeasonal,
		with(tpl(extend.T("Halloween").Source, MediaMovie, srcs(keyword("halloween")), mkRule("votes", "gte", 200)), accent("#ff6600"))},
	{"seasonal-halloween-family", extend.T("Not-Too-Scary Halloween").Source, extend.T("Halloween fun for the whole family.").Source, catSeasonal,
		with(tpl(extend.T("Not-Too-Scary Halloween").Source, MediaMovie, srcs(keyword("halloween")), mkRule("genre", "in", []string{"Family", "Animation", "Comedy"}), mkRule("votes", "gte", 100)),
			excl(mkRule("genre", "is", "Horror")), accent("#ff8c00"))},
	{"seasonal-christmas", extend.T("Christmas Movies").Source, extend.T("Festive films for the holidays.").Source, catSeasonal,
		with(tpl(extend.T("Christmas Movies").Source, MediaMovie, srcs(keyword("christmas")), mkRule("votes", "gte", 200)), accent("#b3000c"))},
	{"seasonal-valentines", extend.T("Valentine's Day").Source, extend.T("Love stories for February 14th.").Source, catSeasonal,
		with(tpl(extend.T("Valentine's Day").Source, MediaMovie, srcs(keyword("valentine's day")), mkRule("votes", "gte", 50)), accent("#e91e63"))},
	{"seasonal-easter", extend.T("Easter").Source, extend.T("Easter-season family and reflective films, synced from MDBList.").Source, catSeasonal,
		with(mdbTpl(extend.T("Easter").Source, MediaMovie, "https://mdblist.com/lists/fuzi0n/easter-movies/json", "#ab47bc"), refresh(RefreshWeekly))},
	{"seasonal-thanksgiving", extend.T("Thanksgiving").Source, extend.T("Thanksgiving-themed films for the holiday weekend, synced from MDBList.").Source, catSeasonal,
		with(mdbTpl(extend.T("Thanksgiving").Source, MediaMovie, "https://mdblist.com/lists/fuzi0n/thanksgiving-movies/json", "#d84315"), refresh(RefreshWeekly))},
	{"seasonal-new-year", extend.T("New Year's Eve").Source, extend.T("Films set around New Year's Eve and the turn of the year.").Source, catSeasonal,
		with(mdbTpl(extend.T("New Year's Eve").Source, MediaMovie, "https://mdblist.com/lists/fuzi0n/new-years-movies/json", "#ffd54f"), refresh(RefreshWeekly))},
	{"seasonal-aapi-heritage", extend.T("AAPI Heritage Month").Source, extend.T("Films and series highlighting Asian American and Pacific Islander voices (Kometa MDBList).").Source, catSeasonal,
		with(mdbTpl(extend.T("AAPI Heritage Month").Source, MediaMixed, "https://mdblist.com/lists/k0meta/asian-american-pacific-islander-heritage-month/json", "#ec407a"), refresh(RefreshWeekly))},
	{"seasonal-latinx-heritage", extend.T("Latinx Heritage Month").Source, extend.T("Films and series celebrating Latinx storytellers and themes (Kometa MDBList).").Source, catSeasonal,
		with(mdbTpl(extend.T("Latinx Heritage Month").Source, MediaMixed, "https://mdblist.com/lists/k0meta/latinx-heritage-month/json", "#ff7043"), refresh(RefreshWeekly))},
	{"seasonal-pride-month", extend.T("Pride Month").Source, extend.T("LGBTQ-led films and series curated for Pride Month.").Source, catSeasonal,
		with(mdbTpl(extend.T("Pride Month").Source, MediaMixed, "https://mdblist.com/lists/apollocat/new-in-lgbtq/json", "#7e57c2"), refresh(RefreshWeekly))},

	// --- Top lists & Popular Genres -------------------------------------------
	{"top-trending", extend.T("Trending This Week").Source, extend.T("What everyone is watching this week.").Source, catTop,
		with(tpl(extend.T("Trending This Week").Source, MediaMixed, srcs(core.CollectionSource{Kind: "tmdbList", List: "trending_week"})), sortBy("source", false), refresh(RefreshHourly), limit(40))},
	{"top-trending-today", extend.T("Trending Today").Source, extend.T("Movies and series trending across TMDB in the last 24 hours.").Source, catTop,
		with(tpl(extend.T("Trending Today").Source, MediaMixed, srcs(core.CollectionSource{Kind: "tmdbList", List: "trending_day"})), sortBy("source", false), refresh(RefreshHourly), limit(40))},
	{"top-trending-movies", extend.T("Trending Movies This Week").Source, extend.T("Top trending movies on TMDB over the past seven days.").Source, catTop,
		with(tpl(extend.T("Trending Movies This Week").Source, MediaMovie, srcs(core.CollectionSource{Kind: "tmdbList", List: "trending_week", MediaType: MediaMovie})), sortBy("source", false), refresh(RefreshDaily), limit(60))},
	{"top-trending-shows", extend.T("Trending TV This Week").Source, extend.T("Top trending TV shows on TMDB over the past seven days.").Source, catTop,
		with(tpl(extend.T("Trending TV This Week").Source, MediaShow, srcs(core.CollectionSource{Kind: "tmdbList", List: "trending_week", MediaType: MediaShow})), sortBy("source", false), refresh(RefreshDaily), limit(60))},
	{"top-popular-movies", extend.T("Popular Right Now").Source, extend.T("TMDB's most popular films today.").Source, catTop,
		with(tpl(extend.T("Popular Right Now").Source, MediaMovie, srcs(core.CollectionSource{Kind: "tmdbList", List: "popular", MediaType: MediaMovie})), sortBy("source", false), refresh(RefreshHourly), limit(40))},
	{"top-popular-shows", extend.T("Popular TV Right Now").Source, extend.T("TMDB's most popular TV series today.").Source, catTop,
		with(tpl(extend.T("Popular TV Right Now").Source, MediaShow, srcs(core.CollectionSource{Kind: "tmdbList", List: "popular", MediaType: MediaShow})), sortBy("source", false), refresh(RefreshDaily), limit(60))},
	{"top-now-playing", extend.T("Now Playing in Theaters").Source, extend.T("Movies currently in theatrical release.").Source, catTop,
		with(tpl(extend.T("Now Playing in Theaters").Source, MediaMovie, srcs(core.CollectionSource{Kind: "tmdbList", List: "now_playing", MediaType: MediaMovie})), sortBy("source", false), refresh(RefreshDaily), limit(60))},
	{"top-airing-today", extend.T("Airing Today").Source, extend.T("TV series broadcasting new episodes today.").Source, catTop,
		with(tpl(extend.T("Airing Today").Source, MediaShow, srcs(core.CollectionSource{Kind: "tmdbList", List: "airing_today", MediaType: MediaShow})), sortBy("source", false), refresh(RefreshHourly), limit(60))},
	{"top-on-the-air", extend.T("On the Air This Week").Source, extend.T("TV series airing new episodes within the week.").Source, catTop,
		with(tpl(extend.T("On the Air This Week").Source, MediaShow, srcs(core.CollectionSource{Kind: "tmdbList", List: "on_the_air", MediaType: MediaShow})), sortBy("source", false), refresh(RefreshDaily), limit(60))},
	{"imdb-top-250-movies", extend.T("IMDb Top 250 Movies").Source, extend.T("The canonical IMDb Top 250 movies list, kept in sync from MDBList.").Source, catTop,
		with(mdbTpl(extend.T("IMDb Top 250 Movies").Source, MediaMovie, "https://mdblist.com/lists/snoak/imdb-top-250-movies/json", "#f5c518"), limit(250), refresh(RefreshWeekly))},
	{"imdb-top-250-shows", extend.T("IMDb Top 250 Shows").Source, extend.T("The canonical IMDb Top 250 TV shows list, kept in sync from MDBList.").Source, catTop,
		with(mdbTpl(extend.T("IMDb Top 250 Shows").Source, MediaShow, "https://mdblist.com/lists/snoak/imdb-top-250-shows/json", "#f5c518"), limit(250), refresh(RefreshWeekly))},
	{"imdb-moviemeter-top-100", extend.T("IMDb MovieMeter Top 100").Source, extend.T("IMDb MovieMeter's 100 most popular movies right now, synced from MDBList.").Source, catTop,
		with(mdbTpl(extend.T("IMDb MovieMeter Top 100").Source, MediaMovie, "https://mdblist.com/lists/linaspurinis/imdb-moviemeter-top-100/json", "#e6b800"), limit(100), refresh(RefreshDaily))},
	{"genre-popular-action", extend.T("Popular Action").Source, extend.T("Crowd-pleasing action films sorted by current popularity.").Source, catTop,
		tpl(extend.T("Popular Action").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Action"), mkRule("votes", "gte", 300))},
	{"genre-popular-adventure", extend.T("Popular Adventure").Source, extend.T("Trending adventure films driving the most attention right now.").Source, catTop,
		tpl(extend.T("Popular Adventure").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Adventure"), mkRule("votes", "gte", 300))},
	{"genre-popular-animation", extend.T("Popular Animation").Source, extend.T("Popular animated features across family and adult animation.").Source, catTop,
		tpl(extend.T("Popular Animation").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Animation"), mkRule("votes", "gte", 300))},
	{"genre-popular-comedy", extend.T("Popular Comedy").Source, extend.T("Comedies pulling the biggest audience right now.").Source, catTop,
		tpl(extend.T("Popular Comedy").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Comedy"), mkRule("votes", "gte", 300))},
	{"genre-popular-crime", extend.T("Popular Crime").Source, extend.T("Heists, mobsters, and crime thrillers sorted by popularity.").Source, catTop,
		tpl(extend.T("Popular Crime").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Crime"), mkRule("votes", "gte", 300))},
	{"genre-popular-drama", extend.T("Popular Drama").Source, extend.T("Drama films riding the top of popularity charts.").Source, catTop,
		tpl(extend.T("Popular Drama").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Drama"), mkRule("votes", "gte", 300))},
	{"genre-popular-fantasy", extend.T("Popular Fantasy").Source, extend.T("Magic, myths, and quests trending across fantasy cinema.").Source, catTop,
		tpl(extend.T("Popular Fantasy").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Fantasy"), mkRule("votes", "gte", 300))},
	{"genre-popular-horror", extend.T("Popular Horror").Source, extend.T("Horror films drawing the biggest crowds right now.").Source, catTop,
		tpl(extend.T("Popular Horror").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Horror"), mkRule("votes", "gte", 300))},
	{"genre-popular-mystery", extend.T("Popular Mystery").Source, extend.T("Whodunits and mysteries with the most active audience.").Source, catTop,
		tpl(extend.T("Popular Mystery").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Mystery"), mkRule("votes", "gte", 300))},
	{"genre-popular-romance", extend.T("Popular Romance").Source, extend.T("Love stories pulling the most viewers right now.").Source, catTop,
		tpl(extend.T("Popular Romance").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Romance"), mkRule("votes", "gte", 300))},
	{"genre-popular-scifi", extend.T("Popular Science Fiction").Source, extend.T("Sci-fi blockbusters and cult favourites sorted by popularity.").Source, catTop,
		tpl(extend.T("Popular Science Fiction").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Science Fiction"), mkRule("votes", "gte", 300))},
	{"genre-popular-thriller", extend.T("Popular Thriller").Source, extend.T("Tense, fast-moving thrillers ranked by current popularity.").Source, catTop,
		tpl(extend.T("Popular Thriller").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Thriller"), mkRule("votes", "gte", 300))},

	// --- World cinema ---------------------------------------------------------
	{"world-korean", extend.T("Korean Cinema").Source, extend.T("Acclaimed films from South Korea.").Source, catWorld,
		tpl(extend.T("Korean Cinema").Source, MediaMovie, catalogSrc(), mkRule("language", "is", "ko"), mkRule("rating", "gte", 6.8), mkRule("votes", "gte", 200))},
	{"world-japanese", extend.T("Japanese Cinema").Source, extend.T("Live-action classics and modern hits from Japan.").Source, catWorld,
		with(tpl(extend.T("Japanese Cinema").Source, MediaMovie, catalogSrc(), mkRule("language", "is", "ja"), mkRule("rating", "gte", 6.8), mkRule("votes", "gte", 200)),
			excl(mkRule("genre", "is", "Animation")))},
	{"world-anime-films", extend.T("Anime Films").Source, extend.T("Feature-length anime.").Source, catWorld,
		tpl(extend.T("Anime Films").Source, MediaMovie, catalogSrc(), mkRule("language", "is", "ja"), mkRule("genre", "is", "Animation"), mkRule("votes", "gte", 200))},
	{"world-french", extend.T("French Cinema").Source, extend.T("Films from France and the French-speaking world.").Source, catWorld,
		tpl(extend.T("French Cinema").Source, MediaMovie, catalogSrc(), mkRule("language", "is", "fr"), mkRule("rating", "gte", 6.8), mkRule("votes", "gte", 200))},
	{"world-spanish", extend.T("Spanish-Language Cinema").Source, extend.T("Films from Spain and Latin America.").Source, catWorld,
		tpl(extend.T("Spanish-Language Cinema").Source, MediaMovie, catalogSrc(), mkRule("language", "is", "es"), mkRule("rating", "gte", 6.8), mkRule("votes", "gte", 200))},
	{"world-bollywood", extend.T("Bollywood").Source, extend.T("Hindi-language hits.").Source, catWorld,
		tpl(extend.T("Bollywood").Source, MediaMovie, catalogSrc(), mkRule("language", "is", "hi"), mkRule("rating", "gte", 6.5), mkRule("votes", "gte", 100))},
	{"world-nordic-noir", extend.T("Nordic Noir").Source, extend.T("Dark crime from Scandinavia.").Source, catWorld,
		tpl(extend.T("Nordic Noir").Source, MediaMixed, catalogSrc(), mkRule("language", "in", []string{"sv", "da", "no", "is", "fi"}), mkRule("genre", "in", []string{"Crime", "Thriller", "Mystery"}), mkRule("votes", "gte", 50))},
	{"world-k-drama", extend.T("K-Dramas").Source, extend.T("Binge-worthy Korean series.").Source, catWorld,
		tpl(extend.T("K-Dramas").Source, MediaShow, catalogSrc(), mkRule("language", "is", "ko"), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 50))},

	// --- Family -------------------------------------------------------------
	{"family-night", extend.T("Family Movie Night").Source, extend.T("Films everyone can watch together.").Source, catFamily,
		with(tpl(extend.T("Family Movie Night").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", "Family"), mkRule("rating", "gte", 6.5), mkRule("votes", "gte", 500),
			mkRule("contentRating", "lte", "PG")), rotateDaily(30), accent("#2e7d32"))},
	{"family-animated", extend.T("Animated Favourites").Source, extend.T("The best-loved animated films.").Source, catFamily,
		with(tpl(extend.T("Animated Favourites").Source, MediaMovie, catalogSrc(), mkRule("genre", "is", []string{"Animation", "Family"}), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 1000)), sortBy("rating", true))},

	// --- TV -------------------------------------------------------------------
	{"tv-limited-series", extend.T("Binge-Worthy Limited Series").Source, extend.T("Complete stories you can finish in a weekend.").Source, catTV,
		with(tpl(extend.T("Binge-Worthy Limited Series").Source, MediaShow, srcs(core.CollectionSource{Kind: "tmdbDiscover", Params: map[string]string{
			"with_type": "2", "vote_count.gte": "300"}}), mkRule("rating", "gte", 7.3)), sortBy("rating", true), excl(notTV))},
	{"tv-sitcoms", extend.T("Comfort Sitcoms").Source, extend.T("Feel-good comedy series to put on again and again.").Source, catTV,
		with(tpl(extend.T("Comfort Sitcoms").Source, MediaShow, catalogSrc(), mkRule("genre", "is", "Comedy"), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 300)),
			excl(mkRule("genre", "in", []string{"Animation", "Crime"})))},
	{"tv-prestige-drama", extend.T("Prestige Drama").Source, extend.T("Landmark drama series.").Source, catTV,
		with(tpl(extend.T("Prestige Drama").Source, MediaShow, catalogSrc(), mkRule("genre", "is", "Drama"), mkRule("rating", "gte", 8.0), mkRule("votes", "gte", 1000)), sortBy("rating", true))},
	{"tv-docuseries", extend.T("Docuseries").Source, extend.T("Gripping documentary series.").Source, catTV,
		tpl(extend.T("Docuseries").Source, MediaShow, catalogSrc(), mkRule("genre", "is", "Documentary"), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 50))},

	// --- New releases -------------------------------------------------------
	{"new-releases", extend.T("New Releases").Source, extend.T("Films released in the last three months.").Source, catNew,
		with(tpl(extend.T("New Releases").Source, MediaMovie, catalogSrc(), mkRule("released", "within", "90d")), sortBy("released", true), refresh(RefreshHourly))},
	{"new-series", extend.T("New Series").Source, extend.T("Shows that premiered in the last two months.").Source, catNew,
		with(tpl(extend.T("New Series").Source, MediaShow, catalogSrc(), mkRule("released", "within", "60d")), sortBy("released", true), refresh(RefreshHourly))},
	{"recently-added", extend.T("Recently Added").Source, extend.T("Movies and episodes from the last two weeks, and series or seasons with an episode that aired in that window.").Source, catNew,
		with(tpl(extend.T("Recently Added").Source, MediaMixed, catalogSrc(), mkRule("addedAt", "within", "14d")), sortBy("added", true), refresh(RefreshHourly))},

	// --- For each viewer -------------------------------------------------------
	{"for-you-unseen-acclaimed", extend.T("Acclaimed and Unwatched").Source, extend.T("Highly rated films each viewer hasn't seen, ordered by their taste.").Source, catForYou,
		with(tpl(extend.T("Acclaimed Films You Haven't Seen").Source, MediaMovie, catalogSrc(), mkRule("rating", "gte", 7.8), mkRule("votes", "gte", 2000), mkRule("watched", "is", false)),
			sortBy("personal", true), limit(50), homeRow())},
	{"for-you-comfort-rewatches", extend.T("Comfort Rewatches").Source, extend.T("Favourites each viewer has already loved, ready to watch again.").Source, catForYou,
		with(tpl(extend.T("Comfort Rewatches").Source, MediaMixed, catalogSrc(), mkRule("watched", "is", true), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 200)),
			sortBy("personal", true), limit(40), excl(notTV))},
	{"for-you-any-acclaimed-genre", extend.T("Crowd-Pleasers").Source, extend.T("Big, broadly loved hits across action, comedy and adventure.").Source, catForYou,
		with(tpl(extend.T("Crowd-Pleasers").Source, MediaMovie, catalogSrc(), mkRule("genre", "in", []string{"Action", "Comedy", "Adventure"}), mkRule("votes", "gte", 5000), mkRule("rating", "gte", 7.0)),
			sortBy("personal", true), rotateDaily(30))},
	{"any-sci-fi-or-fantasy", extend.T("Other Worlds").Source, extend.T("Science fiction or fantasy, critically loved.").Source, catForYou,
		with(tpl(extend.T("Other Worlds").Source, MediaMixed, catalogSrc(), mkRule("genre", "in", []string{"Science Fiction", "Fantasy", "Sci-Fi & Fantasy"}), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 1000)),
			sortBy("personal", true))},
}
