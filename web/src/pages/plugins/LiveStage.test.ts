import { expect, it } from "vitest";
import { momentsForTool } from "./LiveStage";

it("turns a kinds replay into a library moment with the dry-run", async () => {
  const moments = await momentsForTool(
    "plugin_replay",
    {},
    {
      kinds: {
        kinds: [
          {
            key: "recordings",
            libraryName: "Field Recordings",
            levels: ["collection", "album", "track"],
            liveSearch: true,
            sample: [
              { id: "coast", level: "collection", title: "Coastal" },
              { id: "harbour-2", level: "track", title: "Fog Horn", playable: true },
            ],
            projections: [{ protocol: "plex", summary: "Shows as a TV Shows library" }],
            dryRun: {
              title: "Fog Horn",
              sources: [{ pattern: "range", expanded: "https://media.example/tracks/2.flac" }],
            },
            issues: [{ message: "browse cursor repeats" }],
          },
        ],
      },
    },
  );
  expect(moments[0]).toMatchObject({
    kind: "library",
    title: "Field Recordings",
    levels: "collection → album → track",
    liveSearch: true,
    items: [
      { key: "coast", depth: 0 },
      { key: "harbour-2", depth: 2, playable: true },
    ],
    dryRun: {
      title: "Fog Horn",
      sources: [{ pattern: "range", detail: "https://media.example/tracks/2.flac" }],
    },
    issues: ["browse cursor repeats"],
  });
});

it("shows actionable failed-check details instead of marking every step successful", async () => {
  const moments = await momentsForTool(
    "studio_project_save",
    {},
    {
      compile: { ok: true },
      preview: { ok: false, error: "route did not return a ViewSpec document" },
      installCheck: "unknown contribution capability",
    },
  );
  expect(moments[0]).toMatchObject({
    kind: "checks",
    failed: [false, true, true],
    issues: ["route did not return a ViewSpec document", "unknown contribution capability"],
  });
});

it("does not claim preview or installation checks ran after compilation failed", async () => {
  const moments = await momentsForTool(
    "studio_project_save",
    {},
    {
      compile: {
        ok: false,
        diagnostics: [{ file: "src/plugin.ts", line: 4, text: "Unexpected token" }],
      },
    },
  );
  expect(moments[0]).toMatchObject({
    kind: "checks",
    items: ["Has compile errors"],
    failed: [true],
    issues: ["src/plugin.ts:4 Unexpected token"],
  });
});
