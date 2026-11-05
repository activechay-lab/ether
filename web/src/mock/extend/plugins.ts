/**
 * Mock plugin host (WS7, extended by WS10a): stateful fixture data for every
 * /api/plugins route the real host serves, so Server › Plugins works against
 * the same shapes. Three installed plugins: a verified Live TV source with an
 * update waiting, an Unverified ranking-step extension, and a verified
 * extension with a page, a list source and an attention health.
 */
import type { Descriptor } from "../../lib/extend";
import type { ExtendHandler } from "./index";
import { createStudioMock } from "./studio";

interface PluginSummary {
  id: string;
  name: string;
  version: string;
  kind?: string;
  summary?: string;
  icon?: string;
  enabled: boolean;
  verified: boolean;
  shadow: boolean;
  autoUpdate: boolean;
  updateVersion?: string;
  prevVersion?: string;
  health: string;
  capabilities?: string[];
  sourceKind?: string;
  stremioOnly?: boolean;
  installedAt: number;
  roles?: { role: string; on: boolean }[];
  host?: string;
}

interface PluginHealth {
  status: string;
  reason?: string;
  restarts: number;
  slow: number;
  drops: number;
  lastError?: string;
  lastOk?: string;
  nextRetry?: string;
  observed?: Record<string, number>;
  egressBytes: number;
}

interface PluginDetail extends PluginSummary {
  manifest: Record<string, unknown>;
  permissions: {
    lines: string[];
    declared: Record<string, unknown>;
    grants: { viewers?: number[]; folders?: string[]; revoked?: string[]; lan?: boolean };
    needsViewers: boolean;
    needsFolders: boolean;
  };
  healthDetail: PluginHealth;
  privacy: {
    declaredHosts: string[];
    observedHosts: Record<string, number>;
    undeclared?: string[];
  };
  impact: {
    calls: number;
    errors: number;
    p50Ms: number;
    p95Ms: number;
    egressBytes: number;
    kvBytes: number;
    slow: number;
    eventDrops: number;
    restarts: number;
  };
  addedPermissions?: string[];
  changelog?: string;
}

interface CatalogueEntry {
  id: string;
  name: string;
  version: string;
  kind: string;
  category: string;
  summary: string;
  description?: string;
  author?: { name?: string; url?: string };
  repo?: string;
  icon?: string;
  verified: boolean;
  capabilities?: string[];
  permissions: string[];
  needsViewers?: boolean;
  needsFolders?: boolean;
  screenshots?: string[];
  limits?: string[];
  url: string;
  manifest: Record<string, unknown>;
}

interface Fixture {
  detail: PluginDetail;
  settings: Record<string, unknown>;
  schema: Record<string, unknown>;
  descriptors: Descriptor[];
  journal: Record<string, unknown>[];
  shadowJournal: Record<string, unknown>[];
  logs: string[];
  calls: Record<string, unknown>[];
}

const NOW = 1_762_000_000;

