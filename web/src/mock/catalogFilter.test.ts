import { describe, expect, it } from "vitest";
import { buildCatalog } from "./data";
import { matchesCatalogFilter } from "./catalogFilter";

describe("mock catalogue filtering contract", () => {
  it("finds a real demo title for the full owner slicing task", () => {
    const result = buildCatalog().filter((title) =>
      matchesCatalogFilter(title, {
        yearFrom: 1990,
        yearTo: 1999,
        genres: ["Thriller"],
        minRating: 7,
        maxRuntime: 120 * 60e9,
        playable: "yes",
        qualities: ["4k"],
        group: { unwatched: true, userId: 1 },
      }),
    );
    expect(result.some((title) => title.title === "Midnight Verdict")).toBe(true);
    expect(
      result.every((title) => title.evidence?.verified && title.evidence.qualities.includes("4k")),
    ).toBe(true);
  });
  const titles = buildCatalog();
  const title = titles[0]!;
  it("applies numeric ranges and nested AND/OR to actual results", () => {
    expect(matchesCatalogFilter(title, { yearFrom: (title.year || 0) + 1 })).toBe(false);
    expect(
      matchesCatalogFilter(title, { etherAddedTo: Date.parse(title.addedAt) / 1000 - 1 }),
    ).toBe(false);
    expect(
      matchesCatalogFilter(title, {
        group: {
          operator: "or",
          children: [{ filter: { yearFrom: 2200 } }, { filter: { query: title.title } }],
        },
      }),
    ).toBe(true);
    expect(
      matchesCatalogFilter(title, {
        group: {
          operator: "and",
          children: [{ filter: { yearFrom: 2200 } }, { filter: { query: title.title } }],
        },
      }),
    ).toBe(false);
  });
  it("handles watchlist and rated viewer leaves without treating them as groups", () => {
    const rated = titles.filter((title) =>
      matchesCatalogFilter(title, {
        group: { stateField: "lastRatedAt", stateOperator: ">", stateValue: 0, userId: 1 },
      }),
    );
    const saved = titles.filter((title) =>
      matchesCatalogFilter(title, { group: { onWatchlist: true, userId: 1 } }),
    );
    expect(rated.length).toBeGreaterThan(0);
    expect(saved.length).toBeGreaterThan(0);
    expect(saved.length).toBeLessThan(titles.length);
  });
  it("narrows genre and rating without mutating source data", () => {
    const result = titles.filter((t) =>
      matchesCatalogFilter(t, { genres: ["Thriller"], minRating: 7 }),
    );
    expect(result.length).toBeGreaterThan(0);
    expect(result.length).toBeLessThan(titles.length);
    expect(result.every((t) => t.genres?.includes("Thriller") && (t.rating || 0) >= 7)).toBe(true);
  });
});
