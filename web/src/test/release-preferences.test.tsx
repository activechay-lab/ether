import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { calls, renderApp } from "./harness";
import {
  releaseRuleConflict,
  type RuleFieldDescriptor,
} from "../pages/releases/ReleasePreferences";
import { descriptors as rankingDescriptors } from "../mock/extend/descriptors/ranking";

const ruleFields = rankingDescriptors.filter(
  (d) => d.kind === "ruleField",
) as unknown as RuleFieldDescriptor[];

const WAIT = { timeout: 4000 };

async function openProfile() {
  const { fake } = renderApp("/quality/profiles");
  fireEvent.click(await screen.findByRole("button", { name: "Edit 1080p" }, WAIT));
  const drawer = await screen.findByRole("dialog", { name: "1080p" }, WAIT);
  return { fake, drawer };
}

async function addRule(
  drawer: HTMLElement,
  term: string,
  field: string,
  action: "Only play" | "Prefer" | "Never play",
) {
  fireEvent.change(within(drawer).getByLabelText("Add rule"), { target: { value: term } });
  fireEvent.click(within(drawer).getAllByRole("button", { name: "What it describes" })[0]!);
  fireEvent.click(await screen.findByRole("option", { name: field }, WAIT));
  fireEvent.click(within(drawer).getAllByRole("button", { name: "Action" })[0]!);
  fireEvent.click(await screen.findByRole("option", { name: action }, WAIT));
  fireEvent.click(within(drawer).getByRole("button", { name: "Add rule" }));
}

