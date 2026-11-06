import { describe, it, expect } from "vitest";
import { createFakeServer } from "./server";
describe("catalogue mock sorting", () => {
  it("honours duration/year sorts and primary direction", async () => {
    const fake = createFakeServer();
    for (const [sort, field, desc] of [
      ["duration", "runtimeMinutes", "0"],
      ["year", "year", "1"],
    ] as const) {
      const { items } = await (
        await fake.fetch(`/ether/api/catalog/browse?sort=${sort}&desc=${desc}&limit=100`)
      ).json();
      const values: number[] = items.map((item: Record<string, number>) => item[field] || 0);
      expect(values).toEqual([...values].sort((a, b) => (desc === "1" ? b - a : a - b)));
    }
  });
  it("honours a descending secondary rating order for tied years", async () => {
    const fake = createFakeServer();
    const { items } = await (
      await fake.fetch(
        "/ether/api/catalog/browse?sort=year&desc=1&secondarySort=rating&secondaryDesc=1&limit=100",
      )
    ).json();
    for (let i = 1; i < items.length; i++)
      if (items[i].year === items[i - 1].year)
        expect(items[i].rating || 0).toBeLessThanOrEqual(items[i - 1].rating || 0);
  });
});
