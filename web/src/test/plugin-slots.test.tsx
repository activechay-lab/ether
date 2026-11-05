import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { Toaster } from "@braedonsaunders/appkit-ui";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { resetRegistryCache } from "../lib/extend";
import { PanelSlots, PluginActionMenu, useSlots } from "../viewspec/slots/Slots";
import { galleryDoc } from "../viewspec/fixtures/galleryDoc";
const descriptor = (slot: string, key = "panel") => ({
  kind: "slot",
  id: `plugin:fixture/${key}`,
  owner: "fixture",
  group: "Fixture plugin",
  label: { resolved: `Fixture ${slot}` },
  meta: { slot, key, action: "run" },
});
let calls: { url: string; body?: unknown }[];
function setup(ds: unknown[], result: unknown = {}) {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, body: typeof init?.body === "string" ? JSON.parse(init.body) : undefined });
      const data = url.includes("registry")
        ? { version: 1, descriptors: ds }
        : url.includes("/views/")
          ? galleryDoc
          : result;
      return new Response(JSON.stringify(data), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }),
  );
}
beforeEach(() => {
  resetRegistryCache();
});
afterEach(() => {
  vi.unstubAllGlobals();
});
function renderIn(node: React.ReactNode) {
  return render(<MemoryRouter>{node}</MemoryRouter>);
}
it("does not fetch or render plugin views when no slots exist", async () => {
  setup([]);
  const { container } = renderIn(<PanelSlots slot="dashboard.panel" />);
  await waitFor(() => expect(calls.length).toBeGreaterThan(0));
  expect(calls.filter((c) => c.url.includes("/views/"))).toHaveLength(0);
  expect(container.childElementCount).toBe(0);
});
it("loads the title panel with identifiers and lazily loads multiple panels", async () => {
  setup([descriptor("title.panel", "first"), descriptor("title.panel", "second")]);
  renderIn(
    <PanelSlots
      slot="title.panel"
      recordType="title"
      recordId="movie:4"
      tmdbId={4}
      imdbId="tt123"
      titleKey="movie:4"
      type="movie"
    />,
  );
  await screen.findAllByText("Fixture title.panel");
  expect(calls.filter((c) => c.url.includes("/views/"))).toHaveLength(0);
  fireEvent.click(screen.getAllByText("Fixture title.panel")[0]!);
  await screen.findByText("Gallery plugin");
  const url = calls.find((c) => c.url.includes("/views/"))!.url;
  expect(url).toContain("recordId=movie%3A4");
  expect(url).toContain("tmdbId=4");
  expect(url).toContain("imdbId=tt123");
  expect(url).toContain("titleKey=movie%3A4");
  expect(url).toContain("type=movie");
});
it("renders dashboard and discovers viewer tabs", async () => {
  const originalObserver = globalThis.IntersectionObserver;
  globalThis.IntersectionObserver = undefined as unknown as typeof IntersectionObserver;
  try {
    setup([descriptor("dashboard.panel"), descriptor("viewer.tab", "tab")]);
    function Tabs() {
      const slots = useSlots("viewer.tab");
      return (
        <div>
          {slots.map((d) => (
            <span key={d.id}>{d.meta?.key}</span>
          ))}
        </div>
      );
    }
    renderIn(
      <>
        <PanelSlots slot="dashboard.panel" />
        <Tabs />
      </>,
    );
    expect(await screen.findByText("Gallery plugin")).toBeTruthy();
    expect(await screen.findByText("tab")).toBeTruthy();
  } finally {
    globalThis.IntersectionObserver = originalObserver;
  }
});
it.each(["title", "viewer", "collection", "release"] as const)(
  "posts %s actions and handles approval",
  async (type) => {
    setup([descriptor(`actionMenu.${type}`)], { proposal: { id: "p1", title: "Confirm action" } });
    renderIn(<PluginActionMenu slot={`actionMenu.${type}`} recordId="42" />);
    const button = await screen.findByRole("button", { name: "More actions" });
    fireEvent.click(button);
    fireEvent.click(await screen.findByText(`Fixture actionMenu.${type}`));
    await screen.findByText("Confirm action");
    expect(calls.find((c) => c.url.includes("/actions/run"))?.body).toEqual({
      params: { recordType: type, recordId: "42" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => expect(calls.some((c) => c.url.includes("/proposals/p1"))).toBe(true));
  },
);

it("opens the fixture viewer tab only after it is selected", async () => {
  const { renderApp, calls: apiCalls } = await import("./harness");
  const { fake } = renderApp("/viewers/2");
  const tab = await screen.findByRole("tab", { name: "Fixture viewer.tab" });
  expect(
    apiCalls(fake, "GET", "/plugins/community.example.wrapped/views/fixture-viewer.tab"),
  ).toHaveLength(0);
  fireEvent.click(tab);
  await waitFor(() =>
    expect(
      apiCalls(fake, "GET", "/plugins/community.example.wrapped/views/fixture-viewer.tab"),
    ).toHaveLength(1),
  );
  expect(
    screen.getByRole("tab", { name: "Fixture viewer.tab" }).getAttribute("aria-selected"),
  ).toBe("true");
});

it("applies action toast, refresh and open results", async () => {
  setup([descriptor("actionMenu.title")], {
    toast: "Updated",
    refresh: true,
    open: { route: "/metadata" },
  });
  const refresh = vi.fn();
  function Location() {
    const location = useLocation();
    return <span>{location.pathname}</span>;
  }
  renderIn(
    <>
      <Toaster />
      <PluginActionMenu slot="actionMenu.title" recordId="1" onRefresh={refresh} />
      <Location />
    </>,
  );
  fireEvent.click(await screen.findByRole("button", { name: "More actions" }));
  fireEvent.click(await screen.findByText("Fixture actionMenu.title"));
  expect(await screen.findByText("Updated")).toBeTruthy();
  expect(await screen.findByText("/metadata")).toBeTruthy();
  expect(refresh).toHaveBeenCalledOnce();
});

it("shows a job toast without applying other action effects", async () => {
  setup([descriptor("actionMenu.title")], {
    job: "scan",
    toast: "Scanning",
    refresh: true,
    open: { route: "/metadata" },
  });
  const refresh = vi.fn();
  function Location() {
    return <span>{useLocation().pathname}</span>;
  }
  renderIn(
    <>
      <Toaster />
      <PluginActionMenu slot="actionMenu.title" recordId="1" onRefresh={refresh} />
      <Location />
    </>,
  );
  fireEvent.click(await screen.findByRole("button", { name: "More actions" }));
  fireEvent.click(await screen.findByText("Fixture actionMenu.title"));
  expect(await screen.findByText("Scanning")).toBeTruthy();
  expect(refresh).not.toHaveBeenCalled();
  expect(screen.getByText("/")).toBeTruthy();
});