describe("profile rules and ranking ladder", () => {
  it("adds Prefer rules into the What matters most ladder, reorders them, and saves", async () => {
    const { fake, drawer } = await openProfile();
    await addRule(drawer, "KORSUB", "Release name", "Prefer");
    await addRule(drawer, "SPARKS", "Release group", "Prefer");
    expect(within(drawer).getAllByDisplayValue("KORSUB")).toHaveLength(2);

    fireEvent.click(within(drawer).getAllByRole("button", { name: "Advanced" })[1]!);
    expect(within(drawer).getByText("Other conditions")).toBeInTheDocument();
    expect(within(drawer).queryByText("Fallbacks")).not.toBeInTheDocument();
    expect(within(drawer).getAllByDisplayValue("SPARKS")).toHaveLength(2);

    fireEvent.click(within(drawer).getAllByRole("button", { name: "Move up" })[1]!);
    const names = within(drawer)
      .getAllByLabelText("Rule name")
      .map((el) => (el as HTMLInputElement).value);
    expect(names).toEqual(["SPARKS", "KORSUB"]);

    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), WAIT);
    const saved = fake.state.config.quality.profiles!.find((p) => p.name === "1080p")!;
    expect(saved.releaseRules?.map((r) => r.name)).toEqual(["SPARKS", "KORSUB"]);
    expect(saved.ranking?.slice(0, 2)).toEqual([
      `rule:${saved.releaseRules![0]!.id}`,
      `rule:${saved.releaseRules![1]!.id}`,
    ]);
    expect(saved.releaseRules?.[0]?.conditions[0]).toMatchObject({
      field: "group",
      operator: "equals",
      value: "SPARKS",
    });

    fireEvent.click(await screen.findByRole("button", { name: "Edit 1080p" }, WAIT));
    const again = await screen.findByRole("dialog", { name: "1080p" }, WAIT);
    expect(within(again).getAllByDisplayValue("SPARKS").length).toBeGreaterThan(0);
    expect(within(again).getAllByDisplayValue("KORSUB").length).toBeGreaterThan(0);
  });

  it("moves a Prefer rule below Resolution in the ranking ladder", async () => {
    const { fake, drawer } = await openProfile();
    await addRule(drawer, "SPARKS", "Release group", "Prefer");
    for (let i = 0; i < 3; i++) {
      fireEvent.click(within(drawer).getByRole("button", { name: "Move down" }));
    }
    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), WAIT);
    const saved = fake.state.config.quality.profiles!.find((p) => p.name === "1080p")!;
    const ruleStep = `rule:${saved.releaseRules![0]!.id}`;
    expect(saved.ranking?.slice(0, 5)).toEqual([
      "language",
      "connection",
      "resolution",
      ruleStep,
      "releaseType",
    ]);
  });

  it("shows a conflict, previews filenames, and drops a stale result when the source changes", async () => {
    const { fake, drawer } = await openProfile();
    await addRule(drawer, "hevc", "Video codec", "Only play");
    await addRule(drawer, "h264", "Video codec", "Only play");
    expect(within(drawer).getByRole("alert")).toHaveTextContent("cannot match the same release");
    const pinsBefore = calls(fake, "POST", "/releases/pins").length;

    fireEvent.change(within(drawer).getByLabelText("Release names"), {
      target: { value: "Movie.2026.1080p.WEB-DL.H.264-HEVC" },
    });
    fireEvent.click(within(drawer).getByRole("button", { name: "Preview" }));
    expect(
      await within(drawer).findByText("Movie.2026.1080p.WEB-DL.H.264-HEVC", {}, WAIT),
    ).toBeInTheDocument();

    fireEvent.click(within(drawer).getByRole("button", { name: "Preview source" }));
    fireEvent.click(await screen.findByRole("option", { name: "Search a title" }, WAIT));
    expect(
      within(drawer).queryByText("Movie.2026.1080p.WEB-DL.H.264-HEVC"),
    ).not.toBeInTheDocument();
    expect(calls(fake, "POST", "/releases/pins")).toHaveLength(pinsBefore);
    expect(calls(fake, "POST", "/config")).toHaveLength(0);
  });

  it("imports a rules file onto the unsaved draft", async () => {
    const { drawer } = await openProfile();
    const input = within(drawer).getByLabelText("Import rules", { selector: "input" });
    const file = new File(
      [
        JSON.stringify([
          {
            id: "old",
            name: "Arbitrary tag",
            action: "avoid",
            conditions: [{ field: "name", operator: "contains", value: "KORSUB" }],
          },
        ]),
      ],
      "rules.json",
      { type: "application/json" },
    );
    await userEvent.upload(input, file);
    await waitFor(() => {
      const alert = within(drawer).queryByRole("alert");
      if (!within(drawer).queryByDisplayValue("Arbitrary tag")) {
        throw new Error(alert?.textContent || "import did not add the rule");
      }
    }, WAIT);
  });

  it("sets a movie default without changing the legacy or show defaults", async () => {
    const { fake } = renderApp("/quality/profiles");
    fireEvent.click(await screen.findByRole("button", { name: "Edit 4K" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "4K" }, WAIT);
    const defaults = within(drawer).getByRole("group", { name: "Default for" });
    // The profile applies to movies, so checking At home writes defaultMovie.
    fireEvent.click(within(defaults).getByRole("button", { name: "At home" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), WAIT);
    expect(fake.state.config.quality.defaultMovie).toBe("4K");
    expect(fake.state.config.quality.default).toBe("1080p");
    expect(fake.state.config.quality.defaultTv).toBeUndefined();
    expect(screen.getAllByText("Default at home").length).toBeGreaterThan(0);
  });

  it("merges a custom format added by a concurrent import", async () => {
    const { fake } = renderApp("/quality/profiles");
    fireEvent.click(await screen.findByRole("button", { name: "Edit 4K" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "4K" }, WAIT);
    fireEvent.click(within(drawer).getByRole("button", { name: "Edit scores" }));
    const defs = await within(drawer).findByLabelText("Custom format definitions", {}, WAIT);
    fireEvent.change(defs, {
      target: {
        value: JSON.stringify([{ trash_id: "mine", name: "Mine", specifications: [] }]),
      },
    });
    // Another import adds a definition while this drawer is open.
    fake.state.config.resolver.customFormats = [
      { trash_id: "concurrent", name: "Concurrent", specifications: [] },
    ];
    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/resolver")).toHaveLength(1));
    const body = calls(fake, "PUT", "/config/resolver")[0]!.body as {
      customFormats: { trash_id?: string; app?: string }[];
    };
    expect(body.customFormats.map((f) => f.trash_id)).toEqual(["concurrent", "mine"]);
    // The new definition is tagged for the profile's movie app.
    expect(body.customFormats[1]).toMatchObject({ app: "radarr" });
  });

  it("flags conflicting requirements before save", () => {
    expect(
      releaseRuleConflict(
        [
          {
            id: "a",
            name: "HEVC",
            action: "require",
            conditions: [{ field: "video", operator: "equals", value: "hevc" }],
          },
          {
            id: "b",
            name: "H.264",
            action: "require",
            conditions: [{ field: "video", operator: "equals", value: "h264" }],
          },
        ],
        ruleFields,
      ),
    ).toMatch(/cannot match the same release/);
  });

  it("adds original-language audio without a typed value", async () => {
    const { drawer } = await openProfile();
    await addRule(drawer, "Original audio", "Original-language audio", "Prefer");
    expect(within(drawer).getAllByDisplayValue("Original audio")).toHaveLength(1);
    expect(
      within(drawer).getAllByText(
        /Subtitles do not count, and a filename alone cannot make the comparison/,
      ).length,
    ).toBeGreaterThan(0);
    expect(within(drawer).getByText(/cannot be compared from a filename/)).toBeInTheDocument();
  });

  it("adds and removes a plugin ranking step and selects a plugin rule field", async () => {
    const { fake, drawer } = await openProfile();
    const addStepBtn = await within(drawer).findByRole("button", { name: "Add a step" }, WAIT);
    fireEvent.click(addStepBtn);
    expect(
      await within(drawer).findByText("From community.example.dub", {}, WAIT),
    ).toBeInTheDocument();

    fireEvent.click(within(drawer).getByRole("button", { name: "Remove step" }));
    expect(within(drawer).queryByText("From community.example.dub")).not.toBeInTheDocument();

    fireEvent.click(within(drawer).getByRole("button", { name: "Add a step" }));
    await addRule(drawer, "en", "Dubbed · From community.example.dub", "Prefer");
    expect(within(drawer).getAllByText("From community.example.dub").length).toBeGreaterThanOrEqual(
      2,
    );

    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), WAIT);
    const saved = fake.state.config.quality.profiles!.find((p) => p.name === "1080p")!;
    expect(saved.ranking).toContain("plugin:community.example.dub/dub");
    expect(saved.releaseRules?.[0]?.conditions[0]?.field).toBe(
      "plugin:community.example.dub/dubbed",
    );
  });
});

