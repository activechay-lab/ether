import { describe, it, expect } from "vitest";
import { catalogValueLabel } from "./catalogValueLabel";
import { chipFilter } from "./catalogQuery";
describe("catalogue enum display labels", () => {
  it("shows friendly translated labels while retaining canonical predicates", () => {
    expect(catalogValueLabel("type", "movie")).toBe("Movie");
    expect(catalogValueLabel("type", "show")).toBe("TV show");
    expect(catalogValueLabel("watched", "unwatched")).toBe("Unwatched");
    expect(catalogValueLabel("progress", "true")).toBe("Yes");
    expect(catalogValueLabel("rated", "false")).toBe("No");
    expect(catalogValueLabel("status", "Canceled")).toBe("Cancelled");
    expect(catalogValueLabel("status", "Returning Series")).toBe("Returning series");
    expect(chipFilter({ field: "type", op: "is", value: "movie" }, "")).toEqual({ type: "movie" });
    expect(chipFilter({ field: "status", op: "is", value: "Canceled" }, "")).toEqual({
      statuses: ["Canceled"],
    });
  });
  it("keeps open text values unchanged", () => {
    expect(catalogValueLabel("title", "true")).toBe("true");
    expect(catalogValueLabel("genre", "Drama")).toBe("Drama");
  });
});
