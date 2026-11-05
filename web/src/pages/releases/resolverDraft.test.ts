import { describe, expect, it } from "vitest";
import { mergeCustomFormats } from "./resolverDraft";

const format = (trash_id: string, extra: Record<string, unknown> = {}) => ({
  trash_id,
  name: trash_id,
  ...extra,
});

describe("custom format three-way merge", () => {
  it("keeps definitions another import added while the drawer was open", () => {
    const merged = mergeCustomFormats({
      original: [format("a")],
      edited: [format("a")],
      latest: [format("a"), format("concurrent")],
    });
    expect(merged.map((f) => f.trash_id)).toEqual(["a", "concurrent"]);
  });

  it("keeps a user addition and tags it for a media-scoped profile", () => {
    const merged = mergeCustomFormats({
      original: [],
      edited: [format("mine", { name: "Mine" })],
      latest: [format("server")],
      app: "radarr",
    });
    expect(merged).toEqual([
      { trash_id: "server", name: "server" },
      { trash_id: "mine", name: "Mine", app: "radarr" },
    ]);
  });

  it("deletes only the definition the user removed from the original", () => {
    const merged = mergeCustomFormats({
      original: [format("keep"), format("remove")],
      edited: [format("keep")],
      latest: [format("keep"), format("remove"), format("concurrent")],
    });
    expect(merged.map((f) => f.trash_id)).toEqual(["keep", "concurrent"]);
  });

  it("preserves an app tag the edited JSON left out", () => {
    const merged = mergeCustomFormats({
      original: [format("sonarr-only", { app: "sonarr" })],
      edited: [{ trash_id: "sonarr-only", name: "Renamed" }],
      latest: [format("sonarr-only", { app: "sonarr" })],
    });
    expect(merged).toEqual([{ trash_id: "sonarr-only", name: "Renamed", app: "sonarr" }]);
  });

  it("leaves a both-media profile's new definitions untagged", () => {
    const merged = mergeCustomFormats({
      original: [],
      edited: [format("mine")],
      latest: [],
      app: null,
    });
    expect(merged).toEqual([format("mine")]);
  });
});
