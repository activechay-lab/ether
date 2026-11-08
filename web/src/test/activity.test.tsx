import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, it, expect, vi } from "vitest";
import { setToken } from "../lib/api";
import { descriptors as notifyDescriptors } from "../mock/extend/descriptors/notify";
import Activity from "../pages/activity/Activity";
import Notifications from "../pages/notifications/Notifications";
import { HardwareStatus } from "../pages/playback/Hardware";
import { SourceStatistics } from "../pages/sources/statistics/Statistics";

const WAIT = { timeout: 4000 };
const emptySummary = {
  plays: 0,
  watchedMs: 0,
  bytes: 0,
  completed: 0,
  daily: [],
  hourly: [],
  platforms: [],
  titles: [],
  users: [],
};
const schedule = {
  enabled: false,
  weekday: 1,
  hour: 9,
  userId: 0,
  nextDue: 0,
  lastAttempt: 0,
  lastStatus: "",
};

function backend(cluster = false) {
  const calls: { url: string; init?: RequestInit }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, init });
      let v: unknown = { ok: true };
      if (url.endsWith("/cluster/"))
        v = cluster
          ? {
              mode: "postgres",
              self: { id: "server" },
              nodes: [
                { id: "source", name: "Media source" },
                { id: "gpu", name: "GPU worker" },
              ],
            }
          : { mode: "solo" };
      else if (url.endsWith("/cluster/console"))
        v = {
          sampledAt: new Date().toISOString(),
          truncated: false,
          assignments: [
            {
              sessionId: "execution",
              playId: "live",
              sourceNodeId: "source",
              encoderNodeId: "gpu",
              encoder: "h264_nvenc",
            },
            {
              sessionId: "remote",
              sourceNodeId: "source",
              encoderNodeId: "gpu",
              encoder: "cpu",
              playback: { title: "Remote film", viewer: "Jordan", decision: "transcode" },
            },
          ],
          events: [{ sessionId: "execution", status: "recovering" }],
        };
      else if (url.includes("/extend/registry")) v = { version: 1, descriptors: notifyDescriptors };
      else if (url.includes("/stats/summary")) v = emptySummary;
      else if (url.includes("/stats/history")) v = [];
      else if (url.endsWith("/users"))
        v = {
          users: [
            { id: 7, title: "Sam", username: "sam-account", isOwner: true },
            { id: 12, title: "Alex", username: "alex-account", isOwner: false },
          ],
        };
      else if (url.endsWith("/status/sessions"))
        v = {
          value: [
            {
              ID: "live",
              TitleName: "Example film",
              Username: "Sam",
              Decision: "directplay",
              SpeedBps: 125000,
              Client: { deviceName: "Phone" },
            },
            {
              ID: "other",
              TitleName: "Other film",
              Username: "Alex",
              Client: { deviceName: "TV", protocol: "jellyfin" },
            },
          ],
        };
      else if (url.endsWith("/indexer-usage"))
        v = {
          today: "2026-11-03",
          weekStart: "2026-10-28",
          monthStart: "2026-11-01",
          indexers: [
            {
              id: "ix1",
              name: "My indexer",
              enabled: true,
              torrent: false,
              dailyLimit: 100,
              dailyGrabs: 0,
              today: {
                hits: 7,
                grabs: 1,
                purposes: [
                  { purpose: "feed", hits: 4, grabs: 0 },
                  { purpose: "playback", hits: 3, grabs: 1 },
                ],
              },
              week: { hits: 30, grabs: 2, purposes: [{ purpose: "feed", hits: 30, grabs: 2 }] },
              month: {
                hits: 50,
                grabs: 2,
                purposes: [{ purpose: "unattributed", hits: 50, grabs: 2 }],
              },
              days: [],
            },
          ],
        };
      else if (url.includes("/stats/sources"))
        v = {
          days: 7,
          since: "2026-10-28",
          until: "2026-11-03",
          plays: [{ day: "2026-11-03", origin: "Usenet", plays: 2, failovers: 1, failed: 0 }],
          providerDays: [
            {
              id: "provider-key",
              day: "2026-11-03",
              bytes: 4e9,
              articles: 900,
              misses: 10,
              errors: 0,
              backfillSaves: 0,
              bodyMisses: 0,
              bodyTakedowns: 0,
              spillovers: 0,
            },
          ],
          indexerDays: [],
          debridDays: [],
          streamDays: [],
        };
      else if (url.endsWith("/stats/infrastructure"))
        v = {
          snapshots: {
            providers: {
              at: 1,
              data: [
                {
                  id: "provider-key",
                  name: "My news provider",
                  host: "news.example",
                  state: "ok",
                  avgLatency: 0,
                },
              ],
            },
            indexers: {
              at: 1,
              data: [
                {
                  indexer: "My indexer",
                  hitsUsed: 2,
                  hitsRemaining: 98,
                  grabsUsed: 1,
                  grabsRemaining: 49,
                },
              ],
            },
          },
          providerMonth: [],
          indexerMonth: [],
          costs: [],
          notes: [],
        };
      else if (url.includes("/discovery/settings"))
        v = {
          enabled: false,
          plex: true,
          trakt: true,
          notify: false,
          prewarm: false,
          checkHours: 6,
          naturalSearch: false,
          recaps: false,
        };
      else if (url.endsWith("/notifications/")) v = { channels: [], deliveries: [] };
      else if (url.endsWith("/status/hwaccel"))
        v = { value: { hwaccel: { name: "none", encoders: [] } } };
      else if (url.endsWith("/newsletter/settings") && !init?.method) v = schedule;
      return new Response(JSON.stringify(v), { status: 200 });
    }),
  );
  return calls;
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/activity/:tab?" element={<Activity />} />
        <Route path="/notifications/:tab?" element={<Notifications />} />
        <Route path="/sources/:tab/:section?" element={<SourceStatistics />} />
        <Route path="/hardware" element={<HardwareStatus />} />
      </Routes>
    </MemoryRouter>,
  );
}

