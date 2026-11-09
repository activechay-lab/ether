import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { Workspace } from "../../components/workspace";
import type { AvailableRow, HomeLayout } from "../../lib/contract";
import { EMPTY_LAYOUT, setPolicy, setStatuses, type LayerKey } from "../../lib/layout";
import { RowPool } from "./RowPool";

vi.mock("../../lib/extend", async (load) => {
  const actual = await load<typeof import("../../lib/extend")>();
  const id = "plugin:fixture/picks";
  return {
    ...actual,
    useRegistry: (kind: string) => ({
      descriptors:
        kind === "rowSource"
          ? [
              {
                id,
                kind,
                owner: "fixture",
                label: { resolved: "Picks" },
                meta: { hubId: id, collectionSource: id, sections: ["home"] },
              },
            ]
          : [
              {
                id,
                kind,
                owner: "fixture",
                label: { resolved: "Picks" },
                meta: { editor: "plugin", defaults: { params: { genre: "drama" } } },
              },
            ],
      loading: false,
      error: null,
      reload: () => {},
    }),
  };
});
vi.mock("../../lib/api", async (load) => {
  const actual = await load<typeof import("../../lib/api")>();
  return { ...actual, api: { ...actual.api, rowInsights: async () => [] } };
});
function Destination() {
  const location = useLocation();
  return <output>{JSON.stringify(location.state)}</output>;
}
describe("plugin Home row sources", () => {
  it("starts an enabled collection with the plugin defaults and Home target", () => {
    render(
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route
            path="/"
            element={
              <RowPool
                draft={{ global: [], viewers: [] }}
                onChange={() => {}}
                layer="global"
                section="home"
                viewers={[]}
                available={[]}
                focusRow={null}
                onFocusRow={() => {}}
              />
            }
          />
          <Route path="/collections/new" element={<Destination />} />
        </Routes>
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Create collection" }));
    const state = JSON.parse(screen.getByText(/plugin:fixture/).textContent!);
    expect(state.spec).toMatchObject({
      enabled: true,
      name: "Picks",
      targets: { homeRow: true },
      sources: [{ kind: "plugin:fixture/picks", params: { genre: "drama" } }],
    });
  });
});

function Harness({
  initial,
  available,
  onDraft,
  layer = "global",
}: {
  initial: HomeLayout;
  available: AvailableRow[];
  onDraft?: (d: HomeLayout) => void;
  layer?: LayerKey;
}) {
  const [draft, setDraft] = useState(initial);
  const [focus, setFocus] = useState<string | null>(null);
  return (
    <MemoryRouter>
      <RowPool
        draft={draft}
        onChange={(d) => {
          setDraft(d);
          onDraft?.(d);
        }}
        layer={layer}
        section="home"
        viewers={[]}
        available={available}
        focusRow={focus}
        onFocusRow={setFocus}
      />
    </MemoryRouter>
  );
}

const picks: AvailableRow[] = [
  { hubId: "trending", title: "Trending", kind: "trending", description: "", group: "picks" },
  { hubId: "top-10", title: "Top 10", kind: "top-10", description: "", group: "picks" },
  { hubId: "fresh", title: "Fresh", kind: "fresh", description: "", group: "picks" },
];
const collections: AvailableRow[] = Array.from({ length: 20 }, (_, i) => ({
  hubId: `collection:c${i}`,
  title: `Collection ${i}`,
  kind: `collection:c${i}`,
  description: "",
  group: "collections",
}));
const rowsOf = (d: HomeLayout) => d.global.find((p) => p.section === "home")?.rows ?? [];

describe("bulk row editing", () => {
  it("sets the status of every selected row at once", () => {
    let last: HomeLayout | undefined;
    render(<Harness initial={EMPTY_LAYOUT} available={picks} onDraft={(d) => (last = d)} />);
    fireEvent.click(screen.getByRole("checkbox", { name: "Select Trending" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "Select Fresh" }));
    fireEvent.click(screen.getByRole("button", { name: "Set the selected rows to Off" }));
    expect(rowsOf(last!)).toEqual([
      { row: "trending", status: "off" },
      { row: "fresh", status: "off" },
    ]);
    expect(screen.queryByRole("region", { name: "Selected rows" })).toBeNull();
  });

  it("floats in the Workspace bottom overlay and clears via Done or Escape", () => {
    render(
      <MemoryRouter>
        <Workspace
          title="Home"
          asideLabel="Designer controls"
          mainLabel="Home preview"
          aside={
            <RowPool
              draft={EMPTY_LAYOUT}
              onChange={() => {}}
              layer="global"
              section="home"
              viewers={[]}
              available={picks}
              focusRow={null}
              onFocusRow={() => {}}
            />
          }
          main={<div>Preview</div>}
        />
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByRole("checkbox", { name: "Select Trending" }));
    const bar = screen.getByRole("region", { name: "Selected rows" });
    const aside = screen.getByRole("complementary", { name: "Designer controls" });
    expect(aside.contains(bar)).toBe(false);
    expect(within(bar).getByRole("status").textContent).toBe("1 selected");

    // Clicking Done (Clear selection) clears the selection.
    fireEvent.click(within(bar).getByRole("button", { name: "Clear selection" }));
    expect(screen.queryByRole("region", { name: "Selected rows" })).toBeNull();

    // Pressing Escape clears the selection when no dialog/drawer is open.
    fireEvent.click(screen.getByRole("checkbox", { name: "Select Trending" }));
    expect(screen.getByRole("region", { name: "Selected rows" })).toBeTruthy();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("region", { name: "Selected rows" })).toBeNull();
  });

  it("does not clear the selection on Escape while a drawer is open", () => {
    render(<Harness initial={EMPTY_LAYOUT} available={picks} />);
    fireEvent.click(screen.getByRole("checkbox", { name: "Select Trending" }));
    fireEvent.click(screen.getByRole("button", { name: "Add custom pattern" }));
    expect(screen.getByRole("dialog")).toBeTruthy();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.getByRole("region", { name: "Selected rows" })).toBeTruthy();
  });

  it("selects a whole group and resets it to default", () => {
    let last: HomeLayout | undefined;
    const initial = setStatuses(EMPTY_LAYOUT, "global", "home", ["trending", "top-10"], "always");
    render(<Harness initial={initial} available={picks} onDraft={(d) => (last = d)} />);
    fireEvent.click(screen.getByRole("checkbox", { name: "Select every row in Picks for you" }));
    fireEvent.click(screen.getByRole("button", { name: "Set the selected rows back to default" }));
    expect(rowsOf(last!)).toEqual([]);
  });

  it("changes a row's status inline, without opening it", () => {
    let last: HomeLayout | undefined;
    render(<Harness initial={EMPTY_LAYOUT} available={picks} onDraft={(d) => (last = d)} />);
    const status = screen.getByRole("radiogroup", { name: "Status of Top 10" });
    fireEvent.click(within(status).getByRole("radio", { name: "Always" }));
    expect(rowsOf(last!)).toEqual([{ row: "top-10", status: "always" }]);
  });

  it("clears the selection when the layer or what the list shows changes", () => {
    const { rerender } = render(<Harness initial={EMPTY_LAYOUT} available={picks} />);
    fireEvent.click(screen.getByRole("checkbox", { name: "Select Trending" }));
    expect(screen.getByRole("region", { name: "Selected rows" })).toBeTruthy();
    const filter = screen.getByRole("radiogroup", { name: "Show rows by status" });
    fireEvent.click(within(filter).getByRole("radio", { name: "Eligible" }));
    expect(screen.queryByRole("region", { name: "Selected rows" })).toBeNull();
    fireEvent.click(screen.getByRole("checkbox", { name: "Select Trending" }));
    rerender(<Harness initial={EMPTY_LAYOUT} available={picks} layer={7} />);
    expect(screen.queryByRole("region", { name: "Selected rows" })).toBeNull();
  });

  it("starts a viewer's bulk overrides from Everyone's policy, like a single edit", () => {
    let last: HomeLayout | undefined;
    const initial = setPolicy(EMPTY_LAYOUT, "global", "home", {
      row: "top-10",
      status: "always",
      priority: 2.5,
      maxPerPage: 1,
    });
    render(<Harness initial={initial} available={picks} layer={7} onDraft={(d) => (last = d)} />);
    fireEvent.click(screen.getByRole("checkbox", { name: "Select Top 10" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "Select Fresh" }));
    fireEvent.click(screen.getByRole("button", { name: "Set the selected rows to Off" }));
    const viewer = last!.viewers.find((v) => v.userId === 7)!.pages[0]!.rows;
    expect(viewer).toEqual([
      { row: "top-10", status: "off", priority: 2.5, maxPerPage: 1 },
      { row: "fresh", status: "off" },
    ]);
  });

  it("filters the catalogue by status", () => {
    const initial = setStatuses(EMPTY_LAYOUT, "global", "home", ["fresh"], "off");
    render(<Harness initial={initial} available={picks} />);
    const filter = screen.getByRole("radiogroup", { name: "Show rows by status" });
    fireEvent.click(within(filter).getByRole("radio", { name: "Off" }));
    expect(screen.getByRole("listitem", { name: "Row Fresh" })).toBeTruthy();
    expect(screen.queryByRole("listitem", { name: "Row Trending" })).toBeNull();
  });
});

