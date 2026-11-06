import { describe, expect, it } from "vitest";
import { compileQuery, mergeAIQuery, readAIQuery, readQuery } from "./catalogQuery";

describe("catalogue query language", () => {
  it("preserves the whole selected insertion day independently of air date", () => {
    const exact = compileQuery({
      mode: "and",
      chips: [{ field: "etherAdded", op: "is", value: "2026-01-01" }],
    });
    expect(JSON.stringify(exact)).toContain('"etherAddedFrom":1767225600');
    expect(JSON.stringify(exact)).toContain('"etherAddedTo":1767311999');
    const before = compileQuery({
      mode: "and",
      chips: [{ field: "etherAdded", op: "max", value: "2026-01-01" }],
    });
    expect(JSON.stringify(before)).toContain('"etherAddedTo":1767311999');
  });
  it("compiles an exact date-added day separately from insertion time", () => {
    const query = compileQuery({
      mode: "and",
      chips: [{ field: "dateAdded", op: "is", value: "2026-01-01" }],
    });
    expect(JSON.stringify(query)).toContain('"dateAddedFrom":1767225600');
    expect(JSON.stringify(query)).toContain('"dateAddedTo":1767311999');
  });
  it("compiles nested groups and viewer context without applying metadata filters to state", () => {
    const query = compileQuery(
      {
        mode: "or",
        chips: [
          { field: "watched", op: "is", value: "unwatched" },
          { field: "rating", op: "min", value: "7", group: 1 },
          { field: "runtime", op: "max", value: "120", group: 1 },
        ],
      },
      "42",
    );
    expect(query.group!.operator).toBe("or");
    expect(query.group!.children).toEqual([
      { operator: "and", unwatched: true, userId: 42 },
      {
        operator: "and",
        children: [
          { operator: "and", filter: { minRating: 7 } },
          { operator: "and", filter: { maxRuntime: 7200000000000 } },
        ],
      },
    ]);
  });
  it("matches any ungrouped chip with OR", () => {
    const query = compileQuery({
      mode: "or",
      chips: [
        { field: "genre", op: "is", value: "Comedy" },
        { field: "genre", op: "is", value: "Drama" },
      ],
    });
    expect(query.group!.children).toHaveLength(2);
  });
  it("keeps conjunctive quality requirements on the same release", () => {
    const chips = [
      { field: "quality", op: "is", value: "4k" },
      { field: "quality", op: "is", value: "dv" },
    ];
    expect(compileQuery({ mode: "and", chips }).group!.children).toEqual([
      { operator: "and", filter: { qualities: ["4k", "dv"] } },
    ]);
    expect(compileQuery({ mode: "or", chips }).group!.children).toHaveLength(2);
  });
  it("rejects corrupt or unbounded URL state", () => {
    expect(readQuery("invalid")).toEqual({ chips: [], mode: "and" });
    expect(
      readQuery(
        JSON.stringify({
          chips: Array(33).fill({ field: "year", op: "is", value: "2000" }),
          mode: "and",
        }),
      ).chips,
    ).toEqual([]);
  });
});

describe("AI filter group transactions", () => {
  const incoming = readAIQuery({
    mode: "and",
    groupMode: "or",
    chips: [
      { field: "genre", op: "is", value: "Comedy", group: 1 },
      { field: "genre", op: "is", value: "Romance", group: 1 },
    ],
  });
  it("keeps an existing flat OR selection together when adding AI constraints", () => {
    const merged = mergeAIQuery(
      {
        mode: "or",
        chips: [
          { field: "country", op: "is", value: "CA" },
          { field: "country", op: "is", value: "US" },
        ],
      },
      incoming,
    );
    expect(merged.chips.map((chip) => chip.group)).toEqual([1, 1, 2, 2]);
    expect(compileQuery(merged).group!.children).toMatchObject([
      { operator: "or" },
      { operator: "or" },
    ]);
  });
  it("fails atomically if existing grouping or capacity cannot preserve the full draft", () => {
    expect(() =>
      mergeAIQuery(
        { mode: "or", chips: [{ field: "country", op: "is", value: "CA", group: 1 }] },
        incoming,
      ),
    ).toThrow();
    expect(() =>
      mergeAIQuery(
        { mode: "and", chips: Array(32).fill({ field: "type", op: "is", value: "movie" }) },
        incoming,
      ),
    ).toThrow();
    expect(() =>
      mergeAIQuery(
        {
          mode: "and",
          groupMode: "or",
          chips: [1, 2, 3].map((group) => ({ field: "genre", op: "is", value: "Comedy", group })),
        },
        incoming,
      ),
    ).toThrow();
  });
});
