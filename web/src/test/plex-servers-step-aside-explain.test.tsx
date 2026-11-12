import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PlexServers } from "../pages/settings/PlexServers";

describe("PlexServers step-aside explain", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("shows the scan status and explains why a title still shows", async () => {
    const server = {
      id: 1,
      name: "Living room PMS",
      url: "http://192.168.1.10:32400",
      machineId: "m1",
      version: "1.43.4",
      owner: "alex",
      stepAside: true,
      stepAsideStatus: {
        state: "complete",
        movies: 2,
        showsFull: 1,
        showsPartial: 0,
        episodes: 156,
        unmatched: 0,
        lastSynced: 1700000000,
        lastScan: 1700000000,
        problem: "",
      },
      checkedAt: 1700000000,
      problem: "",
      viewers: [],
    };
    const explain = {
      key: 2001399,
      kind: "show",
      title: "Once Upon a Time",
      hidden: false,
      cause: "missing_episodes",
      servers: ["Living room PMS"],
      airedRegular: 4,
      heldEpisodes: 3,
      missing: ["S01E05"],
      missingMore: 0,
      scanState: "complete",
    };

    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const raw = typeof input === "string" ? input : input.toString();
        const path = raw.replace(/^\/ether/, "");
        const method = init?.method ?? "GET";
        const json = (body: unknown) =>
          new Response(JSON.stringify(body), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        if (path === "/api/plex-servers" && method === "GET") return json([server]);
        if (path === "/api/plex-servers/1/accounts" && method === "GET")
          return json([{ id: 1, name: "alex", owner: true, userId: 7 }]);
        if (path === "/api/viewers" && method === "GET") return json([]);
        if (path.startsWith("/api/catalog/search")) {
          return json({ items: [{ key: "2001399", type: "show", title: "Once Upon a Time" }] });
        }
        if (path.startsWith("/api/plex-servers/step-aside/explain")) return json(explain);
        return json({});
      }),
    );

    render(
      <MemoryRouter>
        <PlexServers enabled={true} />
      </MemoryRouter>,
    );

    // The status line names the counts and the state.
    expect(await screen.findByText(/156 episodes stepped aside/)).toBeTruthy();
    expect(screen.getByText(/Up to date/)).toBeTruthy();

    // Open the explain drawer and search for the title.
    fireEvent.click(screen.getByRole("button", { name: "Why is a title still showing?" }));
    const search = await screen.findByRole("textbox", { name: "Search a movie or show" });
    fireEvent.change(search, { target: { value: "once" } });

    const result = await screen.findByRole("button", { name: /Once Upon a Time/ });
    fireEvent.click(result);

    await waitFor(() => {
      expect(screen.getByText(/missing 1 aired episode/)).toBeTruthy();
    });
    expect(screen.getByText(/S01E05/)).toBeTruthy();
  });
});