describe("collection rows", () => {
  it("starts a long Collections group folded", () => {
    render(<Harness initial={EMPTY_LAYOUT} available={[...picks, ...collections]} />);
    const toggle = screen.getByRole("button", { name: /^Collections \d+ rows/ });
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByRole("listitem", { name: "Row Collection 3" })).toBeNull();
    fireEvent.click(toggle);
    expect(screen.getByRole("listitem", { name: "Row Collection 3" })).toBeTruthy();
  });

  it("can't fold a group a search is holding open", () => {
    render(<Harness initial={EMPTY_LAYOUT} available={[...picks, ...collections]} />);
    fireEvent.change(screen.getByRole("textbox", { name: "Filter rows" }), {
      target: { value: "Collection" },
    });
    const toggle = screen.getByRole("button", { name: /^Collections \d+ rows/ });
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect((toggle as HTMLButtonElement).disabled).toBe(true);
  });

  it("deselects a group's rows when it is folded", () => {
    render(<Harness initial={EMPTY_LAYOUT} available={picks} />);
    fireEvent.click(screen.getByRole("checkbox", { name: "Select Trending" }));
    fireEvent.click(screen.getByRole("button", { name: /^Picks for you/ }));
    expect(screen.queryByRole("region", { name: "Selected rows" })).toBeNull();
  });

  it("turns a whole Collections group off row by row, without a pattern", () => {
    let last: HomeLayout | undefined;
    const few = collections.slice(0, 3);
    render(<Harness initial={EMPTY_LAYOUT} available={few} onDraft={(d) => (last = d)} />);
    fireEvent.click(screen.getByRole("checkbox", { name: "Select every row in Collections" }));
    fireEvent.click(screen.getByRole("button", { name: "Set the selected rows to Off" }));
    expect(rowsOf(last!)).toEqual([
      { row: "collection:c0", status: "off" },
      { row: "collection:c1", status: "off" },
      { row: "collection:c2", status: "off" },
    ]);
    expect(screen.queryByRole("listitem", { name: "Row collection:*" })).toBeNull();
  });

  it("shows a row a pattern reaches with the pattern's status", () => {
    render(
      <Harness
        initial={setStatuses(EMPTY_LAYOUT, "global", "home", ["collection:*"], "off")}
        available={collections.slice(0, 3)}
      />,
    );
    const c2 = screen.getByRole("listitem", { name: "Row Collection 2" });
    expect(within(c2).getByText("via collection:*")).toBeTruthy();
    expect(within(c2).getByRole("radio", { name: "Off" }).getAttribute("aria-checked")).toBe(
      "true",
    );
  });

  it("shows what a pattern reaches and removes it in one click", () => {
    let last: HomeLayout | undefined;
    render(
      <Harness
        initial={setStatuses(EMPTY_LAYOUT, "global", "home", ["collection:*"], "off")}
        available={collections}
        onDraft={(d) => (last = d)}
      />,
    );
    const rule = screen.getByRole("listitem", { name: "Row collection:*" });
    expect(within(rule).getByText("Applies to 20 rows")).toBeTruthy();
    fireEvent.click(within(rule).getByRole("button", { name: "Remove the rule collection:*" }));
    expect(rowsOf(last!)).toEqual([]);
  });
});

