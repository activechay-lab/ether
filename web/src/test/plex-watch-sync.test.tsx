import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PlexWatchSync } from "../pages/viewers/sections/PlexWatchSync";

function mockFetch(handler: (path: string, init?: RequestInit) => unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const raw = typeof input === "string" ? input : input.toString();
      const path = raw.replace(/^\/ether/, "");
      const body = handler(path, init);
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
}

function renderPlexSync(viewerId = 7, plexId = 77) {
  return render(
    <MemoryRouter>
      <PlexWatchSync viewerId={viewerId} plexId={plexId} />
    </MemoryRouter>,
  );
}

/** Open an AppKit select by its accessible name and pick an option. */
async function choose(label: string, option: string) {
  fireEvent.click(screen.getByRole("button", { name: label }));
  fireEvent.click(await screen.findByRole("option", { name: option }));
}

const base = {
  connected: true,
  username: "viewer",
  direction: 3,
  importInterval: 10,
  lastImport: 0,
  lastFullImport: 0,
  lastSend: 0,
  nextImport: 0,
  problem: "",
  needsReconnect: false,
  pending: 0,
  failed: 0,
  restricted: false,
  accountMatch: true,
};

describe("Plex watch sync", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("connects with a Plex approval code", async () => {
    const calls: string[] = [];
    let status: Record<string, unknown> = { ...base, connected: false, direction: 0 };
    mockFetch((path, init) => {
      if (path.endsWith("/plex-servers")) return [];
      calls.push(`${init?.method ?? "GET"} ${path}`);
      if (path.endsWith("/pin"))
        status = { ...status, pinCode: "CODE", pinUrl: "https://app.plex.tv/auth#?code=CODE" };
      if (path.endsWith("/pin/finish"))
        status = { ...status, connected: true, pinCode: undefined, pinUrl: undefined };
      return status;
    });
    renderPlexSync();
    await userEvent.click(await screen.findByRole("button", { name: "Connect" }));
    expect(screen.queryByRole("button", { name: "Sync" })).not.toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "Open Plex approval" })).toHaveAttribute(
      "href",
      "https://app.plex.tv/auth#?code=CODE",
    );
    await userEvent.click(screen.getByRole("button", { name: "Check connection" }));
    await waitFor(() => expect(calls).toContain("POST /api/viewers/7/plex-watch/pin/finish"));
    expect(await screen.findByRole("button", { name: "Sync" })).toBeInTheDocument();
  });

  it("chooses a direction and offers only the matching actions", async () => {
    const calls: string[] = [];
    let status: Record<string, unknown> = { ...base, direction: 0 };
    mockFetch((path, init) => {
      if (path.endsWith("/plex-servers")) return [];
      calls.push(`${init?.method ?? "GET"} ${path} ${init?.body ?? ""}`.trim());
      if (init?.method === "PUT") {
        const b = JSON.parse(String(init.body)) as { direction: number; interval: number };
        status = { ...status, direction: b.direction, importInterval: b.interval };
      }
      return status;
    });
    renderPlexSync();
    await screen.findByRole("button", { name: "Sync" });
    expect(screen.queryByRole("button", { name: "Check Plex now" })).not.toBeInTheDocument();
    await choose("Sync", "Plex → Ether");
    await waitFor(() =>
      expect(calls).toContain(`PUT /api/viewers/7/plex-watch {"direction":2,"interval":10}`),
    );
    expect(await screen.findByRole("button", { name: "Check Plex now" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send watched titles to Plex" })).toBeNull();
    await choose("Check Plex every", "30 minutes");
    await waitFor(() =>
      expect(calls).toContain(`PUT /api/viewers/7/plex-watch {"direction":2,"interval":30}`),
    );
    await userEvent.click(screen.getByRole("button", { name: "Check Plex now" }));
    await waitFor(() => expect(calls).toContain("POST /api/viewers/7/plex-watch/import"));
    await choose("Sync", "Ether → Plex");
    await userEvent.click(
      await screen.findByRole("button", { name: "Send watched titles to Plex" }),
    );
    await waitFor(() => expect(calls).toContain("POST /api/viewers/7/plex-watch/export"));
    expect(screen.queryByRole("button", { name: "Check Plex every" })).toBeNull();
  });

  it("shows problems with a way out", async () => {
    const calls: string[] = [];
    let status: Record<string, unknown> = { ...base, problem: "rate_limited", failed: 2 };
    mockFetch((path, init) => {
      if (path.endsWith("/plex-servers")) return [];
      calls.push(`${init?.method ?? "GET"} ${path}`);
      if (path.endsWith("/retry")) status = { ...status, failed: 0, problem: "" };
      return status;
    });
    renderPlexSync();
    expect(await screen.findByText(/Plex asked Ether to slow down/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Retry 2 failed changes" }));
    await waitFor(() => expect(calls).toContain("POST /api/viewers/7/plex-watch/retry"));
    expect(await screen.findByText("Up to date")).toBeInTheDocument();
  });

  it("asks to reconnect when Plex refused the token", async () => {
    const calls: string[] = [];
    mockFetch((path, init) => {
      if (path.endsWith("/plex-servers")) return [];
      calls.push(`${init?.method ?? "GET"} ${path}`);
      return { ...base, connected: false, needsReconnect: true, problem: "token" };
    });
    renderPlexSync();
    expect(await screen.findByText("Reconnect required")).toBeInTheDocument();
    expect(screen.getByText(/Reconnect the account/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Sync" })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Reconnect" }));
    await waitFor(() => expect(calls).toContain("POST /api/viewers/7/plex-watch/pin"));
    expect(screen.getByRole("button", { name: "Disconnect" })).toBeEnabled();
  });

  it("renders Plex account sync and Plex Media Server Sync as distinct blocks", async () => {
    let servers: unknown[] = [
      {
        serverId: 1,
        serverName: "Living room PMS",
        accountId: 1,
        accountName: "alex",
        owner: true,
        direction: 3,
        interval: 10,
        lastImport: 1700000100,
        lastSend: 1700000200,
        nextSync: 1700000600,
        problem: "unreachable",
        unmatched: 1,
      },
    ];
    mockFetch((path) => (path.endsWith("/plex-servers") ? servers : { ...base, lastSend: 0 }));

    const view = renderPlexSync(7, 77);
    expect(await screen.findByRole("heading", { name: "Plex account sync" })).toBeInTheDocument();
    expect(
      screen.getByText(
        "Keeps this viewer's watched and in-progress status in step with their Plex account.",
      ),
    ).toBeInTheDocument();
    expect(
      await screen.findByRole("heading", { name: "Plex Media Server Sync" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Keeps watch state in step with your own Plex Media Servers, per viewer."),
    ).toBeInTheDocument();
    expect(screen.getByText("Living room PMS")).toBeInTheDocument();
    expect(screen.getByText("alex · Both · every 10 minutes")).toBeInTheDocument();
    expect(screen.getByText(/Last read .* · Last sent .* · Next .*/)).toBeInTheDocument();
    expect(
      screen.getByText(
        "Ether can't reach this server. Check that it's running and the address is right.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Manage in Integrations › Plex Media Server Sync" }),
    ).toHaveAttribute("href", "/services/plexservers");

    servers = [];
    view.rerender(
      <MemoryRouter>
        <PlexWatchSync viewerId={8} plexId={0} />
      </MemoryRouter>,
    );
    expect(
      await screen.findByText(/This viewer isn't mapped to any of your Plex servers\./),
    ).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Plex account sync" })).toBeNull();
    expect(
      screen.getByRole("link", { name: "Manage in Integrations › Plex Media Server Sync" }),
    ).toHaveAttribute("href", "/services/plexservers");
  });
});
