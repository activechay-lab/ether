import type {
  CollectionSpec,
  CollectionTemplate,
  CollectionTemplateBundle,
  Rule,
} from "../lib/contract";
import { blankSpec } from "../lib/rules";

type T = {
  id: string;
  name: string;
  description: string;
  category: string;
  spec: Partial<CollectionSpec>;
};

const genre = (...g: string[]): Rule => ({ field: "genre", op: "in", value: g });
const notGenre = (...g: string[]): Rule => ({ field: "genre", op: "notIn", value: g });
const minRating = (n: number): Rule => ({ field: "rating", op: "gte", value: n });
const minVotes = (n: number): Rule => ({ field: "votes", op: "gte", value: n });
const maxRuntime = (n: number): Rule => ({ field: "runtime", op: "lte", value: n });
const decade = (...d: string[]): Rule => ({ field: "decade", op: "in", value: d });
const released = (w: string): Rule => ({ field: "released", op: "within", value: w });
const unwatched: Rule = { field: "watched", op: "is", value: false };

const RAW: T[] = [
  // Moods
  {
    id: "mood-feel-good",
    category: "Moods",
    name: "Feel-good favourites",
    description: "Warm comedies and family films rated well by audiences.",
    spec: { rules: [genre("Comedy", "Family", "Romance"), minRating(6.8)], match: "all" },
  },
  {
    id: "mood-edge-of-seat",
    category: "Moods",
    name: "Edge of your seat",
    description: "Tense thrillers and mysteries with strong reviews.",
    spec: { rules: [genre("Thriller", "Mystery"), minRating(6.5)] },
  },
  {
    id: "mood-mind-benders",
    category: "Moods",
    name: "Mind-benders",
    description: "Twisty science fiction and mysteries.",
    spec: { rules: [genre("Science Fiction", "Mystery"), minRating(7)] },
  },
  {
    id: "mood-comfort",
    category: "Moods",
    name: "Comfort watches",
    description: "Familiar favourites to fall back on.",
    spec: { rules: [genre("Comedy", "Animation", "Family"), minVotes(2000)], sort: "personal" },
  },
  {
    id: "mood-date-night",
    category: "Moods",
    name: "Date night",
    description: "Romances and romantic comedies under two hours.",
    spec: { rules: [genre("Romance"), maxRuntime(125)] },
  },
  {
    id: "mood-rainy-day",
    category: "Moods",
    name: "Rainy day epics",
    description: "Long, sweeping adventures for a day indoors.",
    spec: {
      rules: [
        genre("Adventure", "Fantasy", "History"),
        { field: "runtime", op: "gte", value: 140 },
      ],
    },
  },
  // Genres
  {
    id: "genre-horror-essentials",
    category: "Genres",
    name: "Horror essentials",
    description: "Highly rated horror with plenty of votes.",
    spec: { rules: [genre("Horror"), minRating(6.5), minVotes(1000)] },
  },
  {
    id: "genre-sci-fi",
    category: "Genres",
    name: "Science fiction greats",
    description: "The best-rated science fiction in your catalogue.",
    spec: { rules: [genre("Science Fiction"), minRating(7)], sort: "rating" },
  },
  {
    id: "genre-animation",
    category: "Genres",
    name: "Animated worlds",
    description: "Animation for every age.",
    spec: { rules: [genre("Animation")] },
  },
  {
    id: "genre-documentary",
    category: "Genres",
    name: "Documentaries worth your time",
    description: "Acclaimed documentaries.",
    spec: { rules: [genre("Documentary"), minRating(7)] },
  },
  {
    id: "genre-westerns",
    category: "Genres",
    name: "Westerns",
    description: "Frontier stories old and new.",
    spec: { rules: [genre("Western")], sort: "released" },
  },
  {
    id: "genre-war",
    category: "Genres",
    name: "War stories",
    description: "War films and series.",
    spec: { rules: [genre("War")] },
  },
  {
    id: "genre-crime",
    category: "Genres",
    name: "Crime and punishment",
    description: "Crime dramas and heists.",
    spec: { rules: [genre("Crime"), minRating(6.8)] },
  },
  {
    id: "genre-musicals",
    category: "Genres",
    name: "Music and musicals",
    description: "Concert films, biopics and musicals.",
    spec: { rules: [genre("Music")] },
  },
  // Decades
  {
    id: "decade-80s",
    category: "Decades",
    name: "Totally ’80s",
    description: "The decade of synths and sequels.",
    spec: { rules: [decade("1980")] },
  },
  {
    id: "decade-90s",
    category: "Decades",
    name: "’90s rewind",
    description: "Nineties favourites.",
    spec: { rules: [decade("1990")] },
  },
  {
    id: "decade-2000s",
    category: "Decades",
    name: "Noughties hits",
    description: "The best of the 2000s.",
    spec: { rules: [decade("2000"), minRating(6.8)] },
  },
  {
    id: "decade-classics",
    category: "Decades",
    name: "Before 1980",
    description: "Classics from the 1970s and earlier.",
    spec: { rules: [{ field: "year", op: "lte", value: 1979 }], sort: "rating" },
  },
  // Acclaim
  {
    id: "acclaim-top-rated",
    category: "Acclaim",
    name: "Top rated of all time",
    description: "The highest vote averages with a meaningful number of votes.",
    spec: { rules: [minRating(8), minVotes(3000)], sort: "rating", limit: 100 },
  },
  {
    id: "acclaim-hidden-gems",
    category: "Acclaim",
    name: "Hidden gems",
    description: "Great ratings, few votes.",
    spec: { rules: [minRating(7.5), { field: "votes", op: "between", value: [100, 2000] }] },
  },
  {
    id: "acclaim-cult",
    category: "Acclaim",
    name: "Cult classics",
    description: "Older titles with devoted audiences.",
    spec: {
      rules: [
        { field: "year", op: "lte", value: 2000 },
        minRating(7),
        { field: "popularity", op: "lte", value: 40 },
      ],
    },
  },
  {
    id: "acclaim-best-picture",
    category: "Critically Acclaimed",
    name: "Academy Award Best Picture Winners",
    description: "Best Picture winners curated on MDBList.",
    spec: { rules: [minRating(7.5), minVotes(2000)], sort: "rating" },
  },
  {
    id: "acclaim-imdb-top-250",
    category: "Critically Acclaimed",
    name: "IMDb Top 250 Movies",
    description: "Highest-rated films on IMDb.",
    spec: { rules: [minRating(8), minVotes(3000)], sort: "rating", limit: 250 },
  },
  {
    id: "acclaim-rt-certified-fresh",
    category: "Critically Acclaimed",
    name: "Rotten Tomatoes Certified Fresh",
    description: "Certified Fresh releases with strong audience scores.",
    spec: { rules: [minRating(7.3), minVotes(1500)], sort: "rating" },
  },
  {
    id: "acclaim-letterboxd-top-250",
    category: "Critically Acclaimed",
    name: "Letterboxd Top 250 Narrative Features",
    description: "Highest-rated narrative features among cinephiles.",
    spec: { rules: [minRating(7.8), minVotes(1000)], sort: "rating" },
  },
  // Streaming Originals
  {
    id: "stream-netflix-movies",
    category: "Streaming Services",
    name: "Netflix Original Movies",
    description: "Original films from Netflix.",
    spec: { rules: [minRating(6.5)], sort: "popularity" },
  },
  {
    id: "stream-netflix-shows",
    category: "Streaming Services",
    name: "Netflix Original Series",
    description: "Original series from Netflix.",
    spec: { mediaType: "show", rules: [minRating(6.8)], sort: "popularity" },
  },
  {
    id: "stream-apple-tv-shows",
    category: "Streaming Services",
    name: "Apple TV+ Original Series",
    description: "Original series from Apple TV+.",
    spec: { mediaType: "show", rules: [minRating(7)], sort: "popularity" },
  },
  {
    id: "stream-max-shows",
    category: "Streaming Services",
    name: "HBO & Max Original Series",
    description: "Prestige series from HBO and Max.",
    spec: { mediaType: "show", rules: [minRating(7.2)], sort: "rating" },
  },
  {
    id: "stream-disney-plus",
    category: "Streaming Services",
    name: "Disney+ Originals",
    description: "Original films and series on Disney+.",
    spec: { mediaType: "mixed", rules: [minRating(6.5)], sort: "popularity" },
  },
  {
    id: "stream-prime-video",
    category: "Streaming Services",
    name: "Prime Video Originals",
    description: "Original series and films from Prime Video.",
    spec: { mediaType: "mixed", rules: [minRating(6.5)], sort: "popularity" },
  },
  // Trending
  {
    id: "trend-week",
    category: "Trending",
    name: "Trending this week",
    description: "What everyone is watching on TMDB.",
    spec: {
      sources: [{ kind: "tmdbList", list: "trending_week", mediaType: "movie" }],
      sort: "source",
      refresh: "daily",
    },
  },
  {
    id: "trend-popular-tv",
    category: "Trending",
    name: "Popular TV",
    description: "The most popular series right now.",
    spec: {
      mediaType: "show",
      sources: [{ kind: "tmdbList", list: "popular", mediaType: "show" }],
      sort: "source",
    },
  },
  {
    id: "trend-in-cinemas",
    category: "Trending",
    name: "Recently in cinemas",
    description: "Films from the last six months.",
    spec: { rules: [released("6m")], sort: "released" },
  },
  {
    id: "trend-new-to-ether",
    category: "Trending",
    name: "New to Ether",
    description: "Titles added in the last 30 days.",
    spec: {
      rules: [{ field: "addedAt", op: "within", value: "30d" }],
      sort: "added",
      refresh: "hourly",
    },
  },
  {
    id: "trend-airing",
    category: "Trending",
    name: "On the air",
    description: "Series with episodes airing now.",
    spec: {
      mediaType: "show",
      sources: [{ kind: "tmdbList", list: "on_the_air", mediaType: "show" }],
    },
  },
  // Family
  {
    id: "family-night",
    category: "Family",
    name: "Family movie night",
    description: "G and PG films everyone can enjoy.",
    spec: { rules: [{ field: "contentRating", op: "in", value: ["G", "PG"] }, minRating(6.5)] },
  },
  {
    id: "family-kids-tv",
    category: "Family",
    name: "Kids’ TV",
    description: "Shows for younger viewers.",
    spec: {
      mediaType: "show",
      rules: [{ field: "contentRating", op: "in", value: ["TV-Y", "TV-Y7", "TV-G"] }],
    },
  },
  {
    id: "family-animated-adventures",
    category: "Family",
    name: "Animated adventures",
    description: "Animation plus adventure.",
    spec: { rules: [genre("Animation"), genre("Adventure", "Fantasy")], match: "all" },
  },
  {
    id: "family-teens",
    category: "Family",
    name: "Teen picks",
    description: "PG-13 coming-of-age stories.",
    spec: {
      rules: [
        { field: "contentRating", op: "in", value: ["PG-13", "TV-14"] },
        { field: "keyword", op: "in", value: [210], label: "coming of age" },
      ],
    },
  },
  // Short on time
  {
    id: "short-under-90",
    category: "Short on time",
    name: "Under 90 minutes",
    description: "Films you can finish tonight.",
    spec: { rules: [maxRuntime(90), minRating(6.3)] },
  },
  {
    id: "short-half-hour",
    category: "Short on time",
    name: "Half-hour TV",
    description: "Series with short episodes.",
    spec: { mediaType: "show", rules: [maxRuntime(32)] },
  },
  {
    id: "short-weeknight",
    category: "Short on time",
    name: "Weeknight wind-down",
    description: "Short, light and well liked.",
    spec: {
      rules: [maxRuntime(100), genre("Comedy", "Animation"), unwatched],
      targets: {
        collectionsTab: false,
        homeRow: true,
        sectionRow: false,
        users: "all",
        schedule: { days: [1, 2, 3, 4], fromHour: 18 },
      },
    },
  },
  // World
  {
    id: "world-korean",
    category: "World cinema",
    name: "Korean cinema",
    description: "Films and series from South Korea.",
    spec: { mediaType: "mixed", rules: [{ field: "country", op: "in", value: ["KR"] }] },
  },
  {
    id: "world-french",
    category: "World cinema",
    name: "French films",
    description: "French-language cinema.",
    spec: { rules: [{ field: "language", op: "in", value: ["fr"] }] },
  },
  {
    id: "world-japanese",
    category: "World cinema",
    name: "From Japan",
    description: "Japanese films and anime.",
    spec: { mediaType: "mixed", rules: [{ field: "country", op: "in", value: ["JP"] }] },
  },
  {
    id: "world-british",
    category: "World cinema",
    name: "Best of British",
    description: "UK productions.",
    spec: { mediaType: "mixed", rules: [{ field: "country", op: "in", value: ["GB"] }] },
  },
  // Seasonal
  {
    id: "season-halloween",
    category: "Seasonal",
    name: "Halloween",
    description: "Horror and haunted houses for October.",
    spec: {
      rules: [
        genre("Horror"),
        { field: "keyword", op: "in", value: [206], label: "haunted house" },
      ],
      match: "any",
      targets: {
        collectionsTab: true,
        homeRow: true,
        sectionRow: false,
        users: "all",
        schedule: null,
      },
    },
  },
  {
    id: "season-summer",
    category: "Seasonal",
    name: "Summer blockbusters",
    description: "Big, loud and popular.",
    spec: { rules: [genre("Action", "Adventure"), { field: "popularity", op: "gte", value: 150 }] },
  },
  {
    id: "season-holiday",
    category: "Seasonal",
    name: "Holiday classics",
    description: "Family films for the holidays.",
    spec: { rules: [genre("Family"), minRating(6.5)] },
  },
  // Franchises
  {
    id: "franchise-finish",
    category: "Franchises",
    name: "Finish the franchise",
    description: "Collections where viewers have started but not finished.",
    spec: {
      rules: [{ field: "inProgress", op: "is", value: true }],
      sort: "released",
      sortDesc: false,
    },
  },
  {
    id: "franchise-sagas",
    category: "Franchises",
    name: "Epic sagas",
    description: "Titles that belong to a TMDB collection.",
    spec: {
      sources: [{ kind: "tmdbCollection", id: 501, label: "The Meridian Saga" }],
      sort: "released",
      sortDesc: false,
    },
  },
  // Personal
  {
    id: "personal-for-you",
    category: "Personal",
    name: "Picked for each viewer",
    description: "Unwatched titles ordered by each viewer’s taste.",
    spec: {
      rules: [unwatched, minRating(6.5)],
      sort: "personal",
      targets: {
        collectionsTab: false,
        homeRow: true,
        sectionRow: false,
        users: "all",
        schedule: null,
      },
    },
  },
  {
    id: "personal-something-different",
    category: "Personal",
    name: "Something different",
    description: "Well-rated titles outside the usual genres.",
    spec: { rules: [minRating(7.2), unwatched], exclude: [notGenre("Drama")], sort: "random" },
  },
];