describe("pattern constraint inheritance", () => {
  const pattern = {
    row: "collection:*",
    status: "always" as const,
    priority: 2.5,
    minSlot: 2,
    maxSlot: 8,
    maxPerPage: 1,
    when: { days: [1, 2] },
  };
  it.each(["global", 7] as const)("preserves matched constraints in a bulk edit in %s", (layer) => {
    let last: HomeLayout | undefined;
    render(
      <Harness
        initial={setPolicy(EMPTY_LAYOUT, "global", "home", pattern)}
        available={collections.slice(0, 2)}
        layer={layer}
        onDraft={(d) => (last = d)}
      />,
    );
    fireEvent.click(screen.getByRole("checkbox", { name: "Select Collection 0" }));
    fireEvent.click(screen.getByRole("button", { name: "Set the selected rows to Off" }));
    const pages = layer === "global" ? last!.global : last!.viewers[0]!.pages;
    expect(pages[0]!.rows.find((r) => r.row === "collection:c0")).toEqual({
      ...pattern,
      row: "collection:c0",
      status: "off",
    });
  });
  it("preserves matched constraints in an inline edit", () => {
    let last: HomeLayout | undefined;
    render(
      <Harness
        initial={setPolicy(EMPTY_LAYOUT, "global", "home", pattern)}
        available={collections.slice(0, 2)}
        onDraft={(d) => (last = d)}
      />,
    );
    const row = screen.getByRole("listitem", { name: "Row Collection 0" });
    fireEvent.click(within(row).getByRole("radio", { name: "Off" }));
    expect(rowsOf(last!).find((r) => r.row === "collection:c0")).toEqual({
      ...pattern,
      row: "collection:c0",
      status: "off",
    });
  });
});

