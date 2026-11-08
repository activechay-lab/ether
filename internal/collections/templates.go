package collections

import (
	"github.com/braedonsaunders/ether/internal/core"
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

const (
	catFranchise = "Franchises & universes"
	catDirectors = "Directors"
	catActors    = "Actors"
	catDecades   = "Decades"
	catAcclaimed = "Critically acclaimed"
	catMoods     = "Moods"
	catStreaming = "Streaming services"
	catSeasonal  = "Seasonal"
	catTop       = "Top lists"
	catWorld     = "World cinema"
	catFamily    = "Family"
	catTV        = "TV"
	catNew       = "New releases"
	catForYou    = "For each viewer"
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
	{"star-wars", "Star Wars Saga", "Every Star Wars film, in release order.", catFranchise,
		with(tpl("Star Wars Saga", MediaMovie, srcs(coll(10, "Star Wars Collection"))), chronology, accent("#d4a017"))},
	{"middle-earth", "Middle-earth", "The Lord of the Rings and The Hobbit trilogies.", catFranchise,
		with(tpl("Middle-earth", MediaMovie, srcs(coll(119, "The Lord of the Rings Collection"), coll(121938, "The Hobbit Collection"))), chronology)},
	{"wizarding-world", "The Wizarding World", "Harry Potter and Fantastic Beasts.", catFranchise,
		with(tpl("The Wizarding World", MediaMovie, srcs(coll(1241, "Harry Potter Collection"), coll(435259, "Fantastic Beasts Collection"))), chronology)},
	{"marvel-cinematic-universe", "Marvel Cinematic Universe", "Every MCU film, from Iron Man on, in release order.", catFranchise,
		with(tpl("Marvel Cinematic Universe", MediaMovie, srcs(keywordID(180547, "marvel cinematic universe (mcu)")), mkRule("votes", "gte", 300)), chronology, limit(0), accent("#c8102e"))},
	{"james-bond", "James Bond 007", "Every official Bond film.", catFranchise,
		with(tpl("James Bond 007", MediaMovie, srcs(coll(645, "James Bond Collection"))), chronology)},
	{"jurassic", "Jurassic Park & World", "Life finds a way.", catFranchise,
		with(tpl("Jurassic Park & World", MediaMovie, srcs(coll(328, "Jurassic Park Collection"))), chronology)},
	{"fast-and-furious", "Fast & Furious", "Every film in the Fast & Furious saga, in release order.", catFranchise,
		with(tpl("Fast & Furious", MediaMovie, srcs(coll(9485, "The Fast and the Furious Collection"))), chronology, accent("#ff5722"))},
	{"mission-impossible", "Mission: Impossible", "Ethan Hunt and the IMF across every Mission: Impossible film.", catFranchise,
		with(tpl("Mission: Impossible", MediaMovie, srcs(coll(87359, "Mission: Impossible Collection"))), chronology, accent("#b71c1c"))},
	{"pirates-of-the-caribbean", "Pirates of the Caribbean", "Captain Jack Sparrow's swashbuckling pirate saga.", catFranchise,
		with(tpl("Pirates of the Caribbean", MediaMovie, srcs(coll(295, "Pirates of the Caribbean Collection"))), chronology, accent("#6d4c41"))},
	{"monsterverse", "MonsterVerse", "Godzilla and the Titans across Legendary's MonsterVerse.", catFranchise,
		with(tpl("MonsterVerse", MediaMovie, srcs(coll(535313, "Godzilla Collection"))), chronology, accent("#00acc1"))},
	{"john-wick", "John Wick", "Every chapter in the John Wick action saga.", catFranchise,
		with(tpl("John Wick", MediaMovie, srcs(coll(404609, "John Wick Collection"))), chronology, accent("#263238"))},
	{"the-matrix", "The Matrix", "Every film in The Matrix saga.", catFranchise,
		with(tpl("The Matrix", MediaMovie, srcs(coll(2344, "The Matrix Collection"))), chronology, accent("#00c853"))},
	{"alien-saga", "Alien Saga", "In space no one can hear you scream.", catFranchise,
		with(tpl("Alien Saga", MediaMovie, srcs(coll(8091, "Alien Collection"))), chronology, accent("#1b5e20"))},
	{"back-to-the-future", "Back to the Future", "The complete time-travelling DeLorean trilogy.", catFranchise,
		with(tpl("Back to the Future", MediaMovie, srcs(coll(264, "Back to the Future Collection"))), chronology, accent("#ff6f00"))},
	{"indiana-jones", "Indiana Jones", "Every adventure of Dr. Henry Walton Jones Jr.", catFranchise,
		with(tpl("Indiana Jones", MediaMovie, srcs(coll(84, "Indiana Jones Collection"))), chronology, accent("#8d6e63"))},
	{"spider-verse", "Spider-Verse", "Miles Morales across the animated Spider-Verse.", catFranchise,
		with(tpl("Spider-Verse", MediaMovie, srcs(coll(573436, "Spider-Man: Spider-Verse Collection"))), chronology, accent("#d50000"))},
	{"dune-saga", "Dune", "Denis Villeneuve's adaptation of Frank Herbert's Dune.", catFranchise,
		with(tpl("Dune", MediaMovie, srcs(coll(726871, "Dune Collection"))), chronology, accent("#c68400"))},
	{"pixar", "Pixar", "Every Pixar feature.", catFranchise,
		with(tpl("Pixar", MediaMovie, srcs(company(3, "Pixar")), mkRule("genre", "is", "Animation"), mkRule("runtime", "gte", 60), mkRule("votes", "gte", 200)), sortBy("released", true), limit(0))},
	{"studio-ghibli", "Studio Ghibli", "Hand-drawn wonder from Studio Ghibli.", catFranchise,
		with(tpl("Studio Ghibli", MediaMovie, srcs(company(10342, "Studio Ghibli")), mkRule("genre", "is", "Animation"), mkRule("runtime", "gte", 60), mkRule("votes", "gte", 100)), sortBy("released", true), limit(0))},
	{"a24", "A24", "Distinctive films from A24.", catFranchise,
		with(tpl("A24", MediaMovie, srcs(company(41077, "A24")), wellLiked...), sortBy("released", true))},

	// --- Directors --------------------------------------------------------
	{"director-christopher-nolan", "Christopher Nolan", "Every film directed by Christopher Nolan.", catDirectors,
		with(tpl("Directed by Christopher Nolan", MediaMovie, srcs(person(525, "Christopher Nolan", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-denis-villeneuve", "Denis Villeneuve", "Every film directed by Denis Villeneuve.", catDirectors,
		with(tpl("Directed by Denis Villeneuve", MediaMovie, srcs(person(137427, "Denis Villeneuve", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-quentin-tarantino", "Quentin Tarantino", "Every film directed by Quentin Tarantino.", catDirectors,
		with(tpl("Directed by Quentin Tarantino", MediaMovie, srcs(person(138, "Quentin Tarantino", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-steven-spielberg", "Steven Spielberg", "Blockbusters and classics from Steven Spielberg.", catDirectors,
		with(tpl("Directed by Steven Spielberg", MediaMovie, srcs(person(488, "Steven Spielberg", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-martin-scorsese", "Martin Scorsese", "Every film directed by Martin Scorsese.", catDirectors,
		with(tpl("Directed by Martin Scorsese", MediaMovie, srcs(person(1032, "Martin Scorsese", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-stanley-kubrick", "Stanley Kubrick", "The complete Kubrick.", catDirectors,
		with(tpl("Directed by Stanley Kubrick", MediaMovie, srcs(person(240, "Stanley Kubrick", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-hayao-miyazaki", "Hayao Miyazaki", "Every feature by Hayao Miyazaki.", catDirectors,
		with(tpl("Directed by Hayao Miyazaki", MediaMovie, srcs(person(608, "Hayao Miyazaki", "director")), mkRule("genre", "is", "Animation"), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-bong-joon-ho", "Bong Joon-ho", "From Memories of Murder to Parasite.", catDirectors,
		with(tpl("Directed by Bong Joon-ho", MediaMovie, srcs(person(21684, "Bong Joon-ho", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-wes-anderson", "Wes Anderson", "Symmetry, pastel and deadpan.", catDirectors,
		with(tpl("Directed by Wes Anderson", MediaMovie, srcs(person(5655, "Wes Anderson", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},
	{"director-greta-gerwig", "Greta Gerwig", "Films directed by Greta Gerwig.", catDirectors,
		with(tpl("Directed by Greta Gerwig", MediaMovie, srcs(person(45400, "Greta Gerwig", "director")), mkRule("votes", "gte", 150)), sortBy("released", true), limit(0))},

	// --- Actors -------------------------------------------------------------
	{"actor-tom-hanks", "Tom Hanks", "The best-loved films starring Tom Hanks.", catActors,
		with(tpl("Starring Tom Hanks", MediaMovie, srcs(person(31, "Tom Hanks", "cast")), mkRule("votes", "gte", 500)), limit(40))},
	{"actor-meryl-streep", "Meryl Streep", "Films starring Meryl Streep.", catActors,
		with(tpl("Starring Meryl Streep", MediaMovie, srcs(person(5064, "Meryl Streep", "cast")), mkRule("votes", "gte", 300)), limit(40))},
	{"actor-denzel-washington", "Denzel Washington", "Films starring Denzel Washington.", catActors,
		with(tpl("Starring Denzel Washington", MediaMovie, srcs(person(5292, "Denzel Washington", "cast")), mkRule("votes", "gte", 300)), limit(40))},
	{"actor-keanu-reeves", "Keanu Reeves", "From Speed to John Wick.", catActors,
		with(tpl("Starring Keanu Reeves", MediaMovie, srcs(person(6384, "Keanu Reeves", "cast")), mkRule("votes", "gte", 300)), limit(40))},
	{"actor-leonardo-dicaprio", "Leonardo DiCaprio", "Films starring Leonardo DiCaprio.", catActors,
		with(tpl("Starring Leonardo DiCaprio", MediaMovie, srcs(person(6193, "Leonardo DiCaprio", "cast")), mkRule("votes", "gte", 300)), limit(40))},

	// --- Decades ------------------------------------------------------------
	{"decade-1970s", "Best of the 70s", "Well-loved films from the 1970s.", catDecades,
		tpl("Best of the 70s", MediaMovie, catalogSrc(), mkRule("decade", "is", 1970), mkRule("votes", "gte", 500), mkRule("rating", "gte", 6.8))},
	{"decade-1980s", "Best of the 80s", "Well-loved films from the 1980s.", catDecades,
		tpl("Best of the 80s", MediaMovie, catalogSrc(), mkRule("decade", "is", 1980), mkRule("votes", "gte", 800), mkRule("rating", "gte", 6.8))},
	{"decade-1990s", "Best of the 90s", "Well-loved films from the 1990s.", catDecades,
		tpl("Best of the 90s", MediaMovie, catalogSrc(), mkRule("decade", "is", 1990), mkRule("votes", "gte", 1000), mkRule("rating", "gte", 6.8))},
	{"decade-2000s", "Best of the 2000s", "Well-loved films from the 2000s.", catDecades,
		tpl("Best of the 2000s", MediaMovie, catalogSrc(), mkRule("decade", "is", 2000), mkRule("votes", "gte", 1500), mkRule("rating", "gte", 6.8))},
	{"decade-2010s", "Best of the 2010s", "Well-loved films from the 2010s.", catDecades,
		tpl("Best of the 2010s", MediaMovie, catalogSrc(), mkRule("decade", "is", 2010), mkRule("votes", "gte", 1500), mkRule("rating", "gte", 6.8))},

	// --- Critically acclaimed & awards -------------------------------------
	{"acclaimed-movies", "Critically Acclaimed", "Films with outstanding ratings from thousands of viewers.", catAcclaimed,
		with(tpl("Critically Acclaimed", MediaMovie, catalogSrc(), mkRule("rating", "gte", 7.8), mkRule("votes", "gte", 3000)), sortBy("rating", true))},
	{"acclaimed-series", "Acclaimed Series", "The best-rated series.", catAcclaimed,
		with(tpl("Acclaimed Series", MediaShow, catalogSrc(), mkRule("rating", "gte", 8.0), mkRule("votes", "gte", 1500)), sortBy("rating", true), excl(notTV))},
	{"top-100-series", "Top 100 Series", "The highest-rated series with a broad audience.", catAcclaimed,
		with(tpl("Top 100 Series", MediaShow, catalogSrc(), mkRule("votes", "gte", 2000), mkRule("rating", "gte", 7.5)), sortBy("rating", true), refresh(RefreshWeekly), excl(notTV))},
	{"top-250-movies", "Top 250 Movies", "The 250 highest-rated films with a broad audience.", catAcclaimed,
		with(tpl("Top 250 Movies", MediaMovie, catalogSrc(), mkRule("votes", "gte", 5000), mkRule("rating", "gte", 7.0)), sortBy("rating", true), limit(250), refresh(RefreshWeekly))},
	{"hidden-gems", "Hidden Gems", "Excellent films far fewer people have seen.", catAcclaimed,
		with(tpl("Hidden Gems", MediaMovie, catalogSrc(), mkRule("rating", "gte", 7.3), mkRule("votes", "between", []int{150, 1500}), mkRule("released", "lte", "-1y")),
			sortBy("rating", true), limit(60), rotateDaily(20), excl(mkRule("genre", "in", []string{"Documentary", "Music", "TV Movie"})))},
	{"modern-classics", "Modern Classics", "Acclaimed films from 2000 to 2015.", catAcclaimed,
		with(tpl("Modern Classics", MediaMovie, catalogSrc(), mkRule("year", "between", []int{2000, 2015}), mkRule("rating", "gte", 7.8), mkRule("votes", "gte", 3000)), sortBy("rating", true))},
	{"awards-oscar-winners", "Oscar Winners", "Academy Award winning films across categories, synced from MDBList.", catAcclaimed,
		with(mdbTpl("Oscar Winners", MediaMovie, "https://mdblist.com/lists/atalante421/oscar-winners/json", "#d4af37"), refresh(RefreshWeekly))},
	{"awards-golden-globes-winners", "Golden Globes Winners", "Golden Globe winning films across categories, synced from MDBList.", catAcclaimed,
		with(mdbTpl("Golden Globes Winners", MediaMovie, "https://mdblist.com/lists/atalante421/golden-globe-winners/json", "#c5a028"), refresh(RefreshWeekly))},
	{"best-of-2023", "Best of 2023", "Top-rated 2023 releases from Rotten Tomatoes end-of-year picks.", catAcclaimed,
		with(mdbTpl("Best of 2023", MediaMovie, "https://mdblist.com/lists/siskourso/rotten-tomatoes-best-of-2023/json", "#fa320a"), refresh(RefreshWeekly))},
	{"best-of-2024", "Best of 2024", "Top-rated 2024 releases from Rotten Tomatoes end-of-year picks.", catAcclaimed,
		with(mdbTpl("Best of 2024", MediaMovie, "https://mdblist.com/lists/siskourso/rotten-tomatoes-best-of-2024-ed84tni24k/json", "#fa320a"), refresh(RefreshWeekly))},
	{"best-of-2025", "Best of 2025", "Top-rated 2025 releases from Rotten Tomatoes end-of-year picks.", catAcclaimed,
		with(mdbTpl("Best of 2025", MediaMovie, "https://mdblist.com/lists/siskourso/rotten-tomatoes-best-of-2025/json", "#fa320a"), refresh(RefreshWeekly))},
	{"criterion-collection", "The Criterion Collection", "Landmark cinema in The Criterion Collection, synced in full from MDBList.", catAcclaimed,
		with(mdbTpl("The Criterion Collection", MediaMovie, "https://mdblist.com/lists/reptauros/the-criterion-collection/json", "#1c1c1c"), limit(0), refresh(RefreshWeekly))},
	{"ifc-films", "IFC Films", "Independent cinema distributed by IFC Films, synced from MDBList.", catAcclaimed,
		with(mdbTpl("IFC Films", MediaMovie, "https://mdblist.com/lists/notamongo5/ifc/json", "#1565c0"), refresh(RefreshWeekly))},
	{"genre-top-rated-action", "Top Rated Action", "The highest-rated action films with a broad audience.", catAcclaimed,
		with(tpl("Top Rated Action", MediaMovie, catalogSrc(), mkRule("genre", "is", "Action"), mkRule("rating", "gte", 7.2), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-animation", "Top Rated Animation", "The highest-rated animated features.", catAcclaimed,
		with(tpl("Top Rated Animation", MediaMovie, catalogSrc(), mkRule("genre", "is", "Animation"), mkRule("rating", "gte", 7.3), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-comedy", "Top Rated Comedy", "The best-rated comedies with lasting appeal.", catAcclaimed,
		with(tpl("Top Rated Comedy", MediaMovie, catalogSrc(), mkRule("genre", "is", "Comedy"), mkRule("rating", "gte", 7.2), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-crime", "Top Rated Crime", "The most acclaimed crime and heist films.", catAcclaimed,
		with(tpl("Top Rated Crime", MediaMovie, catalogSrc(), mkRule("genre", "is", "Crime"), mkRule("rating", "gte", 7.3), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-drama", "Top Rated Drama", "Character-led, story-first dramas with top viewer scores.", catAcclaimed,
		with(tpl("Top Rated Drama", MediaMovie, catalogSrc(), mkRule("genre", "is", "Drama"), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-horror", "Top Rated Horror", "The highest-rated horror films with a strong vote floor.", catAcclaimed,
		with(tpl("Top Rated Horror", MediaMovie, catalogSrc(), mkRule("genre", "is", "Horror"), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-scifi", "Top Rated Science Fiction", "Science fiction films at the top of viewer ratings.", catAcclaimed,
		with(tpl("Top Rated Science Fiction", MediaMovie, catalogSrc(), mkRule("genre", "is", "Science Fiction"), mkRule("rating", "gte", 7.3), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},
	{"genre-top-rated-thriller", "Top Rated Thriller", "Suspense and thriller films with top audience ratings.", catAcclaimed,
		with(tpl("Top Rated Thriller", MediaMovie, catalogSrc(), mkRule("genre", "is", "Thriller"), mkRule("rating", "gte", 7.3), mkRule("votes", "gte", 1000)), sortBy("rating", true), refresh(RefreshWeekly))},

	// --- Moods ----------------------------------------------------------------
	{"mood-cosy", "Cosy Nights In", "Warm, gentle comedies, romances and family films under two hours.", catMoods,
		with(tpl("Cosy Nights In", MediaMovie, catalogSrc(), joinRules(ruleList(mkRule("genre", "in", []string{"Comedy", "Romance", "Family", "Animation"}), mkRule("runtime", "lte", 115)), wellLiked)...),
			excl(notGrim), rotateDaily(30), accent("#b5651d"))},
	{"mood-mind-bending", "Mind-Bending", "Twists, loops and puzzles that reward a second watch.", catMoods,
		with(tpl("Mind-Bending", MediaMovie, srcs(keyword("plot twist"), keyword("time loop"), keyword("nonlinear timeline"), keyword("alternate reality"), keyword("dream")),
			mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 500)), sortBy("rating", true), accent("#5b2a86"))},
	{"mood-feel-good", "Feel-Good Favourites", "Guaranteed to leave you smiling.", catMoods,
		with(tpl("Feel-Good Favourites", MediaMovie, catalogSrc(), mkRule("genre", "in", []string{"Comedy", "Family", "Music", "Animation"}), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 500)),
			excl(notGrim), rotateDaily(30), accent("#f2a900"))},
	{"mood-edge-of-your-seat", "Edge of Your Seat", "Taut thrillers that never let go.", catMoods,
		with(tpl("Edge of Your Seat", MediaMovie, catalogSrc(), mkRule("genre", "is", "Thriller"), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 1000)),
			excl(mkRule("genre", "in", []string{"Animation", "Family"})), accent("#8b0000"))},
	{"mood-tearjerkers", "Tearjerkers", "Keep the tissues close.", catMoods,
		with(tpl("Tearjerkers", MediaMovie, srcs(keyword("tearjerker"), keyword("tragedy")), mkRule("genre", "is", "Drama"), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 300)), accent("#4a6fa5"))},
	{"mood-date-night", "Date Night", "Romantic comedies and love stories.", catMoods,
		with(tpl("Date Night", MediaMovie, catalogSrc(), mkRule("genre", "is", "Romance"), mkRule("rating", "gte", 6.8), mkRule("votes", "gte", 500)), rotateDaily(30), accent("#c2185b"))},
	{"mood-epic-weekend", "Epic Weekend Watches", "Acclaimed films over two and a half hours, for when you have the time.", catMoods,
		with(tpl("Epic Weekend Watches", MediaMovie, catalogSrc(), mkRule("runtime", "gte", 150), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 2000)), sortBy("rating", true))},
	{"mood-true-stories", "Based on a True Story", "Remarkable true stories.", catMoods,
		with(tpl("Based on a True Story", MediaMovie, srcs(keyword("based on true story")), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 500)))},
	{"short-under-90", "Short on Time", "Well-reviewed films under 90 minutes.", catMoods,
		with(tpl("Short on Time", MediaMovie, catalogSrc(), mkRule("runtime", "lte", 90), mkRule("rating", "gte", 6.5), mkRule("votes", "gte", 500)), rotateDaily(30))},

	// --- Streaming services & Originals -------------------------------------
	{"streaming-netflix", "What's on Netflix", "Popular titles streaming on Netflix in your region.", catStreaming,
		streaming("What's on Netflix", "#e50914", provider(8, "Netflix"))},
	{"streaming-disney-plus", "What's on Disney+", "Popular titles streaming on Disney+ in your region.", catStreaming,
		streaming("What's on Disney+", "#113ccf", provider(337, "Disney Plus"))},
	{"streaming-prime-video", "What's on Prime Video", "Popular titles streaming on Prime Video in your region.", catStreaming,
		streaming("What's on Prime Video", "#00a8e1", provider(9, "Amazon Prime Video"))},
	{"streaming-max", "What's on HBO Max", "Popular titles streaming on HBO Max in your region.", catStreaming,
		streaming("What's on HBO Max", "#002be7", provider(1899, "HBO Max"))},
	{"streaming-apple-tv", "What's on Apple TV", "Popular titles streaming on Apple TV (formerly Apple TV+) in your region.", catStreaming,
		streaming("What's on Apple TV", "#333333", provider(350, "Apple TV"))},
	{"streaming-hulu", "What's on Hulu", "Popular titles streaming on Hulu.", catStreaming,
		streaming("What's on Hulu", "#1ce783", provider(15, "Hulu"))},
	{"streaming-paramount-plus", "What's on Paramount+", "Popular titles streaming on Paramount+.", catStreaming,
		streaming("What's on Paramount+", "#0064ff", provider(2303, "Paramount Plus Premium"), provider(2616, "Paramount Plus Essential"))},
	{"streaming-apple-tv-originals", "Apple TV+ Originals", "Apple TV+ original films and series, curated by the Kometa team on MDBList.", catStreaming,
		mdbTpl("Apple TV+ Originals", MediaMixed, "https://mdblist.com/lists/k0meta/appletv-originals/json", "#333333")},
	{"streaming-disney-plus-originals", "Disney+ Originals", "Disney+ original films and series, curated by the Kometa team on MDBList.", catStreaming,
		mdbTpl("Disney+ Originals", MediaMixed, "https://mdblist.com/lists/k0meta/disney-originals/json", "#113ccf")},
	{"streaming-max-originals", "HBO Max Originals", "HBO and Max original films and series, curated by the Kometa team on MDBList.", catStreaming,
		mdbTpl("HBO Max Originals", MediaMixed, "https://mdblist.com/lists/k0meta/max-originals/json", "#002be7")},
	{"streaming-hulu-originals", "Hulu Originals", "Hulu original films and series, curated by the Kometa team on MDBList.", catStreaming,
		mdbTpl("Hulu Originals", MediaMixed, "https://mdblist.com/lists/k0meta/hulu-originals/json", "#1ce783")},
	{"streaming-netflix-originals", "Netflix Originals", "Netflix-produced films and series, curated by the Kometa team on MDBList.", catStreaming,
		mdbTpl("Netflix Originals", MediaMixed, "https://mdblist.com/lists/k0meta/netflix-originals/json", "#e50914")},
	{"streaming-peacock-originals", "Peacock Originals", "Peacock original films and series, curated by the Kometa team on MDBList.", catStreaming,
		mdbTpl("Peacock Originals", MediaMixed, "https://mdblist.com/lists/k0meta/peacock-originals/json", "#ffb300")},
	{"streaming-prime-video-originals", "Prime Video Originals", "Amazon Prime Video original films and series, curated by the Kometa team on MDBList.", catStreaming,
		mdbTpl("Prime Video Originals", MediaMixed, "https://mdblist.com/lists/k0meta/amazon-originals/json", "#00a8e1")},
	{"streaming-shudder", "Shudder Horror & Thrillers", "Recent horror and thriller titles streaming on Shudder, synced from MDBList.", catStreaming,
		mdbTpl("Shudder Horror & Thrillers", MediaMovie, "https://mdblist.com/lists/snoak/latest-shudder-movies/json", "#b71c1c")},

	// --- Seasonal -------------------------------------------------------------
	{"seasonal-halloween", "Halloween", "Spooky-season favourites.", catSeasonal,
		with(tpl("Halloween", MediaMovie, srcs(keyword("halloween")), mkRule("votes", "gte", 200)), accent("#ff6600"))},
	{"seasonal-halloween-family", "Not-Too-Scary Halloween", "Halloween fun for the whole family.", catSeasonal,
		with(tpl("Not-Too-Scary Halloween", MediaMovie, srcs(keyword("halloween")), mkRule("genre", "in", []string{"Family", "Animation", "Comedy"}), mkRule("votes", "gte", 100)),
			excl(mkRule("genre", "is", "Horror")), accent("#ff8c00"))},
	{"seasonal-christmas", "Christmas Movies", "Festive films for the holidays.", catSeasonal,
		with(tpl("Christmas Movies", MediaMovie, srcs(keyword("christmas")), mkRule("votes", "gte", 200)), accent("#b3000c"))},
	{"seasonal-valentines", "Valentine's Day", "Love stories for February 14th.", catSeasonal,
		with(tpl("Valentine's Day", MediaMovie, srcs(keyword("valentine's day")), mkRule("votes", "gte", 50)), accent("#e91e63"))},
	{"seasonal-easter", "Easter", "Easter-season family and reflective films, synced from MDBList.", catSeasonal,
		with(mdbTpl("Easter", MediaMovie, "https://mdblist.com/lists/fuzi0n/easter-movies/json", "#ab47bc"), refresh(RefreshWeekly))},
	{"seasonal-thanksgiving", "Thanksgiving", "Thanksgiving-themed films for the holiday weekend, synced from MDBList.", catSeasonal,
		with(mdbTpl("Thanksgiving", MediaMovie, "https://mdblist.com/lists/fuzi0n/thanksgiving-movies/json", "#d84315"), refresh(RefreshWeekly))},
	{"seasonal-new-year", "New Year's Eve", "Films set around New Year's Eve and the turn of the year.", catSeasonal,
		with(mdbTpl("New Year's Eve", MediaMovie, "https://mdblist.com/lists/fuzi0n/new-years-movies/json", "#ffd54f"), refresh(RefreshWeekly))},
	{"seasonal-aapi-heritage", "AAPI Heritage Month", "Films and series highlighting Asian American and Pacific Islander voices (Kometa MDBList).", catSeasonal,
		with(mdbTpl("AAPI Heritage Month", MediaMixed, "https://mdblist.com/lists/k0meta/asian-american-pacific-islander-heritage-month/json", "#ec407a"), refresh(RefreshWeekly))},
	{"seasonal-latinx-heritage", "Latinx Heritage Month", "Films and series celebrating Latinx storytellers and themes (Kometa MDBList).", catSeasonal,
		with(mdbTpl("Latinx Heritage Month", MediaMixed, "https://mdblist.com/lists/k0meta/latinx-heritage-month/json", "#ff7043"), refresh(RefreshWeekly))},
	{"seasonal-pride-month", "Pride Month", "LGBTQ-led films and series curated for Pride Month.", catSeasonal,
		with(mdbTpl("Pride Month", MediaMixed, "https://mdblist.com/lists/apollocat/new-in-lgbtq/json", "#7e57c2"), refresh(RefreshWeekly))},

	// --- Top lists & Popular Genres -------------------------------------------
	{"top-trending", "Trending This Week", "What everyone is watching this week.", catTop,
		with(tpl("Trending This Week", MediaMixed, srcs(core.CollectionSource{Kind: "tmdbList", List: "trending_week"})), sortBy("source", false), refresh(RefreshHourly), limit(40))},
	{"top-trending-today", "Trending Today", "Movies and series trending across TMDB in the last 24 hours.", catTop,
		with(tpl("Trending Today", MediaMixed, srcs(core.CollectionSource{Kind: "tmdbList", List: "trending_day"})), sortBy("source", false), refresh(RefreshHourly), limit(40))},
	{"top-trending-movies", "Trending Movies This Week", "Top trending movies on TMDB over the past seven days.", catTop,
		with(tpl("Trending Movies This Week", MediaMovie, srcs(core.CollectionSource{Kind: "tmdbList", List: "trending_week", MediaType: MediaMovie})), sortBy("source", false), refresh(RefreshDaily), limit(60))},
	{"top-trending-shows", "Trending TV This Week", "Top trending TV shows on TMDB over the past seven days.", catTop,
		with(tpl("Trending TV This Week", MediaShow, srcs(core.CollectionSource{Kind: "tmdbList", List: "trending_week", MediaType: MediaShow})), sortBy("source", false), refresh(RefreshDaily), limit(60))},
	{"top-popular-movies", "Popular Right Now", "TMDB's most popular films today.", catTop,
		with(tpl("Popular Right Now", MediaMovie, srcs(core.CollectionSource{Kind: "tmdbList", List: "popular", MediaType: MediaMovie})), sortBy("source", false), refresh(RefreshHourly), limit(40))},
	{"top-popular-shows", "Popular TV Right Now", "TMDB's most popular TV series today.", catTop,
		with(tpl("Popular TV Right Now", MediaShow, srcs(core.CollectionSource{Kind: "tmdbList", List: "popular", MediaType: MediaShow})), sortBy("source", false), refresh(RefreshDaily), limit(60))},
	{"top-now-playing", "Now Playing in Theaters", "Movies currently in theatrical release.", catTop,
		with(tpl("Now Playing in Theaters", MediaMovie, srcs(core.CollectionSource{Kind: "tmdbList", List: "now_playing", MediaType: MediaMovie})), sortBy("source", false), refresh(RefreshDaily), limit(60))},
	{"top-airing-today", "Airing Today", "TV series broadcasting new episodes today.", catTop,
		with(tpl("Airing Today", MediaShow, srcs(core.CollectionSource{Kind: "tmdbList", List: "airing_today", MediaType: MediaShow})), sortBy("source", false), refresh(RefreshHourly), limit(60))},
	{"top-on-the-air", "On the Air This Week", "TV series airing new episodes within the week.", catTop,
		with(tpl("On the Air This Week", MediaShow, srcs(core.CollectionSource{Kind: "tmdbList", List: "on_the_air", MediaType: MediaShow})), sortBy("source", false), refresh(RefreshDaily), limit(60))},
	{"imdb-top-250-movies", "IMDb Top 250 Movies", "The canonical IMDb Top 250 movies list, kept in sync from MDBList.", catTop,
		with(mdbTpl("IMDb Top 250 Movies", MediaMovie, "https://mdblist.com/lists/snoak/imdb-top-250-movies/json", "#f5c518"), limit(250), refresh(RefreshWeekly))},
	{"imdb-top-250-shows", "IMDb Top 250 Shows", "The canonical IMDb Top 250 TV shows list, kept in sync from MDBList.", catTop,
		with(mdbTpl("IMDb Top 250 Shows", MediaShow, "https://mdblist.com/lists/snoak/imdb-top-250-shows/json", "#f5c518"), limit(250), refresh(RefreshWeekly))},
	{"imdb-moviemeter-top-100", "IMDb MovieMeter Top 100", "IMDb MovieMeter's 100 most popular movies right now, synced from MDBList.", catTop,
		with(mdbTpl("IMDb MovieMeter Top 100", MediaMovie, "https://mdblist.com/lists/linaspurinis/imdb-moviemeter-top-100/json", "#e6b800"), limit(100), refresh(RefreshDaily))},
	{"genre-popular-action", "Popular Action", "Crowd-pleasing action films sorted by current popularity.", catTop,
		tpl("Popular Action", MediaMovie, catalogSrc(), mkRule("genre", "is", "Action"), mkRule("votes", "gte", 300))},
	{"genre-popular-adventure", "Popular Adventure", "Trending adventure films driving the most attention right now.", catTop,
		tpl("Popular Adventure", MediaMovie, catalogSrc(), mkRule("genre", "is", "Adventure"), mkRule("votes", "gte", 300))},
	{"genre-popular-animation", "Popular Animation", "Popular animated features across family and adult animation.", catTop,
		tpl("Popular Animation", MediaMovie, catalogSrc(), mkRule("genre", "is", "Animation"), mkRule("votes", "gte", 300))},
	{"genre-popular-comedy", "Popular Comedy", "Comedies pulling the biggest audience right now.", catTop,
		tpl("Popular Comedy", MediaMovie, catalogSrc(), mkRule("genre", "is", "Comedy"), mkRule("votes", "gte", 300))},
	{"genre-popular-crime", "Popular Crime", "Heists, mobsters, and crime thrillers sorted by popularity.", catTop,
		tpl("Popular Crime", MediaMovie, catalogSrc(), mkRule("genre", "is", "Crime"), mkRule("votes", "gte", 300))},
	{"genre-popular-drama", "Popular Drama", "Drama films riding the top of popularity charts.", catTop,
		tpl("Popular Drama", MediaMovie, catalogSrc(), mkRule("genre", "is", "Drama"), mkRule("votes", "gte", 300))},
	{"genre-popular-fantasy", "Popular Fantasy", "Magic, myths, and quests trending across fantasy cinema.", catTop,
		tpl("Popular Fantasy", MediaMovie, catalogSrc(), mkRule("genre", "is", "Fantasy"), mkRule("votes", "gte", 300))},
	{"genre-popular-horror", "Popular Horror", "Horror films drawing the biggest crowds right now.", catTop,
		tpl("Popular Horror", MediaMovie, catalogSrc(), mkRule("genre", "is", "Horror"), mkRule("votes", "gte", 300))},
	{"genre-popular-mystery", "Popular Mystery", "Whodunits and mysteries with the most active audience.", catTop,
		tpl("Popular Mystery", MediaMovie, catalogSrc(), mkRule("genre", "is", "Mystery"), mkRule("votes", "gte", 300))},
	{"genre-popular-romance", "Popular Romance", "Love stories pulling the most viewers right now.", catTop,
		tpl("Popular Romance", MediaMovie, catalogSrc(), mkRule("genre", "is", "Romance"), mkRule("votes", "gte", 300))},
	{"genre-popular-scifi", "Popular Science Fiction", "Sci-fi blockbusters and cult favourites sorted by popularity.", catTop,
		tpl("Popular Science Fiction", MediaMovie, catalogSrc(), mkRule("genre", "is", "Science Fiction"), mkRule("votes", "gte", 300))},
	{"genre-popular-thriller", "Popular Thriller", "Tense, fast-moving thrillers ranked by current popularity.", catTop,
		tpl("Popular Thriller", MediaMovie, catalogSrc(), mkRule("genre", "is", "Thriller"), mkRule("votes", "gte", 300))},

	// --- World cinema ---------------------------------------------------------
	{"world-korean", "Korean Cinema", "Acclaimed films from South Korea.", catWorld,
		tpl("Korean Cinema", MediaMovie, catalogSrc(), mkRule("language", "is", "ko"), mkRule("rating", "gte", 6.8), mkRule("votes", "gte", 200))},
	{"world-japanese", "Japanese Cinema", "Live-action classics and modern hits from Japan.", catWorld,
		with(tpl("Japanese Cinema", MediaMovie, catalogSrc(), mkRule("language", "is", "ja"), mkRule("rating", "gte", 6.8), mkRule("votes", "gte", 200)),
			excl(mkRule("genre", "is", "Animation")))},
	{"world-anime-films", "Anime Films", "Feature-length anime.", catWorld,
		tpl("Anime Films", MediaMovie, catalogSrc(), mkRule("language", "is", "ja"), mkRule("genre", "is", "Animation"), mkRule("votes", "gte", 200))},
	{"world-french", "French Cinema", "Films from France and the French-speaking world.", catWorld,
		tpl("French Cinema", MediaMovie, catalogSrc(), mkRule("language", "is", "fr"), mkRule("rating", "gte", 6.8), mkRule("votes", "gte", 200))},
	{"world-spanish", "Spanish-Language Cinema", "Films from Spain and Latin America.", catWorld,
		tpl("Spanish-Language Cinema", MediaMovie, catalogSrc(), mkRule("language", "is", "es"), mkRule("rating", "gte", 6.8), mkRule("votes", "gte", 200))},
	{"world-bollywood", "Bollywood", "Hindi-language hits.", catWorld,
		tpl("Bollywood", MediaMovie, catalogSrc(), mkRule("language", "is", "hi"), mkRule("rating", "gte", 6.5), mkRule("votes", "gte", 100))},
	{"world-nordic-noir", "Nordic Noir", "Dark crime from Scandinavia.", catWorld,
		tpl("Nordic Noir", MediaMixed, catalogSrc(), mkRule("language", "in", []string{"sv", "da", "no", "is", "fi"}), mkRule("genre", "in", []string{"Crime", "Thriller", "Mystery"}), mkRule("votes", "gte", 50))},
	{"world-k-drama", "K-Dramas", "Binge-worthy Korean series.", catWorld,
		tpl("K-Dramas", MediaShow, catalogSrc(), mkRule("language", "is", "ko"), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 50))},

	// --- Family -------------------------------------------------------------
	{"family-night", "Family Movie Night", "Films everyone can watch together.", catFamily,
		with(tpl("Family Movie Night", MediaMovie, catalogSrc(), mkRule("genre", "is", "Family"), mkRule("rating", "gte", 6.5), mkRule("votes", "gte", 500),
			mkRule("contentRating", "lte", "PG")), rotateDaily(30), accent("#2e7d32"))},
	{"family-animated", "Animated Favourites", "The best-loved animated films.", catFamily,
		with(tpl("Animated Favourites", MediaMovie, catalogSrc(), mkRule("genre", "is", []string{"Animation", "Family"}), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 1000)), sortBy("rating", true))},

	// --- TV -------------------------------------------------------------------
	{"tv-limited-series", "Binge-Worthy Limited Series", "Complete stories you can finish in a weekend.", catTV,
		with(tpl("Binge-Worthy Limited Series", MediaShow, srcs(core.CollectionSource{Kind: "tmdbDiscover", Params: map[string]string{
			"with_type": "2", "vote_count.gte": "300"}}), mkRule("rating", "gte", 7.3)), sortBy("rating", true), excl(notTV))},
	{"tv-sitcoms", "Comfort Sitcoms", "Feel-good comedy series to put on again and again.", catTV,
		with(tpl("Comfort Sitcoms", MediaShow, catalogSrc(), mkRule("genre", "is", "Comedy"), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 300)),
			excl(mkRule("genre", "in", []string{"Animation", "Crime"})))},
	{"tv-prestige-drama", "Prestige Drama", "Landmark drama series.", catTV,
		with(tpl("Prestige Drama", MediaShow, catalogSrc(), mkRule("genre", "is", "Drama"), mkRule("rating", "gte", 8.0), mkRule("votes", "gte", 1000)), sortBy("rating", true))},
	{"tv-docuseries", "Docuseries", "Gripping documentary series.", catTV,
		tpl("Docuseries", MediaShow, catalogSrc(), mkRule("genre", "is", "Documentary"), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 50))},

	// --- New releases -------------------------------------------------------
	{"new-releases", "New Releases", "Films released in the last three months.", catNew,
		with(tpl("New Releases", MediaMovie, catalogSrc(), mkRule("released", "within", "90d")), sortBy("released", true), refresh(RefreshHourly))},
	{"new-series", "New Series", "Shows that premiered in the last two months.", catNew,
		with(tpl("New Series", MediaShow, catalogSrc(), mkRule("released", "within", "60d")), sortBy("released", true), refresh(RefreshHourly))},
	{"recently-added", "Recently Added", "Movies and episodes from the last two weeks, and series or seasons with an episode that aired in that window.", catNew,
		with(tpl("Recently Added", MediaMixed, catalogSrc(), mkRule("addedAt", "within", "14d")), sortBy("added", true), refresh(RefreshHourly))},

	// --- For each viewer -------------------------------------------------------
	{"for-you-unseen-acclaimed", "Acclaimed and Unwatched", "Highly rated films each viewer hasn't seen, ordered by their taste.", catForYou,
		with(tpl("Acclaimed Films You Haven't Seen", MediaMovie, catalogSrc(), mkRule("rating", "gte", 7.8), mkRule("votes", "gte", 2000), mkRule("watched", "is", false)),
			sortBy("personal", true), limit(50), homeRow())},
	{"for-you-comfort-rewatches", "Comfort Rewatches", "Favourites each viewer has already loved, ready to watch again.", catForYou,
		with(tpl("Comfort Rewatches", MediaMixed, catalogSrc(), mkRule("watched", "is", true), mkRule("rating", "gte", 7.0), mkRule("votes", "gte", 200)),
			sortBy("personal", true), limit(40), excl(notTV))},
	{"for-you-any-acclaimed-genre", "Crowd-Pleasers", "Big, broadly loved hits across action, comedy and adventure.", catForYou,
		with(tpl("Crowd-Pleasers", MediaMovie, catalogSrc(), mkRule("genre", "in", []string{"Action", "Comedy", "Adventure"}), mkRule("votes", "gte", 5000), mkRule("rating", "gte", 7.0)),
			sortBy("personal", true), rotateDaily(30))},
	{"any-sci-fi-or-fantasy", "Other Worlds", "Science fiction or fantasy, critically loved.", catForYou,
		with(tpl("Other Worlds", MediaMixed, catalogSrc(), mkRule("genre", "in", []string{"Science Fiction", "Fantasy", "Sci-Fi & Fantasy"}), mkRule("rating", "gte", 7.5), mkRule("votes", "gte", 1000)),
			sortBy("personal", true))},
}