function catalogueFixtures(): CatalogueEntry[] {
  return [
    {
      id: "community.plutotv.ether",
      name: "Pluto TV",
      version: "1.3.0",
      kind: "livetv_source",
      category: "live_tv",
      summary: "Free Pluto TV channels with their guide",
      description: "Adds regional Pluto TV live channels and a six-day programme guide.",
      author: { name: "Ether Community", url: "https://github.com/ether-plugins/pluto" },
      repo: "https://github.com/ether-plugins/pluto",
      verified: true,
      capabilities: ["livetv_channels"],
      permissions: ["Connects to api.pluto.tv", "Connects to *.pluto.tv"],
      url: "https://plugins.ether.video/plutotv/manifest.json",
      manifest: {
        id: "community.plutotv.ether",
        version: "1.3.0",
        name: "Pluto TV",
        description: "Free Pluto TV channels with their guide.",
        ether: {
          protocol: 1,
          kind: "livetv_source",
          summary: "Free Pluto TV channels with their guide",
          author: { name: "Ether Community", url: "https://github.com/ether-plugins/pluto" },
          repo: "https://github.com/ether-plugins/pluto",
          capabilities: ["livetv_channels"],
          permissions: { network: ["api.pluto.tv", "*.pluto.tv"] },
          contributes: { channels: { refresh: "6h", test: true } },
        },
      },
    },
    {
      id: "community.subdl.subtitles",
      name: "SubDL Subtitles",
      version: "1.1.0",
      kind: "integration",
      category: "subtitles",
      summary: "Regional subtitles fetched on demand when picked",
      description: "Searches SubDL for subtitles in your preferred languages when a viewer asks.",
      author: { name: "Ether Community", url: "https://github.com/ether-plugins/subdl" },
      repo: "https://github.com/ether-plugins/subdl",
      verified: true,
      capabilities: ["subtitles"],
      permissions: ["Connects to api.subdl.com", "Stores an access token"],
      limits: ["Requires a free SubDL API key", "Rate limited to 60 lookups per minute"],
      url: "https://plugins.ether.video/subdl/manifest.json",
      manifest: {
        id: "community.subdl.subtitles",
        version: "1.1.0",
        name: "SubDL Subtitles",
        ether: {
          protocol: 1,
          kind: "integration",
          summary: "Regional subtitles fetched on demand when picked",
          author: { name: "Ether Community", url: "https://github.com/ether-plugins/subdl" },
          repo: "https://github.com/ether-plugins/subdl",
          capabilities: ["subtitles"],
          permissions: { network: ["api.subdl.com"], secrets: ["apiKey"] },
          contributes: {
            subtitles: { languages: "any", formats: ["srt", "vtt"] },
            integration: {
              category: "subtitles",
              adds: ["On-demand regional subtitles"],
              connect: "fields",
              test: true,
            },
          },
        },
      },
    },
    {
      id: "community.local.folders",
      name: "Local folders",
      version: "1.0.2",
      kind: "source",
      category: "sources",
      summary: "Plays media files from folders you pick on this server",
      description: "Scans folders you choose and matches files to titles in your catalogue.",
      author: { name: "Ether Community", url: "https://github.com/ether-plugins/local-folders" },
      repo: "https://github.com/ether-plugins/local-folders",
      verified: true,
      capabilities: ["stream_source", "ui"],
      permissions: ["Reads folders you choose", "Reads your catalogue"],
      needsFolders: true,
      url: "https://plugins.ether.video/local-folders/manifest.json",
      manifest: {
        id: "community.local.folders",
        version: "1.0.2",
        name: "Local folders",
        ether: {
          protocol: 1,
          kind: "source",
          summary: "Plays media files from folders you pick on this server",
          author: {
            name: "Ether Community",
            url: "https://github.com/ether-plugins/local-folders",
          },
          repo: "https://github.com/ether-plugins/local-folders",
          capabilities: ["stream_source", "ui"],
          permissions: { folders: "read", catalog: "read" },
          contributes: {
            source: { class: "direct", abilities: ["movies", "tv"] },
            pages: [{ key: "unmatched", label: "Unmatched files", route: "/ether/view/unmatched" }],
          },
        },
      },
    },
    {
      id: "community.homeassistant.notify",
      name: "Home Assistant",
      version: "0.9.0",
      kind: "notification_channel",
      category: "automation",
      summary: "Sends household alerts and playback events to Home Assistant",
      description: "Delivers playback and release events to your Home Assistant webhook.",
      author: { name: "Ether Community", url: "https://github.com/ether-plugins/homeassistant" },
      repo: "https://github.com/ether-plugins/homeassistant",
      verified: true,
      capabilities: ["notify", "events"],
      permissions: [
        "Connects to homeassistant.local",
        "Gets play and new-release events",
        "Stores an access token",
      ],
      url: "https://plugins.ether.video/homeassistant/manifest.json",
      manifest: {
        id: "community.homeassistant.notify",
        version: "0.9.0",
        name: "Home Assistant",
        ether: {
          protocol: 1,
          kind: "notification_channel",
          summary: "Sends household alerts and playback events to Home Assistant",
          capabilities: ["notify", "events"],
          permissions: { network: ["homeassistant.local"], secrets: ["webhookToken"] },
          contributes: {
            notification: { sendTest: true, formats: ["text"] },
            events: { subscribe: ["playback.started", "playback.stopped"] },
          },
        },
      },
    },
  ];
}

/** An installed Stremio add-on (no ether block) and its roles. */
function addonDetail(
  id: string,
  name: string,
  host: string,
  roles: { role: string; on: boolean }[],
): PluginDetail {
  return {
    id,
    name,
    version: "1.0.0",
    summary: `${name} streams and catalogs`,
    enabled: true,
    verified: false,
    shadow: false,
    autoUpdate: false,
    health: "ok",
    capabilities: roles.filter((r) => r.on).map((r) => r.role),
    sourceKind: "url",
    stremioOnly: true,
    installedAt: NOW - 86400 * 3,
    roles,
    host,
    manifest: { id: "org.example.addon", version: "1.0.0", name, types: ["movie", "series"] },
    permissions: {
      lines: ["Connects to the plugin address you gave"],
      declared: {},
      grants: {},
      needsViewers: false,
      needsFolders: false,
    },
    healthDetail: { status: "ok", restarts: 0, slow: 0, drops: 0, observed: {}, egressBytes: 0 },
    privacy: { declaredHosts: [host], observedHosts: { [host]: 12 } },
    impact: {
      calls: 12,
      errors: 0,
      p50Ms: 900,
      p95Ms: 2400,
      egressBytes: 0,
      kvBytes: 0,
      slow: 0,
      eventDrops: 0,
      restarts: 0,
    },
  };
}