describe("large catalogues", () => {
  it("bulk edits 1000 folded collection rows without creating a pattern", () => {
    const large = Array.from({ length: 1000 }, (_, i) => ({
      ...collections[0]!,
      hubId: `collection:large${i}`,
      kind: `collection:large${i}`,
      title: `Large ${i}`,
    }));
    let last: HomeLayout | undefined;
    render(<Harness initial={EMPTY_LAYOUT} available={large} onDraft={(d) => (last = d)} />);
    expect(screen.queryByRole("listitem")).toBeNull();
    fireEvent.click(screen.getByRole("checkbox", { name: "Select every row in Collections" }));
    fireEvent.click(screen.getByRole("button", { name: "Set the selected rows to Off" }));
    expect(rowsOf(last!)).toHaveLength(1000);
    expect(rowsOf(last!).every((r) => r.status === "off" && !r.row.includes("*"))).toBe(true);
  });
  it("keeps inherited anchors unique across selected rows", () => {
    const initial = setPolicy(EMPTY_LAYOUT, "global", "home", {
      row: "collection:*",
      status: "always",
      slot: 2,
    });
    const result = setStatuses(
      initial,
      7,
      "home",
      ["collection:c0", "collection:c1"],
      "always",
      () => initial.global[0]!.rows[0],
    );
    expect(result.viewers[0]!.pages[0]!.rows).toEqual([
      { row: "collection:c0", status: "always" },
      { row: "collection:c1", status: "always", slot: 2 },
    ]);
  });
});

it("does not bulk edit a selected row hidden by an inline status change", () => {
  render(<Harness initial={EMPTY_LAYOUT} available={picks} />);
  const filter = screen.getByRole("radiogroup", { name: "Show rows by status" });
  fireEvent.click(within(filter).getByRole("radio", { name: "Eligible" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Select Trending" }));
  const status = screen.getByRole("radiogroup", { name: "Status of Trending" });
  fireEvent.click(within(status).getByRole("radio", { name: "Off" }));
  expect(screen.queryByRole("region", { name: "Selected rows" })).toBeNull();
});
