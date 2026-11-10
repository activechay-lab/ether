import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { FakeServer } from "../mock/server";
import Me from "../pages/me/Me";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 4000 };

afterEach(() => {
  sessionStorage.clear();
  vi.restoreAllMocks();
});

function withTraktRoutes(
  enabled: boolean,
  route?: (method: string, path: string) => unknown,
): (fake: FakeServer) => void {
  return (fake: FakeServer) => {
    fake.state.config.trakt = {
      enabled,
      clientId: enabled ? "trakt-cid" : "",
      clientSecret: enabled ? "trakt-secret" : "",
    };
    if (!route) return;
    const base = fake.fetch;
    fake.fetch = async (input, init) => {
      const url = new URL(String(input), "http://localhost");
      const path = url.pathname.replace(/^\/ether\/api/, "") + url.search;
      const method = (init?.method ?? "GET").toUpperCase();
      const out = route(method, path);
      if (out !== undefined) {
        fake.state.calls.push({ method, path, body: null });
        return new Response(JSON.stringify(out), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return base(input, init);
    };
  };
}

describe("Viewer Trakt linking", () => {
  it("disables Connect and links to Integrations › Trakt when Trakt is not configured", async () => {
    renderApp("/viewers/4/limits");
    const section = await screen.findByRole("region", { name: "Trakt" }, WAIT);
    expect(within(section).getByRole("button", { name: "Connect" })).toBeDisabled();
    const link = within(section).getByRole("link", { name: "Integrations › Trakt" });
    expect(link).toHaveAttribute("href", "/services/trakt");
  });

  it("opens the Connect Trakt drawer, live-polls until connected, and disconnects", async () => {
    let polls = 0;
    let connected = false;
    const { fake } = renderApp(
      "/viewers/4/limits",
      {},
      withTraktRoutes(true, (method, path) => {
        if (method === "GET" && path === "/viewers/4/trakt") {
          return {
            configured: true,
            connected,
            username: connected ? "bobtrakt" : undefined,
            portalUrl: "https://ether.example/ether/me",
          };
        }
        if (method === "POST" && path === "/viewers/4/trakt/device") {
          return {
            configured: true,
            connected: false,
            status: "pending",
            portalUrl: "https://ether.example/ether/me",
            device: {
              userCode: "TRKT-9999",
              verificationUrl: "https://trakt.tv/activate",
              expiresAt: new Date(Date.now() + 600_000).toISOString(),
              interval: 1,
            },
          };
        }
        if (method === "POST" && path === "/viewers/4/trakt/poll") {
          polls += 1;
          if (polls < 2) {
            return {
              configured: true,
              connected: false,
              status: "pending",
              portalUrl: "https://ether.example/ether/me",
              device: {
                userCode: "TRKT-9999",
                verificationUrl: "https://trakt.tv/activate",
                expiresAt: new Date(Date.now() + 600_000).toISOString(),
                interval: 1,
              },
            };
          }
          connected = true;
          return {
            configured: true,
            connected: true,
            username: "bobtrakt",
            status: "authorized",
            portalUrl: "https://ether.example/ether/me",
          };
        }
        if (method === "POST" && path === "/viewers/4/trakt/disconnect") {
          connected = false;
          return {
            configured: true,
            connected: false,
            portalUrl: "https://ether.example/ether/me",
          };
        }
        return undefined;
      }),
    );

    const section = await screen.findByRole("region", { name: "Trakt" }, WAIT);
    expect(within(section).getByText("Not connected")).toBeInTheDocument();
    fireEvent.click(within(section).getByRole("button", { name: "Connect" }));

    const dialog = await screen.findByRole("dialog", { name: "Connect Trakt" }, WAIT);
    expect(await within(dialog).findByText("TRKT-9999", {}, WAIT)).toBeInTheDocument();
    const activateLink = within(dialog).getByRole("link", { name: "https://trakt.tv/activate" });
    expect(activateLink).toHaveAttribute("href", "https://trakt.tv/activate");
    expect(within(dialog).getByText(/https:\/\/ether\.example\/ether\/me/)).toBeInTheDocument();

    await waitFor(
      () => expect(within(section).getByText("Connected as bobtrakt")).toBeInTheDocument(),
      WAIT,
    );
    expect(calls(fake, "POST", "/viewers/4/trakt/poll").length).toBeGreaterThanOrEqual(2);

    fireEvent.click(within(section).getByRole("button", { name: "Disconnect" }));
    await waitFor(
      () => expect(within(section).getByText("Not connected")).toBeInTheDocument(),
      WAIT,
    );
  });

  it("surfaces expired and denied codes in the drawer and lets the owner request a new code", async () => {
    let outcome: "expired" | "denied" = "expired";
    renderApp(
      "/viewers/4/limits",
      {},
      withTraktRoutes(true, (method, path) => {
        if (method === "GET" && path === "/viewers/4/trakt") {
          return { configured: true, connected: false };
        }
        if (method === "POST" && path === "/viewers/4/trakt/device") {
          return {
            configured: true,
            connected: false,
            status: "pending",
            device: {
              userCode: outcome === "expired" ? "EXPD-0001" : "DENY-0002",
              verificationUrl: "https://trakt.tv/activate",
              expiresAt: new Date(Date.now() + 600_000).toISOString(),
              interval: 1,
            },
          };
        }
        if (method === "POST" && path === "/viewers/4/trakt/poll") {
          return { configured: true, connected: false, status: outcome };
        }
        return undefined;
      }),
    );

    const section = await screen.findByRole("region", { name: "Trakt" }, WAIT);
    fireEvent.click(within(section).getByRole("button", { name: "Connect" }));
    const dialog = await screen.findByRole("dialog", { name: "Connect Trakt" }, WAIT);
    expect(
      await within(dialog).findByText(
        "This code expired. Generate a new code to continue.",
        {},
        WAIT,
      ),
    ).toBeInTheDocument();

    outcome = "denied";
    fireEvent.click(within(dialog).getByRole("button", { name: "Get a new code" }));
    expect(
      await within(dialog).findByText(
        "Approval was declined on Trakt. Generate a new code to try again.",
        {},
        WAIT,
      ),
    ).toBeInTheDocument();
  });

  it("links Integrations › Trakt directly to the viewer Limits tab and mentions /ether/me", async () => {
    renderApp("/services/trakt", {}, withTraktRoutes(true));
    const link = await screen.findByRole(
      "link",
      {
        name: "Connect Trakt under each viewer's Limits tab, or let viewers connect at /ether/me.",
      },
      WAIT,
    );
    expect(link).toHaveAttribute("href", "/viewers/1/limits");
  });

  it("lets a signed-in viewer connect and disconnect Trakt on /ether/me", async () => {
    sessionStorage.setItem("ether.viewerPortal", "viewer-grant");
    let connected = false;
    const fetchMock = vi.fn(async (url: string, init?: RequestInit) => {
      const method = (init?.method ?? "GET").toUpperCase();
      if (url.endsWith("/bootstrap")) {
        return new Response(JSON.stringify({ signInMethods: ["local", "plex"], version: "1" }));
      }
      if (url.endsWith("/me/apps")) return new Response(JSON.stringify({ apps: [] }));
      if (url.endsWith("/me/identities")) return new Response(JSON.stringify({ identities: [] }));
      if (url.endsWith("/me/devices")) return new Response(JSON.stringify({ devices: [] }));
      if (url.endsWith("/me/trakt") && method === "GET") {
        return new Response(
          JSON.stringify({
            configured: true,
            connected,
            username: connected ? "alicetrakt" : undefined,
          }),
        );
      }
      if (url.endsWith("/me/trakt/device") && method === "POST") {
        expect((init?.headers as Record<string, string>)["X-Ether-Request"]).toBe("1");
        expect((init?.headers as Record<string, string>).Authorization).toBe("Bearer viewer-grant");
        return new Response(
          JSON.stringify({
            configured: true,
            connected: false,
            status: "pending",
            device: {
              userCode: "SELF-4321",
              verificationUrl: "https://trakt.tv/activate",
              expiresAt: new Date(Date.now() + 600_000).toISOString(),
              interval: 1,
            },
          }),
        );
      }
      if (url.endsWith("/me/trakt/poll") && method === "POST") {
        connected = true;
        return new Response(
          JSON.stringify({
            configured: true,
            connected: true,
            username: "alicetrakt",
            status: "authorized",
          }),
        );
      }
      if (url.endsWith("/me/trakt/disconnect") && method === "POST") {
        connected = false;
        return new Response(JSON.stringify({ configured: true, connected: false }));
      }
      return new Response(JSON.stringify({ id: 1, username: "alice", title: "Alice" }));
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<Me />);
    const section = await screen.findByRole("region", { name: "Trakt" }, WAIT);
    expect(within(section).getByText("Not connected")).toBeInTheDocument();
    fireEvent.click(within(section).getByRole("button", { name: "Connect" }));

    expect(await within(section).findByText("SELF-4321", {}, WAIT)).toBeInTheDocument();
    expect(
      within(section).getByRole("link", { name: "https://trakt.tv/activate" }),
    ).toHaveAttribute("href", "https://trakt.tv/activate");

    await waitFor(
      () => expect(within(section).getByText("Connected as alicetrakt")).toBeInTheDocument(),
      WAIT,
    );

    fireEvent.click(within(section).getByRole("button", { name: "Disconnect" }));
    await waitFor(
      () => expect(within(section).getByText("Not connected")).toBeInTheDocument(),
      WAIT,
    );
  });
});
