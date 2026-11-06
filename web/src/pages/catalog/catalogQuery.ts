export type Chip = { field: string; op: string; value: string; label?: string; group?: number };
export type QueryState = { chips: Chip[]; mode: "and" | "or"; groupMode?: "and" | "or" };
const allowedFields = new Set([
  "gate",
  "playable",
  "cached",
  "quality",
  "title",
  "type",
  "genre",
  "decade",
  "year",
  "released",
  "dateAdded",
  "rating",
  "votes",
  "popularity",
  "runtime",
  "language",
  "country",
  "certification",
  "studio",
  "keyword",
  "tag",
  "anime",
  "kids",
  "cast",
  "director",
  "writer",
  "composer",
  "collection",
  "watched",
  "progress",
  "rated",
  "watchlist",
  "etherAdded",
  "status",
  "seasons",
  "episodes",
]);
export const emptyQuery: QueryState = { chips: [], mode: "and" };
export function readQuery(raw: string | null): QueryState {
  try {
    const value = JSON.parse(raw || "{}");
    if (
      !Array.isArray(value.chips) ||
      value.chips.length > 32 ||
      !["and", "or"].includes(value.mode)
    )
      return emptyQuery;
    return {
      ...value,
      chips: value.chips.filter(
        (c: Chip) =>
          c &&
          typeof c.field === "string" &&
          allowedFields.has(c.field) &&
          typeof c.value === "string" &&
          c.value.length <= 256 &&
          (!c.group || (Number.isInteger(c.group) && c.group >= 0 && c.group <= 3)) &&
          ["is", "min", "max", "not"].includes(c.op),
      ),
    };
  } catch {
    return emptyQuery;
  }
}
export function chipFilter(c: Chip, viewer: string): Record<string, unknown> {
  const number = Number(c.value);
  const ranges: Record<string, [string, string]> = {
    year: ["yearFrom", "yearTo"],
    rating: ["minRating", "maxRating"],
    runtime: ["minRuntime", "maxRuntime"],
    votes: ["minVotes", "maxVotes"],
    popularity: ["minPopularity", "maxPopularity"],
    seasons: ["minSeasons", "maxSeasons"],
    episodes: ["minEpisodes", "maxEpisodes"],
  };
  if (ranges[c.field])
    return {
      [ranges[c.field]![c.op === "max" ? 1 : 0]]: c.field === "runtime" ? number * 60e9 : number,
      ...(c.op === "is"
        ? { [ranges[c.field]![1]]: c.field === "runtime" ? number * 60e9 : number }
        : {}),
    };
  if (c.field === "decade") return { yearFrom: number, yearTo: number + 9 };
  const arrays: Record<string, string> = {
    genre: "genres",
    language: "languages",
    country: "countries",
    certification: "contentRatings",
    keyword: "keywords",
  };
  if (arrays[c.field]) return { [arrays[c.field]!]: [c.value] };
  if (c.field === "tag") return { tags: [c.value] };
  if (c.field === "anime" || c.field === "kids") return { [c.field]: true };
  if (c.field === "collection") return { collections: [number] };
  if (["cast", "director", "writer", "composer"].includes(c.field))
    return {
      credits: [{ ...(number > 0 ? { people: [number] } : { names: [c.value] }), kind: c.field }],
    };
  if (c.field === "dateAdded") {
    const stamp = Date.parse(c.value) / 1000;
    return c.op === "is"
      ? { dateAddedFrom: stamp, dateAddedTo: stamp + 86399 }
      : c.op === "max"
        ? { dateAddedTo: stamp + 86399 }
        : { dateAddedFrom: stamp };
  }
  if (c.field === "etherAdded") {
    const stamp = Date.parse(c.value) / 1000;
    return c.op === "is"
      ? { etherAddedFrom: stamp, etherAddedTo: stamp + 86399 }
      : c.op === "max"
        ? { etherAddedTo: stamp + 86399 }
        : { etherAddedFrom: stamp };
  }
  if (c.field === "watchlist")
    return {
      __state: { operator: "and", onWatchlist: c.value === "true", userId: Number(viewer) },
    };
  if (c.field === "released")
    return {
      releasedFilters: [
        {
          op: c.op === "max" ? "<=" : c.op === "is" ? "=" : ">=",
          unix: Date.parse(c.value) / 1000,
          spanDay: true,
        },
      ],
    };
  if (c.field === "status") return { statuses: [c.value] };
  if (c.field === "studio") return { studio: c.value };
  if (c.field === "gate") return { releaseGate: c.value };
  if (c.field === "playable") return { playable: c.value };
  if (c.field === "cached") return { debridCached: c.value };
  if (c.field === "quality") return { qualities: [c.value] };
  if (c.field === "type") return { type: c.value };
  if (c.field === "title") return { query: c.value };
  if (c.field === "watched")
    return {
      __state: { operator: "and", unwatched: c.value === "unwatched", userId: Number(viewer) },
    };
  if (c.field === "progress")
    return { __state: { operator: "and", inProgress: c.value === "true", userId: Number(viewer) } };
  if (c.field === "rated")
    return {
      __state: {
        operator: "and",
        stateField: "lastRatedAt",
        stateOperator: c.value === "true" ? ">" : "=",
        stateValue: 0,
        userId: Number(viewer),
      },
    };
  return {};
}
export function compileQuery(state: QueryState, viewer = "") {
  if (!state.chips.length) return {};
  const leaf = (chip: Chip) => {
    const filter = chipFilter(chip, viewer);
    return filter.__state || { operator: "and", filter };
  };
  const leaves = (chips: Chip[], operator: string) => {
    const quality = operator === "and" ? chips.filter((c) => c.field === "quality") : [];
    return [
      ...chips.filter((c) => !quality.includes(c)).map(leaf),
      ...(quality.length
        ? [{ operator: "and", filter: { qualities: [...new Set(quality.map((c) => c.value))] } }]
        : []),
    ];
  };
  const groups = [...new Set(state.chips.map((c) => c.group || 0))];
  return {
    group: {
      operator: state.mode,
      children: groups.flatMap((group) =>
        group === 0
          ? leaves(
              state.chips.filter((c) => !c.group),
              state.mode,
            )
          : [
              {
                operator: state.groupMode || "and",
                children: leaves(
                  state.chips.filter((c) => c.group === group),
                  state.groupMode || "and",
                ),
              },
            ],
      ),
    },
  };
}
