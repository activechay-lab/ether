import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { HomeLayout } from "../lib/contract";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 4000 };

type PreviewBody = {
  viewerId: number | null;
  layout?: HomeLayout;
  device: string;
  at: string;
  section: string;
};

function lastPreview(fake: Parameters<typeof calls>[0]): PreviewBody {
  return calls(fake, "POST", "/layout/preview").at(-1)!.body as PreviewBody;
}
function savedLayout(fake: Parameters<typeof calls>[0]): HomeLayout {
  return (calls(fake, "PUT", "/layout").at(-1)!.body as { layout: HomeLayout }).layout;
}
async function closeEditor() {
  const dialog = screen.getByRole("dialog");
  fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
}
function openLayers() {
  fireEvent.click(screen.getByRole("button", { name: "Editing layer" }));
  return screen.getByRole("menu", { name: "Editing layer" });
}
async function layersClosed() {
  await waitFor(() => expect(screen.queryByRole("menu", { name: "Editing layer" })).toBeNull());
}
async function pickLayer(name: RegExp) {
  fireEvent.click(within(openLayers()).getByRole("menuitemradio", { name }));
  await layersClosed();
}
async function setStatus(title: string, value: "Always" | "Eligible" | "Off") {
  fireEvent.click(screen.getByRole("tab", { name: "Rows" }));
  fireEvent.click(screen.getByRole("button", { name: `Constraints for ${title}` }));
  const editor = await screen.findByRole("dialog", { name: title });
  fireEvent.click(
    within(within(editor).getByRole("radiogroup", { name: `Status of ${title}` })).getByRole(
      "radio",
      { name: value },
    ),
  );
  await closeEditor();
}

