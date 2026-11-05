import { describe, it, expect } from "vitest";
import { collectionDraft } from "./catalogSavedView";
const query = (chips: unknown[], extra: Record<string, string> = {}) =>
  new URLSearchParams({ filters: JSON.stringify({ mode: "and", chips }), ...extra });
describe("saved catalogue view promotion", () => {
  it("opens a disabled draft with equivalent metadata rules and sort", () => {
    const draft = collectionDraft(
      "Nineties thrillers",
      query(
        [
          { field: "genre", op: "is", value: "Thriller" },
          { field: "decade", op: "is", value: "1990" },
          { field: "runtime", op: "max", value: "120" },
          { field: "rating", op: "min", value: "7" },
        ],
        { type: "movie", sort: "rating", desc: "0" },
      ),
    );
    expect(draft).toMatchObject({
      name: "Nineties thrillers",
      mediaType: "movie",
      match: "all",
      sort: "rating",
      sortDesc: false,
      enabled: false,
      refresh: "live",
      rules: [
        { field: "genre", op: "is", value: "Thriller" },
        { field: "decade", op: "is", value: 1990 },
        { field: "runtime", op: "lte", value: 120 },
        { field: "rating", op: "gte", value: 7 },
      ],
    });
  });
  it("refuses unsupported predicates rather than broadening a saved view", () => {
    for (const chip of [
      { field: "cast", op: "is", value: "Tom Hanks" },
      { field: "watched", op: "is", value: "unwatched" },
      { field: "status", op: "is", value: "Ended" },
      { field: "genre", op: "is", value: "Comedy", group: 1 },
      { field: "unknown", op: "is", value: "anything" },
    ])
      expect(collectionDraft("Unsupported", query([chip]))).toBeNull();
    expect(
      collectionDraft(
        "OR",
        new URLSearchParams({ filters: JSON.stringify({ mode: "or", chips: [] }) }),
      ),
    ).toBeNull();
    expect(collectionDraft("Text", new URLSearchParams({ q: "Seven" }))).toBeNull();
  });
  it("rejects conflicting media types and preserves legacy genre filters", () => {
    expect(
      collectionDraft(
        "Conflict",
        query([{ field: "type", op: "is", value: "show" }], { type: "movie" }),
      ),
    ).toBeNull();
    expect(
      collectionDraft("Legacy", new URLSearchParams({ genre: "Drama", decade: "1980" }))?.rules,
    ).toEqual([
      { field: "genre", op: "is", value: "Drama" },
      { field: "decade", op: "is", value: 1980 },
    ]);
  });
});
