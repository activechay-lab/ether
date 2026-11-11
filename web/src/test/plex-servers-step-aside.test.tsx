import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PlexServers } from "../pages/settings/PlexServers";

describe("PlexServers step-aside toggle", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("renders default-off toggle and persists stepAside per server", async () => {
    let stepAside = false;
    const requests: Array<{ method: string; path: string; body?: unknown }> = [];

    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const raw = typeof input === "string" ? input : input.toString();
        const path = raw.replace(/^\/ether/, "");
        const method = init?.method ?? "GET";
        const parsedBody =
          typeof init?.body === "string" && init.body ? JSON.parse(init.body) : undefined;
        requests.push({ method, path, body: parsedBody });

        if (path === "/api/plex-servers" && method === "GET") {
          return new Response(
            JSON.stringify([
              {
                id: 1,
                name: "Living room PMS",
                url: "http://192.168.1.10:32400",
                machineId: "m1",
                version: "1.43.4",
                owner: "alex",
                stepAside,
                checkedAt: 1700000000,
                problem: "",
                viewers: [],
              },
            ]),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        if (path === "/api/plex-servers/1/accounts" && method === "GET") {
          return new Response(JSON.stringify([{ id: 1, name: "alex", owner: true, userId: 7 }]), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        }
        if (path === "/api/viewers" && method === "GET") {
          return new Response(JSON.stringify([]), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          });
        }
        if (path === "/api/plex-servers/1/step-aside" && method === "PUT") {
          stepAside = Boolean((parsedBody as { stepAside?: boolean })?.stepAside);
          return new Response(
            JSON.stringify({
              id: 1,
              name: "Living room PMS",
              url: "http://192.168.1.10:32400",
              machineId: "m1",
              version: "1.43.4",
              owner: "alex",
              stepAside,
              checkedAt: 1700000000,
              problem: "",
              viewers: [],
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        return new Response("{}", {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }),
    );

    render(
      <MemoryRouter>
        <PlexServers enabled={true} />
      </MemoryRouter>,
    );

    const toggle = (await screen.findByRole("switch", {
      name: "Step aside for titles on this server",
    })) as HTMLInputElement;
    expect(toggle.checked).toBe(false);
    expect(
      screen.getByText(
        "Hide movies, complete shows, and episodes this server already has from Plex apps.",
      ),
    ).toBeTruthy();

    fireEvent.click(toggle);
    await waitFor(() => {
      expect(toggle.checked).toBe(true);
    });
    expect(requests).toContainEqual({
      method: "PUT",
      path: "/api/plex-servers/1/step-aside",
      body: { stepAside: true },
    });

    fireEvent.click(toggle);
    await waitFor(() => {
      expect(toggle.checked).toBe(false);
    });
    expect(requests).toContainEqual({
      method: "PUT",
      path: "/api/plex-servers/1/step-aside",
      body: { stepAside: false },
    });
  });
});