it("edits and saves a plugin ranking step's configuration", async () => {
  const descriptor = {
    kind: "rankingStep" as const,
    id: "plugin:test.example/dub",
    owner: "test.example",
    label: { source: "Dub preference" },
    meta: { editor: { kind: "toggle" }, canMove: true, defaultPosition: 0 },
  };
  rankingDescriptors.push(descriptor);
  try {
    const { resetRegistryCache } = await import("../lib/extend");
    resetRegistryCache();
    const { fake, drawer } = await openProfile();
    fireEvent.click(within(drawer).getByRole("button", { name: "Step to add" }));
    fireEvent.click(await screen.findByRole("option", { name: /Dub preference/ }, WAIT));
    fireEvent.click(within(drawer).getByRole("button", { name: "Add a step" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Configure Dub preference" }));
    fireEvent.click(within(drawer).getByRole("switch", { name: "Dub preference" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), WAIT);
    const saved = fake.state.config.quality.profiles!.find((p) => p.name === "1080p")!;
    expect(saved.rankingConfig?.[descriptor.id]).toEqual({ value: true });
  } finally {
    rankingDescriptors.splice(rankingDescriptors.indexOf(descriptor), 1);
    const { resetRegistryCache } = await import("../lib/extend");
    resetRegistryCache();
  }
});
