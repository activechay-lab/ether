import { routeApiImagesThroughFetch } from "../lib/api";
import { fetchUpload, setUploadTransport } from "../pages/playback/prerolls-api";
import { setMockAgentEnabled, setMockAgentPace } from "./extend/agent";
import { createFakeServer } from "./server";

/** Route same-origin /ether/api requests to the in-browser fake (mock mode only). */
export function installMockBackend(): void {
  // `?cluster` previews the owner UI as a node of a cluster.
  const params = new URLSearchParams(window.location.search);
  const cluster = params.has("cluster");
  // `?startup=migrating` (or `failed:database_corrupt`, …) previews the
  // startup page; `?viewers=2` puts viewers on the restart drawers;
  // `?opsPace=` slows a simulated restart down (milliseconds per step);
  // `?damaged` reports a damaged database; `?restore=restored|failed|pending`
  // seeds the Backups page's restore report.
  // `?agentPace=` sets milliseconds between streamed agent words (default 70).
  setMockAgentEnabled(params.get("model") !== "off");
  setMockAgentPace(Number(params.get("agentPace") ?? 70));
  const fake = createFakeServer({
    latencyMs: 120,
    liveTVEmpty: params.get("livetv") === "empty",
    onAirLayout: params.get("onair") ?? undefined,
    householdAudio: true,
    cluster,
    startup: params.get("startup") ?? undefined,
    viewers: Number(params.get("viewers") ?? 0),
    opsPace: params.has("opsPace") ? Number(params.get("opsPace")) : undefined,
    damagedDatabase: params.has("damaged"),
    cacheRestart: params.has("cacheRestart"),
    restore: params.get("restore") ?? undefined,
    prerolls: params.has("prerolls"),
  });
  if (params.get("backups") === "empty") fake.state.backups = [];
  fake.state.config.frontends = Object.fromEntries(
    ["plex", "jellyfin", "emby", "silo", "stremio", "webdav", "m3u", "xtream", "dlna"].map((id) => [
      id,
      { enabled: true },
    ]),
  );
  // Deterministic account workspaces for owner-console visual review.
  if (params.get("integrations") === "all") {
    const c = fake.state.config;
    c.catalog.tmdbKey = "[configured]";
    c.trakt = { enabled: true, clientId: "ether-trakt", clientSecret: "[configured]" };
    c.simkl = { enabled: true, clientId: "ether-simkl", clientSecret: "[configured]" };
    c.mdblist = { enabled: true };
    c.openSubtitles = {
      enabled: true,
      apiKey: "[configured]",
      username: "ether",
      password: "[configured]",
    };
    c.subdl = { enabled: true, apiKey: "[configured]" };
    c.markers = { ...c.markers, theIntroDb: true, apiKey: "[configured]" };
    c.posters = {
      version: 1,
      enabled: false,
      source: "rpdb",
      baseUrl: "",
      apiKey: "",
      urlTemplate: "",
      accounts: Object.fromEntries(
        ["postersplus", "xrdb", "betterposters", "rpdb", "custom"].map((id) => [
          id,
          {
            enabled: true,
            source: id,
            baseUrl: id === "xrdb" ? "https://xrdb.example" : "http://postersplus:8000",
            apiKey: "[configured]",
            urlTemplate: id === "custom" ? "https://posters.example/{type}/{tmdb_id}" : "",
            profile: id === "xrdb" ? "family" : "",
          },
        ]),
      ),
    };
  } else if (params.get("integrations") === "fresh") {
    fake.state.config.posters = undefined;
    fake.state.config.trakt.enabled = false;
  }
  // Private mock-only fixture for reviewing live station artwork.
  if (params.has("liveArtwork")) {
    const original = fake.fetch;
    fake.fetch = async (input, init) => {
      const path = new URL(String(input), window.location.origin).pathname;
      const logo = path.match(/\/livetv\/channels\/(ink-dark|ink-light)\/logo$/);
      if (logo) {
        const darkInk = logo[1] === "ink-dark";
        return new Response(
          `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 240 160"><g fill="${darkInk ? "#111" : "#fff"}"><path d="M40 30h25v100H40zm40 0h25v100H80zm40 0h25v100h-25zm40 0h40v25h-40zm0 38h40v25h-40zm0 37h40v25h-40z"/></g></svg>`,
          {
            headers: {
              "Content-Type": "image/svg+xml",
              "X-Ether-Logo-Background": darkInk ? "light" : "dark",
            },
          },
        );
      }
      const response = await original(input, init);
      if (!path.endsWith("/dashboard") && !path.endsWith("/status/sessions")) return response;
      const body = await response.json();
      const sessions = ["ink-dark", "ink-light"].map((id, i) => ({
        ID: id,
        TitleName: i ? "Evening News" : "Morning Edition",
        Username: i ? "Alex" : "Sam",
        Client: { product: "Plex for Apple TV", deviceName: i ? "Bedroom" : "Living Room" },
        Decision: "copy",
        liveChannel: { id, name: i ? "World News" : "Local News", number: i ? "24" : "12.1" },
        StartedAt: new Date().toISOString(),
      }));
      if (path.endsWith("/dashboard")) body.sections.sessions = sessions;
      else body.value = sessions;
      return new Response(JSON.stringify(body), {
        headers: { "Content-Type": "application/json" },
      });
    };
  }
  // Owner screenshot fixtures: opt in with ?review. Switch availability/sync
  // scenarios through sessionStorage["ether.review.state"] (done/running/empty/failed/pin).
  if (params.has("review")) {
    const original = fake.fetch;
    fake.fetch = async (input, init) => {
      const path = new URL(String(input), window.location.origin).pathname;
      const state = sessionStorage.getItem("ether.review.state") || "done";
      const json = (value: unknown) =>
        new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json" } });
      if (path.endsWith("/discovery/settings"))
        return json({
          enabled: true,
          plex: true,
          trakt: true,
          plexFriendsStatus: [
            {
              id: "friend-1",
              username: "Sam",
              configured: true,
              lastRead: Math.floor(Date.now() / 1000),
              imported: 12,
              unmatchedTitles: 0,
            },
            {
              id: "friend-2",
              username: "Jordan",
              configured: true,
              lastRead: Math.floor(Date.now() / 1000),
              imported: 0,
              unmatchedTitles: 0,
              problem: "upstream",
              error: "Plex could not read this watchlist. Try again later.",
            },
          ],
        });
      if (path.includes("/discovery/users/")) {
        if (path.includes("/sync/jobs"))
          return json({
            id: 1,
            state: state === "running" ? "running" : "completed",
            plex: state === "pin" ? "pin" : "synced",
            count: 3,
          });
        if (path.endsWith("/check"))
          return json({
            state,
            done: 2,
            total: 3,
            enabled: true,
            nextCheck: new Date(Date.now() + 1800000).toISOString(),
          });
        return json(
          state === "empty"
            ? []
            : [
                {
                  title: {
                    key: "movie:1",
                    title: "The Grand Budapest Hotel",
                    released: "2014-03-28",
                  },
                  sources: ["Plex Home"],
                  availableAt: new Date().toISOString(),
                  checkedAt: new Date().toISOString(),
                },
                {
                  title: { key: "movie:2", title: "Arrival", released: "2016-11-11" },
                  sources: ["Friends' Watchlist"],
                  checkedAt: new Date().toISOString(),
                },
                {
                  title: { key: "movie:3", title: "Spirited Away", released: "2001-07-20" },
                  sources: ["Trakt"],
                },
              ],
        );
      }
      if (path.endsWith("/logs/stream"))
        return new Response(
          "event: snapshot\ndata: " +
            JSON.stringify({ entries: fake.state.logs, total: fake.state.logs.length }) +
            "\n\n",
          { headers: { "Content-Type": "text/event-stream" } },
        );
      if (path.endsWith("/poster-folders"))
        return json({
          breadcrumbs: [{ name: "Posters", path: "/media/posters" }],
          path: "/media/posters",
          parent: "/media",
          directories: ["Movies", "Shows", "Family"],
          posters: 128,
          limited: false,
        });
      const response = await original(input, init);
      if (path.includes("/libraries/3/item/")) {
        const body = await response.json();
        body.item.unavailable = {
          code: "file_missing",
          detail:
            "The file was moved or removed from the library folder. Choose another file or sync this library.",
        };
        return json(body);
      }
      if (path.endsWith("/plex-servers/step-aside/explain"))
        return json({
          title: "The Iron Echo",
          key: 2,
          kind: "movie",
          cause: "held",
          servers: ["Living room PMS"],
          missing: [],
          missingMore: 0,
        });
      if (path.endsWith("/plex-servers")) {
        const body = await response.json();
        if (Array.isArray(body))
          for (const server of body) {
            server.stepAside = true;
            server.stepAsideStatus = {
              state: "complete",
              movies: 124,
              showsFull: 18,
              showsPartial: 3,
              episodes: 420,
              unmatched: 2,
              lastScan: Math.floor(Date.now() / 1000) - 120,
            };
          }
        return json(body);
      }
      return response;
    };
  }
  const real = window.fetch.bind(window);
  window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(
      input instanceof Request ? input.url : String(input),
      window.location.origin,
    );
    if (url.origin === window.location.origin && url.pathname.startsWith("/ether/api/"))
      return fake.fetch(url.toString(), init);
    return real(input, init);
  };
  // Generated artwork (/ether/api/collections/{id}/artwork) must come from the fake too.
  routeApiImagesThroughFetch(true);
  // Pre-roll uploads go through the fake too, with progress paced like a LAN upload.
  setUploadTransport(async (url, form, onProgress, signal) => {
    for (let p = 0; p < 100; p += 7) {
      if (signal?.aborted) throw new DOMException("Aborted", "AbortError");
      onProgress(p);
      await new Promise((r) => setTimeout(r, 120));
    }
    return fetchUpload(url, form, onProgress, signal);
  });
  console.info("[ether] mock backend active: all /ether/api calls are served in the browser.");
}