function fixturePlugins(): Fixture[] {
  const pluto: PluginDetail = {
    id: "community.plutotv.ether",
    name: "Pluto TV",
    version: "1.2.0",
    prevVersion: "1.1.0",
    kind: "livetv_source",
    summary: "Free Pluto TV channels with their guide",
    enabled: true,
    verified: true,
    shadow: false,
    autoUpdate: true,
    updateVersion: "1.3.0",
    health: "ok",
    capabilities: ["livetv_channels"],
    sourceKind: "catalogue",
    installedAt: NOW - 86400 * 30,
    changelog: "Adds regional guide categories and faster channel refreshes.",
    manifest: {
      id: "community.plutotv.ether",
      version: "1.2.0",
      name: "Pluto TV",
      description: "Free Pluto TV channels with their guide.",
      types: ["tv"],
      ether: {
        protocol: 1,
        kind: "livetv_source",
        summary: "Free Pluto TV channels with their guide",
        author: { name: "Ether Community", url: "https://github.com/ether-plugins/pluto" },
        repo: "https://github.com/ether-plugins/pluto",
        license: "MIT",
        capabilities: ["livetv_channels"],
        permissions: { network: ["api.pluto.tv", "*.pluto.tv"], secrets: ["token"] },
        contributes: { channels: { refresh: "6h", test: true } },
      },
    },
    permissions: {
      lines: ["Connects to api.pluto.tv", "Connects to *.pluto.tv", "Stores an access token"],
      declared: { network: ["api.pluto.tv", "*.pluto.tv"], secrets: ["token"] },
      grants: {},
      needsViewers: false,
      needsFolders: false,
    },
    healthDetail: {
      status: "ok",
      restarts: 0,
      slow: 1,
      drops: 0,
      observed: { "api.pluto.tv": 42, "boot.pluto.tv": 6 },
      egressBytes: 1_200_000,
    },
    privacy: {
      declaredHosts: ["api.pluto.tv", "*.pluto.tv"],
      observedHosts: { "api.pluto.tv": 42, "boot.pluto.tv": 6 },
    },
    impact: {
      calls: 48,
      errors: 0,
      p50Ms: 180,
      p95Ms: 720,
      egressBytes: 1_200_000,
      kvBytes: 2048,
      slow: 1,
      eventDrops: 0,
      restarts: 0,
    },
    addedPermissions: ["Connects to ads.pluto.tv"],
  };
  const dub: PluginDetail = {
    id: "community.example.dub",
    name: "Dub language",
    version: "0.4.0",
    kind: "extension",
    summary: "Prefers releases dubbed in your language",
    enabled: true,
    verified: false,
    shadow: false,
    autoUpdate: false,
    health: "ok",
    capabilities: ["ranking_step", "rule_condition"],
    sourceKind: "url",
    installedAt: NOW - 86400 * 5,
    manifest: {
      id: "community.example.dub",
      version: "0.4.0",
      name: "Dub language",
      ether: {
        protocol: 1,
        kind: "extension",
        summary: "Prefers releases dubbed in your language",
        author: { name: "Alex Rivera", url: "https://github.com/example/dub" },
        repo: "https://github.com/example/dub",
        capabilities: ["ranking_step", "rule_condition"],
        runtime: { kind: "hosted", entry: "dist/plugin.js" },
        contributes: {
          rankingSteps: [{ key: "dub", label: "Dub language" }],
          ruleConditions: [{ key: "dubbed", label: "Dubbed" }],
        },
      },
    },
    permissions: {
      lines: ["Needs no access to your server or the network"],
      declared: {},
      grants: {},
      needsViewers: false,
      needsFolders: false,
    },
    healthDetail: { status: "ok", restarts: 0, slow: 0, drops: 0, observed: {}, egressBytes: 0 },
    privacy: { declaredHosts: [], observedHosts: {} },
    impact: {
      calls: 200,
      errors: 0,
      p50Ms: 1,
      p95Ms: 2,
      egressBytes: 0,
      kvBytes: 0,
      slow: 0,
      eventDrops: 0,
      restarts: 0,
    },
  };
  const wrapped: PluginDetail = {
    id: "community.example.wrapped",
    name: "Ether Wrapped",
    version: "1.0.1",
    prevVersion: "1.0.0",
    kind: "extension",
    summary: "Your year in viewing, with a shelf to match",
    enabled: true,
    verified: true,
    shadow: true,
    autoUpdate: false,
    health: "attention",
    capabilities: ["ui", "list_source", "events"],
    sourceKind: "catalogue",
    installedAt: NOW - 86400 * 12,
    manifest: {
      id: "community.example.wrapped",
      version: "1.0.1",
      name: "Ether Wrapped",
      ether: {
        protocol: 1,
        kind: "extension",
        summary: "Your year in viewing, with a shelf to match",
        author: { name: "Ether Community", url: "https://github.com/ether-plugins/wrapped" },
        repo: "https://github.com/ether-plugins/wrapped",
        capabilities: ["ui", "list_source", "events"],
        permissions: {
          catalog: "read",
          viewers: { watch_history: "granted_viewers" },
          collections: "write",
        },
        contributes: {
          pages: [{ key: "wrapped", label: "Ether Wrapped", route: "/ether/view/wrapped" }],
          listSources: [
            {
              key: "wrapped",
              label: "Your year in viewing",
              catalog: "wrapped",
              maxItems: 100,
              refresh: "24h",
            },
          ],
          events: { subscribe: ["playback.completed"] },
        },
      },
    },
    permissions: {
      lines: [
        "Reads your catalogue",
        "Reads watch history",
        "Creates and updates collections",
        "Gets play and new-release events",
      ],
      declared: {
        catalog: "read",
        viewers: { watch_history: "granted_viewers" },
        collections: "write",
      },
      grants: { viewers: [1] },
      needsViewers: true,
      needsFolders: false,
    },
    healthDetail: {
      status: "attention",
      reason: "Recent calls were slow.",
      lastError: "GET /ether/view/wrapped took 1900 ms",
      restarts: 1,
      slow: 3,
      drops: 2,
      observed: {},
      egressBytes: 0,
    },
    privacy: { declaredHosts: [], observedHosts: {} },
    impact: {
      calls: 31,
      errors: 1,
      p50Ms: 240,
      p95Ms: 1900,
      egressBytes: 0,
      kvBytes: 8192,
      slow: 3,
      eventDrops: 2,
      restarts: 1,
    },
  };
  const text = (plugin: string, s: string) => ({ resolved: s, plugin });
  const streamAddon: Fixture = {
    detail: addonDetail("addon.mockstreams", "Stream Addon", "addon.example", [
      { role: "stream_source", on: true },
      { role: "list_source", on: false },
    ]),
    settings: {},
    schema: {},
    descriptors: [],
    journal: [],
    shadowJournal: [],
    logs: [],
    calls: [],
  } as unknown as Fixture;
  return [
    streamAddon,
    {
      detail: pluto,
      settings: { region: "us", token: "Saved" },
      schema: {
        fields: [
          { key: "region", kind: "select", label: "Region", required: true },
          {
            key: "token",
            kind: "secret",
            label: "Access token",
            hint: "Optional token for regional channels.",
          },
        ],
      },
      descriptors: [
        {
          kind: "liveTVSource",
          id: "plugin:community.plutotv.ether/channels",
          owner: pluto.id,
          label: text(pluto.id, "Pluto TV"),
          meta: { refresh: "6h" },
        },
      ],
      journal: [],
      shadowJournal: [],
      logs: ["info: refreshed 214 channels", "info: guide covers 6 days"],
      calls: [
        {
          id: 2,
          plugin: pluto.id,
          route: "/ether/health",
          method: "GET",
          status: 200,
          ms: 120,
          created: "2026-11-01T10:00:00Z",
        },
        {
          id: 1,
          plugin: pluto.id,
          route: "/ether/channels.json",
          method: "GET",
          status: 200,
          ms: 640,
          created: "2026-11-01T06:00:00Z",
        },
      ],
    },
    {
      detail: dub,
      settings: { language: "fr" },
      schema: {
        fields: [{ key: "language", kind: "language", label: "Dub language", required: true }],
      },
      descriptors: [
        {
          kind: "rankingStep",
          id: "plugin:community.example.dub/dub",
          owner: dub.id,
          label: text(dub.id, "Dub language"),
          meta: {},
        },
        {
          kind: "ruleField",
          id: "plugin:community.example.dub/dubbed",
          owner: dub.id,
          label: text(dub.id, "Dubbed"),
          meta: {},
        },
      ],
      journal: [],
      shadowJournal: [],
      logs: ["info: loaded ranking step dub"],
      calls: [],
    },
    {
      detail: wrapped,
      settings: {},
      schema: {},
      descriptors: [
        // Fixture plugin contributions exercise every native console slot.
        ...[
          "title.panel",
          "viewer.tab",
          "dashboard.panel",
          "actionMenu.title",
          "actionMenu.viewer",
          "actionMenu.collection",
          "actionMenu.release",
        ].map((slot) => ({
          kind: "slot" as const,
          id: `plugin:${wrapped.id}/fixture-${slot}`,
          owner: wrapped.id,
          group: wrapped.name,
          label: text(wrapped.id, `Fixture ${slot}`),
          meta: {
            slot,
            key: `fixture-${slot}`,
            route: slot.startsWith("actionMenu.") ? undefined : "/ether/view/fixture",
            action: slot.startsWith("actionMenu.") ? "fixture" : undefined,
          },
        })),
        {
          kind: "page",
          id: "plugin:community.example.wrapped/wrapped",
          owner: wrapped.id,
          label: text(wrapped.id, "Ether Wrapped"),
          meta: { route: "/ether/view/wrapped" },
        },
        {
          kind: "collectionSource",
          id: "plugin:community.example.wrapped/wrapped",
          owner: wrapped.id,
          label: text(wrapped.id, "Your year in viewing"),
          meta: { catalog: "wrapped", maxItems: 100 },
        },
      ],
      journal: [
        {
          id: 1,
          plugin: wrapped.id,
          op: "collections.upsert",
          detail: "Your year in viewing",
          shadow: false,
          undone: false,
          created: "2026-10-30T09:00:00Z",
        },
      ],
      shadowJournal: [
        {
          id: 2,
          plugin: wrapped.id,
          op: "collections.upsert",
          detail: "Your year in viewing",
          shadow: true,
          undone: false,
          created: "2026-11-01T09:00:00Z",
        },
      ],
      logs: ["info: built the year shelf with 48 titles", "warn: poster lookup was slow"],
      calls: [
        {
          id: 1,
          plugin: wrapped.id,
          route: "/ether/view/wrapped",
          method: "GET",
          status: 200,
          ms: 1900,
          error: "",
          created: "2026-11-01T09:30:00Z",
        },
      ],
    },
  ];
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function summary(d: PluginDetail): PluginSummary {
  const {
    manifest: _m,
    permissions: _p,
    healthDetail,
    privacy: _pr,
    impact: _i,
    addedPermissions: _a,
    changelog: _c,
    ...s
  } = d;
  return { ...s, health: healthDetail.status };
}

function descriptorsFromManifest(
  id: string,
  name: string,
  manifest: Record<string, unknown>,
): Descriptor[] {
  const text = (s: string) => ({ resolved: s, plugin: id });
  const ether = (manifest.ether ?? {}) as {
    kind?: string;
    capabilities?: string[];
    contributes?: {
      pages?: { key: string; label: string; route: string }[];
      slots?: { key: string; label: string; slot: string; route?: string; action?: string }[];
      listSources?: { key: string; label: string }[];
      rankingSteps?: { key: string; label: string }[];
      ruleConditions?: { key: string; label: string }[];
    };
  };
  const out: Descriptor[] = [];
  for (const p of ether.contributes?.pages ?? []) {
    out.push({
      kind: "page",
      id: `plugin:${id}/${p.key}`,
      owner: id,
      label: text(p.label),
      meta: { route: p.route },
    });
  }
  for (const slot of ether.contributes?.slots ?? []) {
    out.push({
      kind: "slot",
      id: `plugin:${id}/${slot.key}`,
      owner: id,
      group: name,
      label: text(slot.label),
      meta: { slot: slot.slot, key: slot.key, route: slot.route, action: slot.action },
    });
  }
  for (const ls of ether.contributes?.listSources ?? []) {
    out.push({
      kind: "collectionSource",
      id: `plugin:${id}/${ls.key}`,
      owner: id,
      label: text(ls.label),
      meta: {},
    });
  }
  for (const rs of ether.contributes?.rankingSteps ?? []) {
    out.push({
      kind: "rankingStep",
      id: `plugin:${id}/${rs.key}`,
      owner: id,
      label: text(rs.label),
      meta: {},
    });
  }
  if (ether.capabilities?.includes("livetv_channels")) {
    out.push({
      kind: "liveTVSource",
      id: `plugin:${id}/channels`,
      owner: id,
      label: text(name),
      meta: {},
    });
  }
  if (ether.capabilities?.includes("subtitles")) {
    out.push({
      kind: "subtitleProvider",
      id: `plugin:${id}/subtitles`,
      owner: id,
      label: text(name),
      meta: {},
    });
  }
  if (ether.capabilities?.includes("stream_source")) {
    out.push({
      kind: "sourceKind",
      id: `plugin:${id}/stream`,
      owner: id,
      label: text(name),
      meta: {},
    });
  }
  if (ether.capabilities?.includes("notify")) {
    out.push({
      kind: "channelKind",
      id: `plugin:${id}/notify`,
      owner: id,
      label: text(name),
      meta: {},
    });
  }
  if (out.length === 0) {
    out.push({
      kind: "page",
      id: `plugin:${id}/home`,
      owner: id,
      label: text(name),
      meta: { route: "/ether/view/home" },
    });
  }
  return out;
}

export function createPluginsMock(): {
  handle: ExtendHandler;
  descriptors: () => Descriptor[];
  version: () => number;
} {
  let version = 1;
  const fixtures = fixturePlugins();
  const pairings = new Map<
    string,
    { id: string; created: number; lastUsed: number; expires: number }[]
  >();
  const catalogue = catalogueFixtures();
  const bump = () => version++;
  const byId = (id: string) => fixtures.find((f) => f.detail.id === id);
  const studio = createStudioMock(() => bump());

  function buildPreview(body: unknown) {
    const req = (body && typeof body === "object" ? body : {}) as {
      url?: string;
      kind?: string;
      manifest?: Record<string, unknown>;
    };
    const rawUrl = (req.url ?? "").trim();
    const catMatch = catalogue.find((c) => c.url === rawUrl || c.id === rawUrl);
    if (catMatch) {
      const existing = byId(catMatch.id);
      return {
        manifest: catMatch.manifest,
        permissions: catMatch.permissions,
        needsViewers: Boolean(catMatch.needsViewers),
        needsFolders: Boolean(catMatch.needsFolders),
        verified: catMatch.verified,
        stremioOnly: false,
        alreadyInstalled: Boolean(existing),
        installedVersion: existing?.detail.version,
      };
    }
    if (!rawUrl && !req.manifest) {
      // Uploaded .etherpkg binary body
      const man = {
        id: "community.example.pkg",
        version: "1.0.0",
        name: "Packaged plugin",
        ether: {
          protocol: 1,
          kind: "extension",
          summary: "Installed from a package file",
          capabilities: ["ui"],
          contributes: {
            pages: [{ key: "home", label: "Packaged plugin", route: "/ether/view/home" }],
          },
        },
      };
      return {
        manifest: man,
        permissions: ["Needs no access to your server or the network"],
        needsViewers: false,
        needsFolders: false,
        verified: false,
        stremioOnly: false,
      };
    }
    const isFolderUrl = rawUrl.includes("folder");
    const isViewerUrl = rawUrl.includes("viewer");
    const host = safeHost(rawUrl);
    const permissions = [`Connects to ${host}`];
    if (isViewerUrl) permissions.push("Reads watch history");
    if (isFolderUrl) permissions.push("Reads folders you choose");
    const man = req.manifest ?? {
      id: "community.example.preview",
      version: "1.0.0",
      name: "Preview plugin",
      ether: {
        protocol: 1,
        kind: "extension",
        summary: "A plugin from a link",
        author: { name: "Community author", url: rawUrl },
        capabilities: ["ui"],
        permissions: {
          network: [host],
          ...(isViewerUrl ? { viewers: { watch_history: "granted_viewers" } } : {}),
          ...(isFolderUrl ? { folders: "read" } : {}),
        },
        contributes: {
          pages: [{ key: "home", label: "Preview plugin", route: "/ether/view/home" }],
        },
      },
    };
    return {
      manifest: man,
      permissions,
      needsViewers: isViewerUrl,
      needsFolders: isFolderUrl,
      verified: false,
      stremioOnly: rawUrl.includes("stremio"),
    };
  }

  const handle: ExtendHandler = (method, seg, q, body) => {
    if (seg[0] !== "plugins") return null;
    const studioResp = studio.handle(method, seg, q, body);
    if (studioResp) return studioResp;
    if (method === "GET" && seg.length === 1) {
      return json({ plugins: fixtures.map((f) => summary(f.detail)) });
    }
    if (method === "GET" && seg[1] === "catalogue" && seg.length === 2) {
      if (q.get("empty") === "1") return json({ entries: [] });
      return json({ entries: catalogue });
    }
    if (method === "POST" && seg[1] === "addons" && seg[2] === "preview") {
      const url = String((body as { url?: string })?.url ?? "").trim();
      if (!/^(https?|stremio):\/\//.test(url)) {
        return json(
          { error: { code: "invalid", message: "That is not a plugin install link." } },
          400,
        );
      }
      const host = safeHost(url.replace(/^stremio:/, "https:"));
      const id = `addon.${host.replace(/[^a-z0-9]/gi, "")}`;
      const existing = byId(id);
      return json({
        id,
        name: existing?.detail.name ?? (host.split(".")[0] || "Plugin"),
        description: "Streams, catalogs and subtitles from one plugin.",
        version: "1.0.0",
        host,
        alreadyInstalled: Boolean(existing),
        roles: existing?.detail.roles ?? [
          { role: "stream_source", on: true },
          { role: "list_source", on: true },
          { role: "subtitles", on: true },
        ],
        catalogs: [
          { key: "catalog-top", type: "movie", id: "top", name: "Popular" },
          { key: "catalog-new", type: "series", id: "new", name: "New series" },
        ],
      });
    }
    if (method === "POST" && seg[1] === "addons" && seg.length === 2) {
      const req = (body ?? {}) as { url?: string; roles?: string[] };
      const host = safeHost(String(req.url ?? "").replace(/^stremio:/, "https:"));
      const id = `addon.${host.replace(/[^a-z0-9]/gi, "")}`;
      let f = byId(id);
      const offered = ["stream_source", "list_source", "subtitles"];
      if (!f) {
        f = {
          detail: addonDetail(
            id,
            host.split(".")[0] || "Plugin",
            host,
            offered.map((role) => ({ role, on: (req.roles ?? offered).includes(role) })),
          ),
          settings: {},
          schema: {},
          descriptors: [],
          journal: [],
          shadowJournal: [],
          logs: [],
          calls: [],
        } as unknown as Fixture;
        fixtures.push(f);
      } else {
        f.detail.roles = (f.detail.roles ?? []).map((r) => ({
          ...r,
          on: r.on || (req.roles ?? []).includes(r.role),
        }));
      }
      bump();
      return json(summary(f.detail));
    }
    if (method === "PUT" && seg[2] === "roles") {
      const f = byId(seg[1] ?? "");
      if (!f) return json({ error: { code: "not_found", message: "not found" } }, 404);
      const off = ((body ?? {}) as { off?: string[] }).off ?? [];
      f.detail.roles = (f.detail.roles ?? []).map((r) => ({ ...r, on: !off.includes(r.role) }));
      f.detail.capabilities = f.detail.roles.filter((r) => r.on).map((r) => r.role);
      bump();
      return json({ roles: f.detail.roles });
    }
    if (method === "POST" && seg[1] === "install" && seg[2] === "preview") {
      const req = (body && typeof body === "object" ? body : {}) as { url?: string };
      if (req.url === "invalid") {
        return json(
          {
            error: {
              code: "validation",
              message: "Could not read a plugin manifest from that address.",
              fields: [{ field: "url", message: "Enter a valid manifest or repository URL." }],
            },
          },
          400,
        );
      }
      return json(buildPreview(body));
    }
    if (method === "POST" && seg[1] === "install" && seg.length === 2) {
      const options =
        body instanceof FormData ? JSON.parse(String(body.get("options") ?? "{}")) : body;
      const req = (options && typeof options === "object" ? options : {}) as {
        url?: string;
        kind?: string;
        lan?: boolean;
        grants?: PluginDetail["permissions"]["grants"];
        settings?: Record<string, unknown>;
        autoUpdate?: boolean;
        shadow?: boolean;
      };
      if (req.url === "invalid") {
        return json(
          {
            error: {
              code: "validation",
              message: "Preview a plugin first, then confirm.",
              fields: [],
            },
          },
          400,
        );
      }
      const prev = buildPreview(body);
      const man = prev.manifest as {
        id: string;
        version: string;
        name: string;
        ether?: {
          kind?: string;
          summary?: string;
          icon?: string;
          capabilities?: string[];
          permissions?: Record<string, unknown>;
        };
      };
      const id = man.id || "community.example.preview";
      const name = man.name || "Preview plugin";
      const declared = (man.ether?.permissions ?? {}) as { network?: string[] };
      const detail: PluginDetail = {
        id,
        name,
        version: man.version || "1.0.0",
        kind: man.ether?.kind ?? "extension",
        summary: man.ether?.summary ?? "A plugin from a link",
        icon: man.ether?.icon,
        enabled: true,
        verified: prev.verified,
        shadow: Boolean(req.shadow),
        autoUpdate: Boolean(req.autoUpdate),
        health: "ok",
        capabilities: man.ether?.capabilities ?? ["ui"],
        sourceKind: req.kind || (req.url ? "url" : "pkg"),
        stremioOnly: prev.stremioOnly,
        installedAt: NOW,
        manifest: prev.manifest,
        permissions: {
          lines: prev.permissions,
          declared: man.ether?.permissions ?? {},
          grants: req.grants ?? {},
          needsViewers: prev.needsViewers,
          needsFolders: prev.needsFolders,
        },
        healthDetail: {
          status: "ok",
          restarts: 0,
          slow: 0,
          drops: 0,
          observed: {},
          egressBytes: 0,
        },
        privacy: {
          declaredHosts: declared.network ?? [],
          observedHosts: {},
        },
        impact: {
          calls: 0,
          errors: 0,
          p50Ms: 0,
          p95Ms: 0,
          egressBytes: 0,
          kvBytes: 0,
          slow: 0,
          eventDrops: 0,
          restarts: 0,
        },
      };
      const existingIdx = fixtures.findIndex((f) => f.detail.id === id);
      const newFixture: Fixture = {
        detail,
        settings: req.settings ?? {},
        schema: {},
        descriptors: descriptorsFromManifest(id, name, prev.manifest),
        journal: [],
        shadowJournal: [],
        logs: [`info: installed ${name} ${detail.version}`],
        calls: [],
      };
      if (existingIdx >= 0) {
        fixtures[existingIdx] = newFixture;
      } else {
        fixtures.push(newFixture);
      }
      bump();
      return json(summary(detail), 201);
    }
    const f = byId(seg[1] ?? "");
    if (!f) {
      return json({ error: { code: "not_found", message: "That plugin is not installed." } }, 404);
    }
    const d = f.detail;
    if (method === "POST" && seg[2] === "pair") {
      return json({
        code: "PAIR-1234",
        expiresAt: new Date(Date.now() + 600000).toISOString(),
        tunnelUrl: "wss://ether.example.test/tunnel",
        exchangePath: "/api/plugins/pair/exchange",
      });
    }
    if (method === "GET" && seg[2] === "pairings")
      return json({ pairings: pairings.get(d.id) ?? [] });
    if (method === "DELETE" && seg[2] === "pairings" && seg[3]) {
      pairings.set(
        d.id,
        (pairings.get(d.id) ?? []).filter((p) => p.id !== seg[3]),
      );
      return json({ status: "revoked" });
    }
    if (method === "GET" && seg.length === 2) return json(d);
    if (method === "GET" && seg[2] === "settings") {
      return json({ settings: f.settings, schema: f.schema });
    }
    if (method === "PUT" && seg[2] === "settings") {
      const b = (body ?? {}) as { settings?: Record<string, unknown> };
      const incoming = b.settings ?? {};
      const schemaFields =
        (f.schema as { fields?: { key: string; kind?: string; required?: boolean }[] }).fields ??
        [];
      const fieldErrors: { field: string; message: string }[] = [];
      for (const sf of schemaFields) {
        const val = incoming[sf.key];
        if (
          (sf.required && (val === "" || val === undefined || val === null)) ||
          val === "invalid"
        ) {
          fieldErrors.push({
            field: `/${sf.key}`,
            message: val === "invalid" ? "This value is not supported." : "is required",
          });
        }
      }
      for (const [k, v] of Object.entries(incoming)) {
        if (v === "invalid" && !fieldErrors.some((e) => e.field === `/${k}`)) {
          fieldErrors.push({ field: `/${k}`, message: "This value is not supported." });
        }
      }
      if (fieldErrors.length > 0) {
        return json(
          {
            error: {
              code: "validation",
              message: "The plugin did not accept these settings.",
              fields: fieldErrors,
            },
          },
          400,
        );
      }
      const nextSettings: Record<string, unknown> = { ...incoming };
      for (const sf of schemaFields) {
        if (sf.kind === "secret" && nextSettings[sf.key] && nextSettings[sf.key] !== "") {
          nextSettings[sf.key] = "Saved";
        }
      }
      f.settings = nextSettings;
      return json({ status: "saved" });
    }
    if (method === "GET" && seg[2] === "options") {
      return json({
        options: [
          { value: "us", label: "United States" },
          { value: "ca", label: "Canada" },
          { value: "uk", label: "United Kingdom" },
        ],
      });
    }
    if (method === "POST" && seg[2] === "test") {
      return json({ ok: d.enabled, message: d.enabled ? "Works" : "Turned off", details: [] });
    }
    if (method === "POST" && seg[2] === "enable") {
      d.enabled = true;
      bump();
      return json({ status: "enabled" });
    }
    if (method === "POST" && seg[2] === "disable") {
      d.enabled = false;
      bump();
      return json({ status: "disabled" });
    }
    if (method === "PUT" && seg[2] === "grants") {
      d.permissions.grants = (body ?? {}) as PluginDetail["permissions"]["grants"];
      return json({ status: "saved", grants: d.permissions.grants });
    }
    if (method === "POST" && seg[2] === "shadow") {
      d.shadow = Boolean((body as { shadow?: boolean } | null)?.shadow);
      return json({ shadow: d.shadow });
    }
    if (method === "GET" && seg[2] === "shadow") {
      return json({ entries: f.shadowJournal, count: f.shadowJournal.length });
    }
    if (method === "GET" && seg[2] === "auto-update") return json({ autoUpdate: d.autoUpdate });
    if (method === "PUT" && seg[2] === "auto-update") {
      d.autoUpdate = Boolean((body as { autoUpdate?: boolean } | null)?.autoUpdate);
      return json({ autoUpdate: d.autoUpdate });
    }
    if (method === "GET" && seg[2] === "journal") {
      const entries = q.get("shadow") === "1" ? f.shadowJournal : f.journal;
      return json({ entries });
    }
    if (method === "POST" && seg[2] === "undo") {
      const live = f.journal.filter((e) => !e.undone);
      for (const e of live) e.undone = true;
      return json({ undone: live.length });
    }
    if (method === "GET" && seg[2] === "logs") return json({ lines: f.logs });
    if (method === "GET" && seg[2] === "calls") return json({ calls: f.calls });
    if (method === "POST" && seg[2] === "update" && seg[3] === "check") {
      return json({
        updateAvailable: Boolean(d.updateVersion),
        version: d.updateVersion ?? d.version,
        addedPermissions: d.addedPermissions ?? [],
      });
    }
    if (method === "POST" && seg[2] === "update" && seg.length === 3) {
      const accept = Boolean(
        (body as { acceptAddedPermissions?: boolean } | null)?.acceptAddedPermissions,
      );
      if (!d.updateVersion) {
        return json({ error: { code: "error", message: "No update is waiting." } }, 400);
      }
      if ((d.addedPermissions?.length ?? 0) > 0 && !accept) {
        return json(
          {
            error: {
              code: "permission_increase",
              message: "This update asks for more access. Review it, then confirm.",
              fields: (d.addedPermissions ?? []).map((a) => ({ field: "permissions", message: a })),
            },
          },
          409,
        );
      }
      d.prevVersion = d.version;
      d.version = d.updateVersion;
      d.updateVersion = undefined;
      d.addedPermissions = undefined;
      bump();
      return json(summary(d));
    }
    if (method === "POST" && seg[2] === "rollback") {
      if (!d.prevVersion) {
        return json(
          { error: { code: "error", message: "There is no earlier version to go back to." } },
          400,
        );
      }
      d.version = d.prevVersion;
      d.prevVersion = undefined;
      bump();
      return json(summary(d));
    }
    if (method === "DELETE" && seg.length === 2) {
      if (q.get("undo") === "1") {
        f.journal = [];
      }
      fixtures.splice(fixtures.indexOf(f), 1);
      bump();
      return json({ status: "removed" });
    }
    if (method === "GET" && seg[2] === "views") {
      return json({
        spec: {
          specVersion: 1,
          layout: "bare",
          header: [],
          body: [{ kind: "text", content: "Fixture plugin content" }],
        },
        data: {},
      });
    }
    if (method === "POST" && seg[2] === "actions") {
      return json({ toast: "Done" });
    }
    return null;
  };

  return {
    handle,
    descriptors: () => fixtures.filter((f) => f.detail.enabled).flatMap((f) => f.descriptors),
    version: () => version,
  };
}

function safeHost(url: string): string {
  try {
    return new URL(url).hostname || "the address you gave";
  } catch {
    return "the address you gave";
  }
}
