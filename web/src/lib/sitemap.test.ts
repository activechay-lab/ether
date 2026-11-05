import { describe, expect, it } from "vitest";
import { destinations } from "./sitemap";
import { settingsIndex } from "./settings-index";
import { destinationsFrom, pagesFrom } from "./sitemap";
import { descriptors } from "../mock/extend/descriptors/console";
import type { PageMeta } from "./console-descriptors";
import type { Descriptor } from "./extend";

const pages = descriptors.filter((d) => d.kind === "page") as Descriptor<PageMeta>[];

describe("sitemap", () => {
  it("indexes Rules and upcoming content without the retired gate", () => {
    const hrefs = destinations().map((d) => d.href);
    expect(hrefs).toContain("/catalog/rules");
    expect(hrefs).not.toContain("/catalog/visibility");
    expect(settingsIndex().some((s) => s.title === "Wait for digital or physical release")).toBe(
      false,
    );
    expect(settingsIndex().find((s) => s.title === "Show upcoming movies")?.href).toBe(
      "/catalog/settings",
    );
  });

  it("gives every destination exactly one home", () => {
    const hrefs = destinationsFrom(pages).map((d) => d.href);
    expect(new Set(hrefs).size).toBe(hrefs.length);
    expect(pagesFrom(pages).map((p) => p.title)).toContain("Overview");
  });

  it("points Plex sections at the Plex tab, never at dead Apps tabs", () => {
    const hrefs = destinations().map((d) => d.href);
    expect(hrefs).not.toContain("/apps/remote");
    expect(hrefs).not.toContain("/apps/secure");
    expect(hrefs).not.toContain("/apps/plex/compatibility");
    expect(hrefs).toContain("/apps/plex/account");
    expect(hrefs).toContain("/apps/plex/remote");
    expect(hrefs).toContain("/apps/plex/downloads");
  });

  it("lands switchable integrations on their overview card and lists every tab", () => {
    const byTitle = new Map(
      destinations()
        .filter((d) => d.page === "Integrations")
        .map((d) => [d.title, d.href]),
    );
    for (const [title, card] of [
      ["RPDB", "rpdb"],
      ["PostersPlus", "postersplus"],
      ["Custom posters", "custom"],
      ["Trakt", "trakt"],
      ["Simkl", "simkl"],
      ["Language model", "llm"],
      ["OpenSubtitles", "opensubtitles"],
    ] as const) {
      expect(byTitle.get(title)).toBe(`/services/overview#${card}`);
    }
    const hrefs = destinations().map((d) => d.href);
    expect(hrefs).toContain("/services/tmdb");
    expect(hrefs).toContain("/services/ratings");
    expect(hrefs).toContain("/services/introdb");
    expect(hrefs).toContain("/maintenance/health");
    expect(hrefs).not.toContain("/notifications/protection");
  });
});

describe("settings index", () => {
  it("finds moved settings on their new pages", () => {
    const at = (title: string) => settingsIndex().filter((s) => s.title === title);
    expect(at("Log level")[0]).toMatchObject({ href: "/maintenance/logs" });
    expect(at("Streams per viewer")[0]).toMatchObject({ href: "/playback/transcoding" });
    expect(at("Allow downloads")[0]).toMatchObject({ href: "/apps/plex/downloads" });
    expect(at("Trailers and Cinema Trailers")[0]).toMatchObject({ href: "/playback/prerolls" });
    expect(at("Custom server URLs")[0]).toMatchObject({ href: "/apps/overview" });
    expect(at("Bitrate limit")[0]).toMatchObject({ href: "/quality/profiles" });
    expect(at("Watchlist monitoring")[0]).toMatchObject({ href: "/catalog/settings" });
    expect(at("Adapt Home to tonight's browsing")[0]).toMatchObject({ href: "/home" });
  });

  it("uses a stable More options disclosure key for hidden settings", () => {
    const hit = settingsIndex().find((s) => s.title === "Minimum popularity");
    expect(hit?.more).toBe("browsable");
    expect(settingsIndex().find((s) => s.title === "Port")?.more).toBeUndefined();
  });
});
