import { fireEvent, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { buildNavigation } from "../App";
import { descriptors } from "../mock/extend/descriptors/console";
import { fixturePluginPage } from "../lib/console-descriptors.test";
import { resetRegistryCache, type Descriptor } from "../lib/extend";
import { renderApp, settleUi } from "./harness";

describe("registry navigation", () => {
  it("uses the served page route for its compiled component", async () => {
    resetRegistryCache();
    renderApp("/nodes", {}, (fake) => {
      const original = fake.fetch;
      fake.fetch = async (...args) => {
        const response = await original(...args);
        if (!String(args[0]).includes("/extend/registry")) return response;
        const body = await response.json();
        for (const descriptor of body.descriptors) {
          if (descriptor.kind === "page" && descriptor.id === "cluster") {
            descriptor.meta.route = "/nodes";
          }
        }
        return new Response(JSON.stringify(body), {
          headers: { "content-type": "application/json" },
        });
      };
    });
    await settleUi(() =>
      expect(screen.getByRole("heading", { name: "Cluster" })).toBeInTheDocument(),
    );
    resetRegistryCache();
  });

  it("builds the sidebar in the same order as before", () => {
    const groups = buildNavigation(descriptors);
    expect(groups.map((g) => g.label)).toEqual(["", "Library", "Streaming", "Server"]);
    expect(groups[0]!.items.map((i) => i.href)).toEqual(["/", "/activity", "/viewers"]);
    expect(groups[0]!.items.map((i) => i.label)).toEqual(["Overview", "Activity", "Viewers"]);
    expect(groups[1]!.items.map((i) => i.href)).toEqual([
      "/libraries",
      "/collections",
      "/home",
      "/livetv",
      "/calendar",
      "/metadata",
    ]);
    expect(groups[2]!.items.map((i) => i.href)).toEqual([
      "/sources",
      "/playback",
      "/quality",
      "/cache",
    ]);
    expect(groups[3]!.items.map((i) => i.href)).toEqual([
      "/server",
      "/apps",
      "/services",
      "/plugins",
      "/notifications",
      "/cluster",
      "/maintenance",
    ]);
    // Mobile tab bar membership is unchanged.
    const mobile = groups.flatMap((g) => g.items).filter((i) => i.mobile);
    expect(mobile.map((i) => i.href)).toEqual(["/", "/activity", "/viewers", "/collections"]);
  });

  it("keeps plugin pages and plugin nav items out of the sidebar", () => {
    const hostNav: Descriptor = {
      kind: "navItem",
      id: "plugin:community.example.wrapped/page-wrapped",
      owner: "community.example.wrapped",
      label: { resolved: "Ether Wrapped", plugin: "community.example.wrapped" },
      group: "Ether Wrapped",
      meta: { route: "/plugins/community.example.wrapped/wrapped", pageKey: "wrapped" },
    };
    const groups = buildNavigation([...descriptors, fixturePluginPage, hostNav]);
    const hrefs = groups.flatMap((g) => g.items.map((i) => i.href));
    expect(hrefs.filter((h) => h.startsWith("/p/"))).toEqual([]);
    expect(groups[0]!.items[0]!.label).toBe("Overview");
  });

  it("keeps plugin pages out of the sidebar but finds them with ⌘K", async () => {
    renderApp("/");
    await settleUi(() => {
      expect(screen.getAllByRole("link", { name: "Integrations" }).length).toBeGreaterThan(0);
      expect(screen.getAllByRole("link", { name: "Plugins" }).length).toBeGreaterThan(0);
    });
    expect(screen.queryByRole("link", { name: "Ether Wrapped" })).toBeNull();
    const searchInput = screen.getByPlaceholderText("Search or jump to…");
    fireEvent.focus(searchInput);
    fireEvent.change(searchInput, { target: { value: "Ether Wrapped" } });
    await settleUi(() => {
      expect(screen.getByText("Go to")).toBeInTheDocument();
    });
  });

  it("keeps a plugin page route instead of redirecting home", async () => {
    renderApp("/p/dev.example/wrapped");
    await settleUi(() => {
      expect(screen.getAllByRole("link", { name: "Plugins" }).length).toBeGreaterThan(0);
      expect(screen.getByRole("heading", { name: "wrapped" })).toBeInTheDocument();
    });
    const main = document.querySelector("main");
    expect(main?.textContent ?? "").not.toContain("Now playing");
  });
});
