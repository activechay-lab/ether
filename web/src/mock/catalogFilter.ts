import type { FakeTitle } from "./data";
import { PEOPLE, STUDIOS, KEYWORDS } from "./data";

/** Mirror the owner browse wire contract in mock mode, so browser tasks exercise
 * real filtering rather than receiving an unchanged decorative result grid. */
export function matchesCatalogFilter(
  title: FakeTitle,
  filter: Record<string, any>,
  annotations?: ReadonlyMap<string, string[]>,
  onWatchlist?: (viewerId: number, key: string) => boolean,
): boolean {
  const f = filter;
  const some = (values: string[] | undefined, actual: string[]) =>
    !values?.length || values.some((v) => actual.some((a) => a.toLowerCase() === v.toLowerCase()));
  if (f.group) {
    const visit = (g: any): boolean =>
      g.filter
        ? matchesCatalogFilter(title, g.filter, annotations, onWatchlist)
        : g.unwatched !== undefined
          ? g.unwatched === (Number(title.key.slice(-2)) % 3 !== 0)
          : g.inProgress !== undefined
            ? g.inProgress === (Number(title.key.slice(-2)) % 5 === 0)
            : g.onWatchlist !== undefined
              ? g.onWatchlist ===
                (onWatchlist?.(Number(g.userId), title.key) ??
                  Number(title.key.slice(-2)) % 4 === 0)
              : g.stateField === "lastRatedAt"
                ? (g.stateOperator === ">") === (Number(title.key.slice(-2)) % 2 === 0)
                : g.operator === "or"
                  ? g.children.some(visit)
                  : g.children.every(visit);
    if (!visit(f.group)) return false;
  }
  if (
    f.tags?.some((tag: string) => !(annotations?.get(title.key) || []).includes(tag.toLowerCase()))
  )
    return false;
  if (f.playable && (f.playable === "yes") !== Boolean(title.evidence?.verified)) return false;
  if (
    f.debridCached &&
    (f.debridCached === "yes"
      ? title.evidence?.cached !== true
      : title.evidence?.cached !== undefined)
  )
    return false;
  if (
    f.qualities?.length &&
    (!title.evidence?.verified ||
      !f.qualities.every((quality: string) => title.evidence!.qualities.includes(quality)))
  )
    return false;
  if (f.releaseGate && (title.evidence?.gate || "unknown") !== f.releaseGate) return false;
  if (f.statuses?.length && !f.statuses.includes(title.status)) return false;
  if (
    (f.minSeasons && (title.seasons || 0) < f.minSeasons) ||
    (f.maxSeasons && (title.seasons || 0) > f.maxSeasons) ||
    (f.minEpisodes && (title.episodes || 0) < f.minEpisodes) ||
    (f.maxEpisodes && (title.episodes || 0) > f.maxEpisodes)
  )
    return false;
  if (f.etherAddedFrom && Date.parse(title.addedAt) / 1000 < f.etherAddedFrom) return false;
  if (f.etherAddedTo && Date.parse(title.addedAt) / 1000 > f.etherAddedTo) return false;
  if (f.dateAddedFrom && Date.parse(title.released) / 1000 < f.dateAddedFrom) return false;
  if (f.dateAddedTo && Date.parse(title.released) / 1000 > f.dateAddedTo) return false;
  if (f.kids && !title.genres?.includes("Kids")) return false;
  if (f.anime && !title.keywords.some((id) => KEYWORDS.find((k) => k.id === id)?.name === "anime"))
    return false;
  if (f.type && title.type !== f.type) return false;
  if (f.query && !title.title.toLowerCase().includes(f.query.toLowerCase())) return false;
  if (
    !some(f.genres, title.genres || []) ||
    !some(f.languages, [title.language]) ||
    !some(f.countries, [title.country]) ||
    !some(f.contentRatings, [title.contentRating || ""])
  )
    return false;
  for (const [low, high, value] of [
    ["yearFrom", "yearTo", title.year || 0],
    ["minRating", "maxRating", title.rating || 0],
    ["minVotes", "maxVotes", title.votes],
    ["minPopularity", "maxPopularity", title.popularity],
    ["minRuntime", "maxRuntime", (title.runtimeMinutes || 0) * 60e9],
  ] as [string, string, number][])
    if ((f[low] && value < f[low]) || (f[high] && value > f[high])) return false;
  if (f.studio && STUDIOS.find((s) => s.id === title.studio)?.name !== f.studio) return false;
  if (f.collections?.length && !f.collections.includes(title.collectionId)) return false;
  if (
    f.keywords?.some(
      (name: string) =>
        !title.keywords.some(
          (id) => KEYWORDS.find((k) => k.id === id)?.name.toLowerCase() === name.toLowerCase(),
        ),
    )
  )
    return false;
  if (
    f.credits?.some(
      (credit: any) =>
        !(credit.people || []).some((id: number) => title.people.includes(id)) &&
        !(credit.names || []).some((name: string) =>
          title.people.some(
            (id) => PEOPLE.find((p) => p.id === id)?.name.toLowerCase() === name.toLowerCase(),
          ),
        ),
    )
  )
    return false;
  if (
    f.releasedFilters?.some((date: any) => {
      const stamp = Date.parse(title.released) / 1000;
      return date.op === ">="
        ? stamp < date.unix
        : date.op === "<="
          ? stamp > date.unix + 86399
          : false;
    })
  )
    return false;
  return true;
}
