import { describe, expect, it } from "vitest";
import {
  describeSource,
  isSourceComplete,
  newSourceFromDescriptor,
  sourceDescriptor,
  type SourceDescriptor,
} from "../lib/rules";
import { descriptors } from "../mock/extend/descriptors/collections";

const kinds = descriptors.filter((d) => d.kind === "collectionSource") as SourceDescriptor[];
const byId = (id: string) => {
  const d = sourceDescriptor(kinds, id);
  if (!d) throw new Error(`missing descriptor ${id}`);
  return d;
};

describe("collection source kinds from the registry", () => {
  it("serves every built-in kind", () => {
    expect(kinds.map((d) => d.id).sort()).toEqual(
      [
        "anilistList",
        "catalog",
        "friendsWatchlist",
        "letterboxdList",
        "mdblist",
        "stevenlu",
        "titles",
        "tmdbCollection",
        "tmdbCompany",
        "tmdbDiscover",
        "tmdbKeyword",
        "tmdbList",
        "tmdbNetwork",
        "tmdbPerson",
        "tmdbUserList",
        "traktList",
        "watchProvider",
      ].sort(),
    );
  });

  it("describes a streaming service with the default region", () => {
    expect(describeSource({ kind: "watchProvider", id: 8, label: "Netflix" }, kinds)).toBe(
      "Netflix (default region)",
    );
  });

  it("starts an AniList source empty and needs a URL", () => {
    const s = newSourceFromDescriptor(byId("anilistList"), "show");
    expect(s).toEqual({ kind: "anilistList", url: "" });
    expect(isSourceComplete(s)).toBe(false);
    expect(
      isSourceComplete({ kind: "anilistList", url: "https://anilist.co/user/kai/animelist" }),
    ).toBe(true);
    expect(byId("anilistList").label.source).toBe("AniList list");
  });

  it("defaults StevenLu to the plain popular list and names variants", () => {
    const s = newSourceFromDescriptor(byId("stevenlu"), "movie");
    expect(s).toEqual({ kind: "stevenlu", list: "movies" });
    expect(isSourceComplete(s)).toBe(true);
    expect(byId("stevenlu").label.source).toBe("StevenLu popular movies");
    expect(describeSource({ kind: "stevenlu", list: "movies-rottentomatoes-min80" }, kinds)).toBe(
      "StevenLu popular movies: Rotten Tomatoes 80+",
    );
  });

  it("seeds a TMDB chart for the collection media type", () => {
    expect(newSourceFromDescriptor(byId("tmdbList"), "show")).toEqual({
      kind: "tmdbList",
      list: "trending_week",
      mediaType: "show",
    });
    expect(newSourceFromDescriptor(byId("watchProvider"), "movie", "GB")).toEqual({
      kind: "watchProvider",
      id: 0,
      region: "GB",
    });
  });
});
