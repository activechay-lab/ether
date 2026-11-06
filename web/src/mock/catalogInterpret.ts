import type { Chip } from "../pages/catalog/catalogQuery";
/** Deterministic mock-provider response for preview flows only. */
export function interpretDemo(text: string): Chip[] {
  let rest = text.trim();
  const chips: Chip[] = [];
  const take = (re: RegExp, make: (m: RegExpMatchArray) => Chip) => {
    const m = rest.match(re);
    if (m) {
      chips.push(make(m));
      rest = rest.replace(m[0], " ");
    }
  };
  take(/\b(?:19|20)?(\d{2})s\b/i, (m) => ({
    field: "decade",
    op: "is",
    value: String((Number(m[1]!) >= 30 ? 1900 : 2000) + Number(m[1]!)),
  }));
  take(/\b(?:rated\s+)?(?:over|above|at least)\s+(\d(?:\.\d+)?)\b/i, (m) => ({
    field: "rating",
    op: "min",
    value: m[1]!,
  }));
  take(/\b(?:under|less than)\s+(\d+(?:\.\d+)?)\s*(h(?:ours?)?|m(?:in(?:utes?)?)?)\b/i, (m) => ({
    field: "runtime",
    op: "max",
    value: String(Number(m[1]!) * (m[2]!.startsWith("h") ? 60 : 1)),
  }));
  take(/\b(?:playable(?: now)?(?: in)?|available now)\b/i, () => ({
    field: "playable",
    op: "is",
    value: "yes",
  }));
  take(/\bcached(?: on debrid)?\b/i, () => ({ field: "cached", op: "is", value: "yes" }));
  for (const [phrase, value] of [
    ["4k|2160p", "4k"],
    ["hdr", "hdr"],
    ["dolby vision|dv", "dv"],
    ["dolby atmos|atmos", "atmos"],
  ])
    take(new RegExp(`\\b(?:${phrase})\\b`, "i"), () => ({
      field: "quality",
      op: "is",
      value: value!,
    }));
  take(/\b(unwatched|watched)\b/i, (m) => ({
    field: "watched",
    op: "is",
    value: m[1]!.toLowerCase(),
  }));
  take(/\b(?:with|starring)\s+([A-Z][\p{L}'-]+(?:\s+[A-Z][\p{L}'-]+){0,3})/u, (m) => ({
    field: "cast",
    op: "is",
    value: m[1]!,
  }));
  for (const [word, genre] of [
    ["comed(?:y|ies)", "Comedy"],
    ["thrillers?", "Thriller"],
    ["dramas?", "Drama"],
    ["horror", "Horror"],
    ["action", "Action"],
    ["animation", "Animation"],
    ["documentar(?:y|ies)", "Documentary"],
    ["romance", "Romance"],
    ["science fiction|sci-fi", "Science Fiction"],
  ])
    take(new RegExp(`\\b(?:${word})\\b`, "i"), () => ({ field: "genre", op: "is", value: genre! }));
  take(/\b(movies?|films?|shows?|series)\b/i, (m) => ({
    field: "type",
    op: "is",
    value: /show|series/i.test(m[1]!) ? "show" : "movie",
  }));
  rest = rest
    .replace(/\b(and|the|that|are|find|me)\b/gi, " ")
    .replace(/\s+/g, " ")
    .trim();
  if (rest) chips.push({ field: "keyword", op: "is", value: rest });
  return chips;
}
