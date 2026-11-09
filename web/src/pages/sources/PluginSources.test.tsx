import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PluginSourceCards, usePluginSourceSummaries } from "./PluginSources";

const mockDescriptors = [
  {
    kind: "sourceKind" as const,
    id: "debrid",
    owner: "builtin",
    label: { source: "Debrid service" },
    order: 10,
    meta: { class: "debrid" },
  },
  {
    kind: "sourceKind" as const,
    id: "plugin:org.ether.fixture-source/stream",
    owner: "org.ether.fixture-source",
    label: { resolved: "Fixture Stream Source" },
    hint: { resolved: "Streams 4K and 1080p releases." },
    order: 100,
    meta: {
      class: "indexer",
      abilities: ["search", "movie-search", "tv-search"],
      limits: { searchesPerDay: 250, grabsPerDay: 50 },
    },
  },
  {
    kind: "sourceKind" as const,
    id: "plugin:org.ether.folders/stream",
    owner: "org.ether.folders",
    label: { resolved: "Local Folders" },
    hint: { resolved: "Granted local media folders." },
    order: 110,
    meta: {
      class: "direct",
      abilities: ["search", "movie-search"],
    },
  },
  {
    kind: "sourceKind" as const,
    id: "plugin:org.ether.debrid-extra/debrid",
    owner: "org.ether.debrid-extra",
    label: { resolved: "Extra Debrid Plugin" },
    order: 120,
    meta: {
      class: "debrid",
      abilities: ["movie-search"],
    },
  },
];

vi.mock("../../lib/extend", async () => {
  const actual = await vi.importActual<typeof import("../../lib/extend")>("../../lib/extend");
  return {
    ...actual,
    useRegistry: () => ({
      descriptors: mockDescriptors,
      version: 1,
      loading: false,
      error: null,
      refresh: vi.fn(),
    }),
  };
});

describe("PluginSources", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    fetchMock.mockReset();
    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      if (url.endsWith("/api/plugins") && (!init?.method || init.method === "GET")) {
        return new Response(
          JSON.stringify({
            plugins: [
              { id: "org.ether.fixture-source", enabled: true, state: "ready" },
              { id: "org.ether.folders", enabled: true, state: "ready" },
              {
                id: "org.ether.debrid-extra",
                enabled: true,
                state: "ready",
                health: "attention",
                healthReason: "Add an API key in Setup.",
              },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      if (url.endsWith("/api/plugins/org.ether.fixture-source/test") && init?.method === "POST") {
        return new Response(JSON.stringify({ ok: true, durationMs: 12 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify({}), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders indexer and direct plugin source cards, reorders them and runs plugin test", async () => {
    render(
      <MemoryRouter>
        <PluginSourceCards classes={["indexer", "direct"]} />
      </MemoryRouter>,
    );

    expect(await screen.findByText("Fixture Stream Source")).toBeTruthy();
    expect(screen.getByText("Local Folders")).toBeTruthy();
    expect(screen.queryByText("Extra Debrid Plugin")).toBeNull();
    expect(screen.queryByText("Debrid service")).toBeNull();

    // Reorder Local Folders up to position 1.
    const moveUpBtn = screen.getByRole("button", {
      name: "Move Local Folders up",
    });
    fireEvent.click(moveUpBtn);

    // Open Fixture Stream Source drawer and run Test.
    const editBtn = screen.getByRole("button", {
      name: "Edit Fixture Stream Source",
    });
    fireEvent.click(editBtn);

    const testBtn = await screen.findByRole("button", { name: /Test/i });
    fireEvent.click(testBtn);

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("/api/plugins/org.ether.fixture-source/test"),
        expect.objectContaining({ method: "POST" }),
      );
    });
  });

  it("filters debrid plugin sources on the Debrid tab and renders usage summary", async () => {
    render(
      <MemoryRouter>
        <PluginSourceCards classes={["debrid"]} />
        <PluginSummaries />
      </MemoryRouter>,
    );

    expect(await screen.findAllByText("Extra Debrid Plugin")).toHaveLength(2);
    expect(await screen.findByText("Needs attention")).toBeTruthy();
    expect(screen.getByText("Add an API key in Setup.")).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: "Drag to reorder Extra Debrid Plugin" }),
    ).toBeNull();
    expect(screen.getByText(/250 searches a day/)).toBeTruthy();
    expect(screen.getByText(/50 grabs a day/)).toBeTruthy();
  });
});

function PluginSummaries() {
  return (
    <ul>
      {usePluginSourceSummaries().map((p) => (
        <li key={p.id}>
          <span>{p.label}</span> <span>{p.summary}</span>
        </li>
      ))}
    </ul>
  );
}