/** Open an AppKit select by its accessible name and pick an option. */
async function choose(label: string, option: string) {
  fireEvent.click(screen.getByRole("button", { name: label }));
  fireEvent.click(await screen.findByRole("option", { name: option }));
}

const body = (c: { init?: RequestInit }) => JSON.parse(String(c.init?.body));

describe("Activity", () => {
  it("shows actual live streams and sends stop action", async () => {
    const calls = backend();
    renderAt("/activity");
    expect(await screen.findByText("Example film", {}, WAIT)).toBeInTheDocument();
    const card = screen.getByText("Example film").closest("article")!;
    expect(within(card).getByText("Sam")).toBeInTheDocument();
    expect(within(card).getByText("Phone")).toBeInTheDocument();
    await choose("Protocol", "jellyfin");
    expect(screen.queryByText("Example film")).toBeNull();
    expect(screen.getByText("Other film")).toBeInTheDocument();
    await choose("Protocol", "plex");
    expect(screen.queryByText("Other film")).toBeNull();
    expect(
      within(screen.getByText("Example film").closest("article")!).getByText("plex"),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /Stop stream for/ }));
    await waitFor(() =>
      expect(
        calls.some((c) => c.url.endsWith("/stats/sessions/live/stop") && c.init?.method === "POST"),
      ).toBe(true),
    );
    fireEvent.click(screen.getByRole("tab", { name: "History" }));
    expect(
      await screen.findByText(
        "History starts when Ether observes playback. There are no recorded plays in this period.",
        {},
        WAIT,
      ),
    ).toBeInTheDocument();
  });

  it("joins cluster assignments without duplicating the source session and includes remote executions", async () => {
    backend(true);
    renderAt("/activity");
    await screen.findByText("Remote film", {}, WAIT);
    expect(screen.getAllByText("Example film")).toHaveLength(1);
    const card = screen.getByText("Example film").closest("article")!;
    expect(within(card).getByText("Media source")).toBeVisible();
    expect(within(card).getByText("GPU worker · GPU · h264_nvenc")).toBeVisible();
    expect(within(card).getByText("Any delivery node")).toBeVisible();
    expect(within(card).getByText("Recovering")).toBeVisible();
    expect(within(card).getByRole("link", { name: /Recovery history/ })).toHaveAttribute(
      "href",
      "/cluster?tab=recovery&session=execution",
    );
    expect(screen.getByText("GPU worker · CPU")).toBeVisible();
  });

  it("filters by a named viewer while sending the existing account ID", async () => {
    const calls = backend();
    renderAt("/activity/history");
    await screen.findByText(/History starts when Ether observes playback/, {}, WAIT);
    fireEvent.click(screen.getByRole("button", { name: "Viewer" }));
    expect(await screen.findByRole("option", { name: "Sam (owner)" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("option", { name: "Alex" }));
    await waitFor(() =>
      expect(calls.some((c) => c.url.includes("/stats/summary?days=30&user=12"))).toBe(true),
    );
    await waitFor(() =>
      expect(calls.some((c) => c.url.includes("/stats/history?days=30&user=12"))).toBe(true),
    );
  });

  it("shows viewing totals and breakdowns on the Viewing tab", async () => {
    backend();
    renderAt("/activity/viewing");
    expect(await screen.findByText("Hours watched", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByText("Watch time")).toBeInTheDocument();
    expect(screen.getByText("Top titles")).toBeInTheDocument();
    expect(screen.getByText("Top viewers")).toBeInTheDocument();
  });
});

describe("Notifications", () => {
  it("saves the weekly schedule in place through the save bar", async () => {
    const calls = backend();
    renderAt("/notifications/report");
    const toggle = await screen.findByLabelText("Email the weekly report", {}, WAIT);
    expect(screen.queryByRole("button", { name: "Configure weekly report" })).toBeNull();
    expect(screen.queryByRole("region", { name: "Unsaved changes" })).toBeNull();
    fireEvent.click(toggle);
    await choose("Report viewer", "Alex");
    await choose("Day", "Friday");
    const bar = screen.getByRole("region", { name: "Unsaved changes" });
    fireEvent.click(within(bar).getByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(
        calls.some(
          (c) =>
            c.url.endsWith("/newsletter/settings") &&
            c.init?.method === "PUT" &&
            body(c).enabled === true &&
            body(c).userId === 12 &&
            body(c).weekday === 5 &&
            body(c).hour === 9,
        ),
      ).toBe(true),
    );
  });

  it("previews the report for the chosen viewer inline", async () => {
    setToken("ether-admin-report");
    const calls = backend();
    renderAt("/notifications/report");
    await screen.findByLabelText("Email the weekly report", {}, WAIT);
    fireEvent.click(screen.getByLabelText("Email the weekly report"));
    await choose("Report viewer", "Alex");
    fireEvent.click(screen.getByRole("button", { name: "Preview weekly report" }));
    await waitFor(() =>
      expect(calls.some((c) => c.url.endsWith("/stats/newsletter?user=12"))).toBe(true),
    );
    const preview = calls.find((c) => c.url.endsWith("/stats/newsletter?user=12"))!;
    const sent = new Headers(preview.init?.headers);
    expect(sent.get("X-Ether-Admin-Token")).toBe("ether-admin-report");
    expect(sent.get("X-Plex-Token")).toBeNull();
    setToken("");
    expect(await screen.findByTitle("Weekly newsletter preview")).toBeInTheDocument();
    expect(screen.getByLabelText("Email the weekly report")).toBeChecked();
    expect(screen.getByRole("button", { name: "Report viewer" })).toHaveTextContent("Alex");
    // A preview never saves the schedule.
    expect(calls.some((c) => c.init?.method === "PUT")).toBe(false);
  });

  it("creates a channel with real event rules in a drawer", async () => {
    const calls = backend();
    renderAt("/notifications");
    fireEvent.click(await screen.findByRole("button", { name: "New channel" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "New notification channel" });
    fireEvent.change(await within(drawer).findByLabelText("Endpoint URL"), {
      target: { value: "https://notify.example/hook" },
    });
    fireEvent.click(within(drawer).getByRole("button", { name: "Clear" }));
    expect(within(drawer).getByLabelText("Weekly newsletter")).toBeDisabled();
    fireEvent.click(within(drawer).getByLabelText("Playback failed"));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save channel" }));
    await waitFor(() =>
      expect(
        calls.some(
          (c) =>
            c.url.includes("/notifications/") &&
            c.init?.method === "PUT" &&
            body(c).secrets?.endpoint === "https://notify.example/hook" &&
            body(c).events.includes("playback.error"),
        ),
      ).toBe(true),
    );
  });

  it("shows the delivery history tab", async () => {
    backend();
    renderAt("/notifications/deliveries");
    expect(await screen.findByText("No queued notifications", {}, WAIT)).toBeInTheDocument();
  });
});

describe("Source statistics", () => {
  it("opens a source from the usage table and saves its plan price in the drawer", async () => {
    const calls = backend();
    renderAt("/sources/statistics/usage");
    fireEvent.click(await screen.findByText("My news provider", {}, WAIT));
    const price = await screen.findByLabelText("Monthly price for My news provider", {}, WAIT);
    expect(screen.queryByLabelText("Service ID")).not.toBeInTheDocument();
    fireEvent.change(price, { target: { value: "8.50" } });
    fireEvent.click(screen.getByRole("button", { name: "Save price" }));
    await waitFor(() =>
      expect(
        calls.some(
          (c) =>
            c.url.endsWith("/stats/costs") &&
            c.init?.method === "PUT" &&
            body(c).kind === "provider" &&
            body(c).id === "provider-key" &&
            body(c).monthly === 8.5,
        ),
      ).toBe(true),
    );
  });

  it("shows an indexer's real API hits by purpose in its drawer", async () => {
    backend();
    renderAt("/sources/statistics/usage?source=indexer:My%20indexer");
    // Real API hits by purpose, matching the indexer's own counter.
    expect(await screen.findByText("7 of 100", {}, WAIT)).toBeInTheDocument();
    const byPurpose = screen.getByRole("list", { name: "API hits by purpose" });
    expect(byPurpose).toHaveTextContent("Release-feed polls");
    expect(byPurpose).toHaveTextContent("Playback searches");
    fireEvent.click(screen.getByRole("radio", { name: "This month" }));
    expect(screen.getByRole("list", { name: "API hits by purpose" })).toHaveTextContent(
      "Before this breakdown",
    );
  });
});

describe("Hardware status", () => {
  it("shows a tone-map failure on Activity and the transcoder card", async () => {
    const notice = "This FFmpeg cannot tone-map HDR video to SDR";
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        const value = url.endsWith("/status/sessions")
          ? []
          : url.endsWith("/users")
            ? undefined
            : undefined;
        if (url.endsWith("/status/hwaccel")) {
          return new Response(
            JSON.stringify({
              value: { hwaccel: { name: "none", encoders: [] }, playbackError: notice },
            }),
            { status: 200 },
          );
        }
        if (url.endsWith("/status/sessions")) {
          return new Response(JSON.stringify({ value: [] }), { status: 200 });
        }
        if (url.endsWith("/users"))
          return new Response(JSON.stringify({ users: [] }), { status: 200 });
        return new Response(JSON.stringify(value ?? { ok: true }), { status: 200 });
      }),
    );
    const activity = renderAt("/activity");
    expect(await screen.findByRole("alert", {}, WAIT)).toHaveTextContent("cannot tone-map");
    activity.unmount();
    renderAt("/hardware");
    expect(await screen.findByRole("alert", {}, WAIT)).toHaveTextContent("cannot tone-map");
  });

  it("brands VA-API by the GPU's vendor, not always AMD", async () => {
    for (const [vendor, title] of [
      ["intel", "Intel VA-API"],
      ["amd", "AMD VA-API"],
      [undefined, "VA-API"],
    ] as const) {
      vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
          const url = String(input);
          if (url.endsWith("/status/hwaccel"))
            return new Response(
              JSON.stringify({
                value: {
                  hwaccel: {
                    name: "vaapi",
                    encoders: ["h264_vaapi"],
                    device: "/dev/dri/renderD128",
                  },
                  ...(vendor ? { vendor } : {}),
                },
              }),
              { status: 200 },
            );
          if (url.endsWith("/users"))
            return new Response(JSON.stringify({ users: [] }), { status: 200 });
          return new Response(JSON.stringify({ value: [] }), { status: 200 });
        }),
      );
      const view = renderAt("/hardware");
      expect(await screen.findByText(title, { exact: true }, WAIT)).toBeInTheDocument();
      view.unmount();
    }
  });

  it("reports software-only transcoding", async () => {
    backend();
    renderAt("/hardware");
    expect(await screen.findByText("Software only", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByText("No hardware encoders detected")).toBeInTheDocument();
  });
});