export const TEMPLATES: CollectionTemplate[] = RAW.map((t) => {
  const base = blankSpec(t.spec.mediaType ?? "movie");
  const spec: CollectionSpec = {
    ...base,
    ...t.spec,
    name: t.name,
    description: t.description,
    templateId: t.id,
    targets: { ...base.targets, ...(t.spec.targets ?? {}) },
  };
  return { id: t.id, name: t.name, description: t.description, category: t.category, spec };
});

export const TEMPLATE_BUNDLES: CollectionTemplateBundle[] = [
  {
    id: "core-defaults",
    name: "Core Defaults",
    description:
      "Essential library rows: trending films, popular TV, top-rated classics, family movie night, and viewer-personalized picks.",
    templateIds: [
      "trend-week",
      "trend-popular-tv",
      "trend-in-cinemas",
      "acclaim-top-rated",
      "acclaim-hidden-gems",
      "mood-feel-good",
      "short-under-90",
      "family-night",
      "personal-for-you",
    ],
  },
  {
    id: "streaming-originals",
    name: "Streaming Originals",
    description:
      "Curated original films and series from Netflix, Apple TV+, HBO/Max, Disney+, and Prime Video.",
    templateIds: [
      "stream-netflix-movies",
      "stream-netflix-shows",
      "stream-apple-tv-shows",
      "stream-max-shows",
      "stream-disney-plus",
      "stream-prime-video",
    ],
  },
  {
    id: "awards-and-yearly",
    name: "Awards & Critically Acclaimed",
    description:
      "Academy Award Best Picture winners, IMDb Top 250, Rotten Tomatoes Certified Fresh, and Letterboxd favourites.",
    templateIds: [
      "acclaim-best-picture",
      "acclaim-imdb-top-250",
      "acclaim-rt-certified-fresh",
      "acclaim-letterboxd-top-250",
      "acclaim-top-rated",
      "acclaim-cult",
    ],
  },
  {
    id: "seasonal",
    name: "Seasonal & Holidays",
    description: "Scheduled holiday and seasonal rows for Halloween, Summer, and the winter holidays.",
    templateIds: ["season-halloween", "season-summer", "season-holiday"],
  },
  {
    id: "franchises",
    name: "Franchises & Universes",
    description: "Major film sagas and cinematic universes ordered by release.",
    templateIds: ["franchise-finish", "franchise-sagas"],
  },
];
