import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PlexServers } from "../pages/settings/PlexServers";

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

describe("PlexServers full history backfill", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("offers only direction-matching backfill actions, previews counts in a confirm drawer, and shows progress and done status", async () => {
    const calls: string[] = [];
    let server = {
      id: 1,
      name: "Living room PMS",
      url: "http://192.168.1.10:32400",
      machineId: "pms-1",
      version: "1.43.4",
      owner: "alex",
      checkedAt: 1700000000,
      problem: "",
      viewers: [
        {
          accountId: 1,
          accountName: "alex",
          owner: true,
          userId: 10,
          direction: 2,
          interval: 10,
          canSend: true,
          lastImport: 1700000100,
          lastSend: 0,
          nextSync: 1700000700,
          problem: "",
          unmatched: 0,
          inBackfill: { running: false, processed: 0, total: 0, doneAt: 0 },
          outBackfill: { running: false, processed: 0, total: 0, doneAt: 0 },
        },
        {
          accountId: 2,
          accountName: "sam",
          owner: false,
          userId: 11,
          direction: 1,
          interval: 10,
          canSend: true,
          lastImport: 0,
          lastSend: 1700000200,
          nextSync: 1700000800,
          problem: "",
          unmatched: 0,
          inBackfill: { running: false, processed: 0, total: 0, doneAt: 0 },
          outBackfill: { running: false, processed: 0, total: 0, doneAt: 0 },
        },
      ],
    };

    mockFetch((path, init) => {
      const method = init?.method ?? "GET";
      calls.push(`${method} ${path}`);
      if (path === "/api/viewers") {
        return {
          viewers: [
            { id: 10, plexId: 100, username: "alex", title: "Alex", isOwner: true },
            { id: 11, plexId: 101, username: "sam", title: "Sam", isOwner: false },
          ],
        };
      }
      if (path === "/api/plex-servers" && method === "GET") {
        return [server];
      }
      if (path === "/api/plex-servers/1/accounts" && method === "GET") {
        return [
          { id: 1, name: "alex", owner: true, userId: 10 },
          { id: 2, name: "sam", owner: false, userId: 11 },
          { id: 3, name: "guest", owner: false, userId: 0 },
        ];
      }
      if (path === "/api/plex-servers/1/accounts/1/backfill" && method === "GET") {
        return { remoteViews: 342, remoteKnown: true, localWatched: 12, localInProgress: 1 };
      }
      if (path === "/api/plex-servers/1/accounts/2/backfill" && method === "GET") {
        return { remoteViews: 0, remoteKnown: true, localWatched: 18, localInProgress: 3 };
      }
      if (path === "/api/plex-servers/1/accounts/1/backfill-in" && method === "POST") {
        server = {
          ...server,
          viewers: server.viewers.map((v) =>
            v.accountId === 1
              ? { ...v, inBackfill: { running: true, processed: 100, total: 342, doneAt: 0 } }
              : v,
          ),
        };
        return server;
      }
      if (path === "/api/plex-servers/1/accounts/2/backfill-out" && method === "POST") {
        server = {
          ...server,
          viewers: server.viewers.map((v) =>
            v.accountId === 2
              ? {
                  ...v,
                  outBackfill: { running: false, processed: 21, total: 21, doneAt: 1700000900 },
                }
              : v,
          ),
        };
        return server;
      }
      return {};
    });

    render(
      <MemoryRouter>
        <PlexServers enabled={true} />
      </MemoryRouter>,
    );

    const bringInButtons = await screen.findAllByRole("button", {
      name: "Bring in full history from this server",
    });
    const sendOutButtons = screen.getAllByRole("button", {
      name: "Send Ether's full history to this server",
    });
    expect(bringInButtons).toHaveLength(1);
    expect(sendOutButtons).toHaveLength(1);

    const alexRow = bringInButtons[0]!.closest("div")!.parentElement!;
    expect(
      within(alexRow).queryByRole("button", { name: "Send Ether's full history to this server" }),
    ).toBeNull();
    const samRow = sendOutButtons[0]!.closest("div")!.parentElement!;
    expect(
      within(samRow).queryByRole("button", { name: "Bring in full history from this server" }),
    ).toBeNull();

    await userEvent.click(bringInButtons[0]!);
    expect(calls).not.toContain("POST /api/plex-servers/1/accounts/1/backfill-in");
    const importDialog = await screen.findByRole("dialog", {
      name: "Bring in full history from this server",
    });
    expect(
      await within(importDialog).findByText(
        "342 history entries on this Plex server for this account.",
      ),
    ).toBeInTheDocument();
    expect(
      within(importDialog).getByText(/Newer watch state in Ether is never overwritten/),
    ).toBeInTheDocument();

    await userEvent.click(within(importDialog).getByRole("button", { name: "Start import" }));
    await waitFor(() => expect(calls).toContain("POST /api/plex-servers/1/accounts/1/backfill-in"));
    expect(
      await screen.findByText("Full history from this server: 100 of 342 read…"),
    ).toBeInTheDocument();

    await userEvent.click(
      screen.getByRole("button", { name: "Send Ether's full history to this server" }),
    );
    expect(calls).not.toContain("POST /api/plex-servers/1/accounts/2/backfill-out");
    const sendDialog = await screen.findByRole("dialog", {
      name: "Send Ether's full history to this server",
    });
    expect(
      await within(sendDialog).findByText(
        "18 watched titles and 3 in-progress titles in Ether for this viewer.",
      ),
    ).toBeInTheDocument();
    expect(
      within(sendDialog).getByText(/Newer watch state on the Plex server is never overwritten/),
    ).toBeInTheDocument();

    await userEvent.click(within(sendDialog).getByRole("button", { name: "Start send" }));
    await waitFor(() =>
      expect(calls).toContain("POST /api/plex-servers/1/accounts/2/backfill-out"),
    );
    expect(await screen.findByText(/Full history to this server: done \(/)).toBeInTheDocument();
  });
});
