import { describe, expect, it } from "vitest";
import { buildCatalog } from "./data";
import { matchesCatalogFilter } from "./catalogFilter";

describe("mock catalogue filtering contract", () => {
  const titles = buildCatalog();
  const title = titles[0]!;
  it("applies numeric ranges and nested AND/OR to actual results", () => {
    expect(matchesCatalogFilter(title, { yearFrom: (title.year || 0) + 1 })).toBe(false);
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
  it("narrows genre and rating without mutating source data", () => {
    const result = titles.filter((t) =>
      matchesCatalogFilter(t, { genres: ["Thriller"], minRating: 7 }),
    );
    expect(result.length).toBeGreaterThan(0);
    expect(result.length).toBeLessThan(titles.length);
    expect(result.every((t) => t.genres?.includes("Thriller") && (t.rating || 0) >= 7)).toBe(true);
  });
});
