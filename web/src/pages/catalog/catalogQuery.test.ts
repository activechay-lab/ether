import { describe, expect, it } from "vitest";
import { compileQuery, parseNatural, readQuery } from "./catalogQuery";

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
  it("turns the owner example into inspectable predicates", () => {
    const chips = parseNatural("80s comedies over 7 with Tom Hanks under 2h");
    expect(chips).toEqual(
      expect.arrayContaining([
        { field: "decade", op: "is", value: "1980" },
        { field: "genre", op: "is", value: "Comedy" },
        { field: "rating", op: "min", value: "7" },
        { field: "cast", op: "is", value: "Tom Hanks" },
        { field: "runtime", op: "max", value: "120" },
      ]),
    );
    expect(chips).toHaveLength(5);
  });
  it("understands the unwatched playable 4K persona without a model", () => {
    const chips = parseNatural(
      "find unwatched 90s thrillers under 2 hours rated above 7 that are playable in 4K",
    );
    expect(chips).toHaveLength(7);
    expect(chips).toEqual(
      expect.arrayContaining([
        { field: "playable", op: "is", value: "yes" },
        { field: "quality", op: "is", value: "4k" },
      ]),
    );
    expect(chips.some((c) => c.field === "title")).toBe(false);
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
  it("preserves unrecognised phrases rather than silently discarding them", () => {
    expect(parseNatural("obscure moon film")).toContainEqual({
      field: "title",
      op: "is",
      value: "obscure moon",
    });
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
