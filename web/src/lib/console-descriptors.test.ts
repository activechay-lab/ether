import { describe, expect, it } from "vitest";
import { pageRoute, pluginNavGroup, type PageMeta } from "./console-descriptors";
import { destinationsFrom } from "./sitemap";
import { descriptors } from "../mock/extend/descriptors/console";
import type { Descriptor } from "./extend";

export const fixturePluginPage: Descriptor<PageMeta> = {
  kind: "page",
  id: "plugin:dev.example/wrapped",
  owner: "dev.example",
  label: { plugin: "dev.example", key: "wrapped", resolved: "Ether Wrapped" },
  icon: "https://example.org/wrapped.svg",
  group: "server",
  order: 80,
  meta: { navGroup: "server", keywords: { resolved: "year in review" } },
};

describe("console descriptors", () => {
  it("routes a plugin page to /p/<plugin>/<page>", () => {
    expect(pageRoute(fixturePluginPage)).toBe("/p/dev.example/wrapped");
    // A plugin page descriptor whose meta.route is the internal /ether/view/...
    // loader route still maps to its console /p/<plugin>/<page> route.
    expect(
      pageRoute({
        ...fixturePluginPage,
        meta: { route: "/ether/view/wrapped", group: "server" },
      }),
    ).toBe("/p/dev.example/wrapped");
    expect(pageRoute({ ...fixturePluginPage, meta: { route: "/server" } })).toBe(
      "/p/dev.example/wrapped",
    );
  });

  it("lists a plugin page as a search destination", () => {
    const pages = descriptors.filter((d) => d.kind === "page") as Descriptor<PageMeta>[];
    const all = destinationsFrom([...pages, fixturePluginPage]);
    expect(all.find((d) => d.href === "/p/dev.example/wrapped")).toMatchObject({
      title: "Ether Wrapped",
      isPage: true,
    });
  });
});

describe("pluginNavGroup", () => {
  const page = (meta: Record<string, unknown>, group?: string) =>
    ({ ...fixturePluginPage, group, meta }) as unknown as Descriptor<PageMeta>;
  it("never places a plugin page in the top group above Overview", () => {
    expect(pluginNavGroup(page({ group: "" }, "My plugin"))).toBe("server");
    expect(pluginNavGroup(page({}))).toBeUndefined();
  });
  it("keeps a group the manifest declares", () => {
    expect(pluginNavGroup(page({ group: "library" }))).toBe("library");
  });
});
