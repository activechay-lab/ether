import { describe, expect, it } from "vitest";
import { compileQuery, parseNatural, readQuery } from "./catalogQuery";

describe("catalogue query language", () => {
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
