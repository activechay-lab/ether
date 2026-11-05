import { routeApiImagesThroughFetch } from "../lib/api";
import { fetchUpload, setUploadTransport } from "../pages/playback/prerolls-api";
import { setMockAgentPace } from "./extend/agent";
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
  setMockAgentPace(Number(params.get("agentPace") ?? 70));
  const fake = createFakeServer({
    latencyMs: 120,
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
  fake.state.config.frontends = Object.fromEntries(
    ["plex", "jellyfin", "emby", "silo", "stremio", "webdav", "m3u", "dlna"].map((id) => [
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
    c.openSubtitles = {
      enabled: true,
      apiKey: "[configured]",
      username: "ether",
      password: "[configured]",
    };
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