describe("Home", () => {
  it("previews the typical viewer's Home with each row's provenance", async () => {
    const { fake } = renderApp("/home");
    const cw = await screen.findByRole("region", { name: "Slot 1: Continue Watching" }, WAIT);
    expect(within(cw).getByText(/Anchored for everyone/)).toBeInTheDocument();
    expect(within(cw).getByText(/global anchor at slot 1/)).toBeInTheDocument();
    const top = screen.getByRole("region", { name: /Slot \d+: Top 10 on Living Room Ether Today/ });
    expect(within(top).getByText("Personalized")).toBeInTheDocument();
    expect(within(top).getByText(/Chosen by personalization/)).toBeInTheDocument();
    expect(within(top).getAllByRole("listitem").length).toBe(10);
    const body = lastPreview(fake);
    expect(body.viewerId).toBeNull();
    expect(body.layout).toBeUndefined();
    expect(body.device).toBe("tv");
    expect(body.at).toMatch(/^\d{4}-\d{2}-\d{2}T/);
    // The layer picker starts on Global; Kids carry an override and sort first.
    expect(screen.getByRole("button", { name: "Editing layer" })).toHaveTextContent("Everyone");
    const menu = openLayers();
    expect(within(menu).getByRole("menuitemradio", { name: /Everyone/ })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    const kids = within(menu).getByRole("menuitemradio", { name: /The Kids/ });
    expect(kids).toHaveTextContent("2 overrides");
    expect(within(menu).getAllByRole("menuitemradio")[1]).toBe(kids);
  });

  it("edits Global: the server previews the draft and reports the impact across viewers", async () => {
    const { fake } = renderApp("/home");
    await screen.findByRole("region", { name: /Top 10 on Living Room Ether Today/ }, WAIT);
    await setStatus("Top 10 Today", "Off");

    // Previewed server-side from the unsaved draft.
    const hidden = await screen.findByRole(
      "listitem",
      { name: "Not shown: Top 10 on Living Room Ether Today" },
      WAIT,
    );
    expect(within(hidden).getByText("Off in global layer")).toBeInTheDocument();
    expect(lastPreview(fake).layout?.global[0]?.rows).toContainEqual({
      row: "top-10-today",
      status: "off",
    });
    expect(screen.getByText("Previewing unsaved changes")).toBeInTheDocument();
    expect(
      within(screen.getByRole("listitem", { name: "Row Top 10 Today" })).getByText(
        "Affects 4 viewers",
      ),
    ).toBeInTheDocument();

    const impact = await screen.findByRole("status", { name: "Impact across viewers" });
    await waitFor(
      () =>
        expect(impact).toHaveTextContent(
          /Affects 4 viewers · 1 has overrides · \d+ Homes? changes? in this context/,
        ),
      WAIT,
    );
    expect(calls(fake, "POST", "/layout/impact").at(-1)!.body).toMatchObject({
      device: "tv",
      section: "home",
    });

    fireEvent.click(screen.getByRole("button", { name: /Save layout/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/layout")).toHaveLength(1), WAIT);
    const saved = savedLayout(fake);
    expect(saved.global[0]?.rows).toContainEqual({ row: "top-10-today", status: "off" });
    expect(saved.global[0]?.rows).toContainEqual({
      row: "continue-watching",
      status: "always",
      slot: 1,
    });
    expect(saved.viewers).toEqual([
      {
        userId: 3,
        pages: [
          {
            section: "home",
            maxRows: 8,
            rows: [{ row: "short-on-time", status: "always", maxSlot: 3 }],
          },
        ],
      },
    ]);
    await waitFor(() => expect(screen.queryByText("Unsaved changes")).toBeNull());
  });

  it("overrides a Global setting for one viewer and resets it to global", async () => {
    const { fake } = renderApp("/home");
    await screen.findByRole("region", { name: "Slot 1: Continue Watching" }, WAIT);
    await pickLayer(/Sam Rivera/);
    await screen.findByRole("region", { name: /Hidden Gems for You/ }, WAIT);
    await waitFor(() => expect(lastPreview(fake).viewerId).toBe(2));

    await setStatus("Hidden Gems for You", "Off");
    const hidden = await screen.findByRole(
      "listitem",
      { name: "Not shown: Hidden Gems for You" },
      WAIT,
    );
    expect(within(hidden).getByText("Off in override layer")).toBeInTheDocument();
    expect(within(hidden).getByText("Override for Sam Rivera")).toBeInTheDocument();
    expect(lastPreview(fake).layout?.viewers).toContainEqual({
      userId: 2,
      pages: [{ section: "home", rows: [{ row: "hidden-gems", status: "off" }] }],
    });
    expect(
      within(screen.getByRole("listitem", { name: "Row Hidden Gems for You" })).getByText(
        "Override",
      ),
    ).toBeInTheDocument();
    expect(
      within(openLayers()).getByRole("menuitemradio", { name: /Sam Rivera/ }),
    ).toHaveTextContent("1 override");
    fireEvent.click(screen.getByRole("button", { name: "Editing layer" }));
    await layersClosed();
    fireEvent.click(screen.getByRole("tab", { name: /^Overrides/ }));
    expect(screen.getByRole("region", { name: "Overrides for Sam Rivera" })).toHaveTextContent(
      "hidden-gems",
    );

    // Reset to global: the row comes back, the override is gone.
    fireEvent.click(screen.getByRole("tab", { name: "Rows" }));
    fireEvent.click(screen.getByRole("button", { name: "Constraints for Hidden Gems for You" }));
    fireEvent.click(screen.getByRole("button", { name: "Reset to Everyone: Hidden Gems for You" }));
    await closeEditor();
    await waitFor(
      () =>
        expect(
          screen.queryByRole("listitem", { name: "Not shown: Hidden Gems for You" }),
        ).toBeNull(),
      WAIT,
    );
    expect(screen.queryByText("Unsaved changes")).toBeNull();

    // An override that sticks: always show it for Sam, anchored to slot 2.
    await setStatus("Hidden Gems for You", "Always");
    fireEvent.click(screen.getByRole("button", { name: "Constraints for Hidden Gems for You" }));
    fireEvent.change(screen.getByLabelText("Anchor slot for Hidden Gems for You"), {
      target: { value: "2" },
    });
    await closeEditor();
    await waitFor(() => {
      const gems = screen.getByRole("region", { name: "Slot 2: Hidden Gems for You" });
      expect(within(gems).getByText(/override anchor at slot 2/)).toBeInTheDocument();
    }, WAIT);
    fireEvent.click(screen.getByRole("button", { name: /Save layout/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/layout")).toHaveLength(1), WAIT);
    expect(savedLayout(fake).viewers).toContainEqual({
      userId: 2,
      pages: [{ section: "home", rows: [{ row: "hidden-gems", status: "always", slot: 2 }] }],
    });
  });

  it("edits a collection row's schedule on the collection, not in the layout", async () => {
    const { fake } = renderApp("/home");
    await screen.findByRole("region", { name: "Slot 1: Continue Watching" }, WAIT);
    fireEvent.click(screen.getByRole("tab", { name: "Rows" }));
    fireEvent.click(screen.getByRole("button", { name: "Constraints for Weeknight wind-down" }));
    const editor = await screen.findByRole("dialog", { name: "Weeknight wind-down" });
    expect(editor).toHaveTextContent("The collection's schedule, also set in Collections.");
    const link = within(editor).getByRole("link", { name: "Collections" });
    expect(link.getAttribute("href")).toMatch(/^\/collections\/c\d+\?section=audience$/);
    const id = link.getAttribute("href")!.match(/collections\/(c\d+)/)![1]!;
    const days = within(editor).getByRole("group", { name: "Days for Weeknight wind-down" });
    const chips = within(days).getAllByRole("button");
    expect(chips.filter((c) => c.getAttribute("aria-pressed") === "true")).toHaveLength(4);
    fireEvent.click(chips[6]!); // Saturday
    await closeEditor();
    await waitFor(
      () =>
        expect(lastPreview(fake).layout?.collectionSchedules?.[id]?.days).toEqual([1, 2, 3, 4, 6]),
      WAIT,
    );
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Save layout/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/layout")).toHaveLength(1), WAIT);
    const saved = savedLayout(fake);
    // Only the edited collection is sent, and the layout keeps no schedule for it.
    expect(saved.collectionSchedules).toEqual({ [id]: { days: [1, 2, 3, 4, 6], fromHour: 18 } });
    expect(
      saved.global.flatMap((p) => p.rows).filter((r) => r.row === `collection:${id}` && r.when),
    ).toEqual([]);
    await waitFor(() => expect(screen.queryByText("Unsaved changes")).toBeNull());
    // The saved document reads the schedule back from the collection.
    fireEvent.click(screen.getByRole("button", { name: "Constraints for Weeknight wind-down" }));
    const again = await screen.findByRole("dialog", { name: "Weeknight wind-down" });
    const pressed = within(
      within(again).getByRole("group", { name: "Days for Weeknight wind-down" }),
    )
      .getAllByRole("button")
      .filter((c) => c.getAttribute("aria-pressed") === "true");
    expect(pressed).toHaveLength(5);
  });

  it("anchors a row from the preview and limits the page length", async () => {
    const { fake } = renderApp("/home");
    const row = await screen.findByRole("region", { name: /Slot \d+: New Releases/ }, WAIT);
    const slot = row.getAttribute("aria-label")!.match(/^Slot (\d+)/)![1];
    fireEvent.click(
      within(row).getByRole("button", { name: `Anchor New Releases to slot ${slot}` }),
    );
    await waitFor(
      () =>
        expect(
          screen.getByRole("region", { name: `Slot ${slot}: New Releases` }),
        ).toHaveTextContent("Anchored for everyone"),
      WAIT,
    );
    expect(lastPreview(fake).layout?.global[0]?.rows).toContainEqual({
      row: "new-releases",
      status: "always",
      slot: Number(slot),
    });
    // Page structure and length are edited in place on the Layout tab, not in a drawer.
    expect(
      screen.queryByRole("button", { name: /Edit anchored slots|Edit page length/ }),
    ).toBeNull();
    expect(
      within(screen.getByRole("region", { name: "Anchored slots" })).getAllByRole("button", {
        name: /Move anchored row/,
      }),
    ).toHaveLength(2);
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.change(screen.getByLabelText("Home rows"), { target: { value: "3" } });
    const cut = await screen.findByRole("region", { name: "Not on this page" }, WAIT);
    await waitFor(
      () => expect(cut).toHaveTextContent("Page length, frequency cap or position constraints"),
      WAIT,
    );
    expect(lastPreview(fake).layout?.global[0]?.maxRows).toBe(3);
  });

  it("tunes a page with dials, keeps Home's picks off it, and shows what the model learned", async () => {
    const { fake } = renderApp("/home");
    await screen.findByRole("region", { name: "Slot 1: Continue Watching" }, WAIT);
    // Switch to the TV page: its catalogue has series rows, not Home-only ones.
    fireEvent.click(screen.getByRole("button", { name: "Page" }));
    fireEvent.click(screen.getByRole("menuitemradio", { name: "TV" }));
    await waitFor(() => expect(screen.queryByRole("menu", { name: "Page" })).toBeNull());
    fireEvent.click(screen.getByRole("tab", { name: "Rows" }));
    const series = screen.getByRole("group", { name: "Series" });
    expect(
      within(series).getByRole("listitem", { name: "Row Finish What You Started" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("listitem", { name: "Row Top 10 for You" })).toBeNull();
    expect(screen.queryByRole("listitem", { name: "Row Acclaimed & Unseen" })).toBeNull();

    fireEvent.click(screen.getByRole("tab", { name: "Tuning" }));
    fireEvent.change(screen.getByRole("slider", { name: "Discovery" }), {
      target: { value: "80" },
    });
    fireEvent.click(screen.getByRole("switch", { name: "Don't repeat what Home shows" }));
    const learning = await screen.findByRole("region", { name: "How it learns" }, WAIT);
    expect(within(learning).getByText("Active")).toBeInTheDocument();
    expect(within(learning).getByText("+47%")).toBeInTheDocument();
    expect(within(learning).getByText("Taste fit")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Save layout/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/layout")).toHaveLength(1), WAIT);
    const shows = savedLayout(fake).global.find((p) => p.section === "shows");
    expect(shows).toMatchObject({ dedupeHome: false, tuning: { discovery: 0.8 } });
  });

  it("adds a custom row pattern from a drawer", async () => {
    renderApp("/home");
    expect(await screen.findByRole("heading", { name: "Home" }, WAIT)).toBeInTheDocument();
    await screen.findByRole("region", { name: "Slot 1: Continue Watching" }, WAIT);
    fireEvent.click(screen.getByRole("tab", { name: "Rows" }));
    fireEvent.click(screen.getByRole("button", { name: "Add custom pattern" }));
    const drawer = await screen.findByRole("dialog", { name: "Custom row pattern" });
    fireEvent.change(within(drawer).getByLabelText("Custom row pattern"), {
      target: { value: "because.*" },
    });
    fireEvent.click(within(drawer).getByRole("button", { name: "Add" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "Custom row pattern" })).toBeNull(),
    );
    expect(await screen.findByRole("listitem", { name: "Row because.*" })).toBeInTheDocument();
    expect(await screen.findByText("Unsaved changes")).toBeInTheDocument();
  });

  it("discards unsaved edits", async () => {
    const { fake } = renderApp("/home");
    await screen.findByRole("region", { name: /Top 10 on Living Room Ether Today/ }, WAIT);
    await setStatus("New & Noteworthy", "Off");
    expect(await screen.findByText("Unsaved changes")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Discard/ }));
    await waitFor(() => expect(screen.queryByText("Unsaved changes")).toBeNull());
    expect(calls(fake, "PUT", "/layout")).toHaveLength(0);
  });

  it("shows a viewer's saved Home read-only on their profile", async () => {
    const { fake } = renderApp("/viewers/2/home");
    const cw = await screen.findByRole("region", { name: "Slot 1: Continue Watching" }, WAIT);
    expect(within(cw).getByText("Anchored for everyone")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Turn off/ })).toBeNull();
    expect(calls(fake, "GET", "/viewers/2/home")).not.toHaveLength(0);
  });

  it("explains absent Always rows inline and never drops Always rows with titles under page caps", async () => {
    renderApp("/home");
    await screen.findByRole("region", { name: "Slot 1: Continue Watching" }, WAIT);

    // Setting a personal row to Always on Everyone explains why Typical viewer hides it,
    // both on the RowPool card and in the preview's Not on this page list.
    await setStatus("Top 10 for You", "Always");
    const top10Card = screen.getByRole("listitem", { name: "Row Top 10 for You" });
    await waitFor(
      () =>
        expect(top10Card).toHaveTextContent(
          "Hidden for Typical viewer: needs watch history (spot-check a viewer to preview)",
        ),
      WAIT,
    );
    const hiddenTop10 = await screen.findByRole(
      "listitem",
      { name: "Not shown: Top 10 for You" },
      WAIT,
    );
    expect(hiddenTop10).toHaveTextContent(
      "Hidden for Typical viewer: needs watch history (spot-check a viewer to preview)",
    );
    expect(within(hiddenTop10).getByText("Always")).toBeInTheDocument();
    expect(within(hiddenTop10).queryByRole("button", { name: /Always show/ })).toBeNull();

    // Setting a disabled collection to Always explains why it is absent.
    await setStatus("Family Movie Night", "Always");
    const familyCard = screen.getByRole("listitem", { name: "Row Family Movie Night" });
    await waitFor(
      () =>
        expect(familyCard).toHaveTextContent(
          "Hidden for Typical viewer: collection is disabled in Collections",
        ),
      WAIT,
    );

    // Spot-checking a warm viewer shows Top 10 for You, admits cold-start New & Noteworthy
    // when Always, and keeps every Always row with titles even when maxRows is 1.
    await setStatus("New & Noteworthy", "Always");
    fireEvent.click(screen.getByRole("tab", { name: "Layout" }));
    fireEvent.change(screen.getByLabelText("Home rows"), { target: { value: "1" } });

    await pickLayer(/Sam Rivera/);
    await screen.findByRole("region", { name: /Slot \d+: Top 10 for You/ }, WAIT);
    await screen.findByRole("region", { name: /Slot \d+: New Releases/ }, WAIT);
    expect(screen.getByRole("region", { name: "Slot 1: Continue Watching" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Rows" }));
    expect(screen.getByRole("listitem", { name: "Row Top 10 for You" })).not.toHaveTextContent(
      /Hidden for/,
    );
    expect(screen.getByRole("listitem", { name: "Row Family Movie Night" })).toHaveTextContent(
      "Hidden for Sam Rivera: collection is disabled in Collections",
    );
  });
});
