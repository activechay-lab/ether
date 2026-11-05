import { describe, it, expect } from "vitest";
import { createFakeServer } from "./server";
describe("catalogue mock watchlist contract", () => {
  it("persists bulk item mutations per viewer and reloads bounded membership", async () => {
    const server = createFakeServer();
    const keys = server.catalog.slice(0, 3).map((t) => t.key);
    for (const key of keys)
      await server.fetch(`/ether/api/discovery/users/1/catalog/${key}`, { method: "PUT" });
    const membership = await (
      await server.fetch("/ether/api/discovery/users/1/membership", {
        method: "POST",
        body: JSON.stringify({ keys }),
      })
    ).json();
    expect(membership).toEqual(Object.fromEntries(keys.map((key) => [key, true])));
    await server.fetch(`/ether/api/discovery/users/1/catalog/${keys[0]}`, { method: "DELETE" });
    expect(
      await (
        await server.fetch("/ether/api/discovery/users/1/membership", {
          method: "POST",
          body: JSON.stringify({ keys: [keys[0]] }),
        })
      ).json(),
    ).toEqual({ [keys[0]!]: false });
    const filter = { group: { operator: "and", onWatchlist: true, userId: 1 } };
    const response = await (
      await server.fetch(
        `/ether/api/catalog/browse?filter=${encodeURIComponent(JSON.stringify(filter))}&limit=100`,
      )
    ).json();
    expect(response.items.some((t: { key: string }) => t.key === keys[0])).toBe(false);
    expect(response.items.some((t: { key: string }) => t.key === keys[1])).toBe(true);
  });
});
