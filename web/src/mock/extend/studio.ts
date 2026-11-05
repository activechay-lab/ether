import type { ExtendHandler } from "./index";
import type {
  StudioCallRecord,
  StudioCheckReport,
  StudioDiagnostic,
  StudioJournalEntry,
  StudioProject,
  StudioTemplate,
  StudioValidationHint,
} from "../../pages/plugins/studio-types";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

const TEMPLATES: StudioTemplate[] = [
  {
    id: "stremio_remix",
    label: "From a Stremio link",
    summary: "Install a Stremio plugin directly, or create a remix with settings and a page.",
    kind: "extension",
    capabilities: ["list_source", "ui"],
    defaultPluginId: "community.studio.remix",
    defaultName: "Stremio remix",
    defaultRoute: "/catalog/movie/remix_top.json",
    defaultPage: "remix",
    supportsStremio: true,
    files: {
      "manifest.json": JSON.stringify(
        {
          id: "community.studio.remix",
          version: "0.1.0",
          name: "Stremio remix",
          ether: {
            protocol: 1,
            kind: "extension",
            capabilities: ["list_source", "ui"],
            summary: "$t:summary",
            runtime: { kind: "hosted", entry: "src/plugin.ts" },
            strings: { en: "i18n/en.json" },
          },
        },
        null,
        2,
      ),
      "i18n/en.json": JSON.stringify(
        { summary: "Wraps a Stremio plugin with settings and a native status page." },
        null,
        2,
      ),
      "src/plugin.ts": `export function handle(req: { path: string }) {\n  return { status: 200, body: { metas: [{ id: "tt0133093", name: "The Matrix" }] } };\n}\n`,
    },
  },
  {
    id: "channels",
    label: "Channels",
    summary: "Add live channels with numbers, logos and a programme guide to Live TV.",
    kind: "livetv_source",
    capabilities: ["livetv_channels", "ui"],
    defaultPluginId: "community.studio.channels",
    defaultName: "Community channels",
    defaultRoute: "/ether/channels.json",
    defaultPage: "lineup",
    files: {
      "manifest.json": JSON.stringify(
        {
          id: "community.studio.channels",
          version: "0.1.0",
          name: "Community channels",
          ether: {
            protocol: 1,
            kind: "livetv_source",
            capabilities: ["livetv_channels", "ui"],
            summary: "$t:summary",
            runtime: { kind: "hosted", entry: "src/plugin.ts" },
            permissions: { network: ["api.pluto.tv"] },
            strings: { en: "i18n/en.json" },
            contributes: {
              channels: { refresh: "6h", test: true },
              pages: [{ key: "lineup", label: "Channel lineup", route: "/ether/view/lineup" }],
            },
          },
        },
        null,
        2,
      ),
      "i18n/en.json": JSON.stringify(
        { summary: "Live channels and six-day programme guide for Live TV." },
        null,
        2,
      ),
      "src/plugin.ts": `export function handle(req: { path: string }) {\n  if (req.path === "/ether/channels.json") {\n    return { status: 200, body: { channels: [{ id: "ch-1", number: "101", name: "Ether Cinema" }] } };\n  }\n  return { status: 200, body: { status: "ok" } };\n}\n`,
    },
  },
  {
    id: "local_files",
    label: "Local files",
    summary: "Attach files from owner-picked folders to catalogue titles as direct releases.",
    kind: "source",
    capabilities: ["stream_source", "ui"],
    defaultPluginId: "community.studio.localfiles",
    defaultName: "Local folders",
    defaultRoute: "/stream/movie/tt0133093.json",
    defaultPage: "folders",
    files: {
      "manifest.json": JSON.stringify(
        {
          id: "community.studio.localfiles",
          version: "0.1.0",
          name: "Local folders",
          ether: {
            protocol: 1,
            kind: "source",
            capabilities: ["stream_source", "ui"],
            summary: "Attaches files in folders you choose as direct releases on catalogue titles.",
            runtime: { kind: "hosted", entry: "src/plugin.ts" },
          },
        },
        null,
        2,
      ),
      "src/plugin.ts": `export function handle(req: { path: string }) {\n  return { status: 200, body: { streams: [] } };\n}\n`,
    },
  },
  {
    id: "list",
    label: "List",
    summary: "Feed a list of IMDb or TMDB titles into Collection Studio and Home rows.",
    kind: "extension",
    capabilities: ["list_source", "ui"],
    defaultPluginId: "community.studio.list",
    defaultName: "Festival winners",
    defaultRoute: "/catalog/movie/festival_picks.json",
    defaultPage: "shelf",
    files: {
      "manifest.json": JSON.stringify(
        {
          id: "community.studio.list",
          version: "0.1.0",
          name: "Festival winners",
          ether: {
            protocol: 1,
            kind: "extension",
            capabilities: ["list_source", "ui"],
            summary: "Curated festival winners for Collection Studio and Home shelves.",
            runtime: { kind: "hosted", entry: "src/plugin.ts" },
          },
        },
        null,
        2,
      ),
      "src/plugin.ts": `export function handle(req: { path: string }) {\n  return { status: 200, body: { metas: [{ id: "tt6751668", name: "Parasite" }] } };\n}\n`,
    },
  },
  {
    id: "notification",
    label: "Notification channel",
    summary: "Deliver server alerts and digests to a custom webhook or messaging service.",
    kind: "notification_channel",
    capabilities: ["notify"],
    defaultPluginId: "community.studio.notify",
    defaultName: "Custom webhook",
    defaultRoute: "/ether/notify",
    files: {
      "manifest.json": JSON.stringify(
        {
          id: "community.studio.notify",
          version: "0.1.0",
          name: "Custom webhook",
          ether: {
            protocol: 1,
            kind: "notification_channel",
            capabilities: ["notify"],
            summary: "Sends Ether alerts and digests to your custom webhook.",
            runtime: { kind: "hosted", entry: "src/plugin.ts" },
          },
        },
        null,
        2,
      ),
      "src/plugin.ts": `export function handle(req: { path: string }) {\n  return { status: 200, body: { delivered: true } };\n}\n`,
    },
  },
  {
    id: "ranking",
    label: "Ranking step",
    summary:
      "Score release candidates in-process during profile ranking with a fast TypeScript function.",
    kind: "extension",
    capabilities: ["ranking_step"],
    defaultPluginId: "community.studio.ranking",
    defaultName: "Language release boost",
    defaultRoute: "hot:rank_key",
    files: {
      "manifest.json": JSON.stringify(
        {
          id: "community.studio.ranking",
          version: "0.1.0",
          name: "Language release boost",
          ether: {
            protocol: 1,
            kind: "extension",
            capabilities: ["ranking_step"],
            summary: "Boosts release candidates that carry your preferred audio track.",
            runtime: { kind: "hosted", entry: "src/plugin.ts" },
          },
        },
        null,
        2,
      ),
      "src/plugin.ts": `export function rank_key(candidate: { languages?: string[] }): number {\n  return (candidate.languages || []).includes("fr") ? 500 : 0;\n}\n`,
    },
  },
  {
    id: "page",
    label: "Page",
    summary: "Build a native ViewSpec v1 console page or attach a panel to a console slot.",
    kind: "extension",
    capabilities: ["ui"],
    defaultPluginId: "community.studio.page",
    defaultName: "Curator notes",
    defaultRoute: "/ether/view/overview",
    defaultPage: "overview",
    supportsSlot: true,
    files: {
      "manifest.json": JSON.stringify(
        {
          id: "community.studio.page",
          version: "0.1.0",
          name: "Curator notes",
          ether: {
            protocol: 1,
            kind: "extension",
            capabilities: ["ui"],
            summary: "Adds a native ViewSpec v1 page and slot panel to the console.",
            runtime: { kind: "hosted", entry: "src/plugin.ts" },
          },
        },
        null,
        2,
      ),
      "src/plugin.ts": `export function handle(req: { path: string }) {\n  return { status: 200, body: { title: "Curator notes" } };\n}\n`,
    },
  },
  {
    id: "tool",
    label: "Agent tool",
    summary: "Add a read or proposal tool to Ask Ether and paired MCP clients.",
    kind: "extension",
    capabilities: ["tools"],
    defaultPluginId: "community.studio.tool",
    defaultName: "Room lights tool",
    defaultRoute: "/ether/tool/check_room_lights",
    files: {
      "manifest.json": JSON.stringify(
        {
          id: "community.studio.tool",
          version: "0.1.0",
          name: "Room lights tool",
          ether: {
            protocol: 1,
            kind: "extension",
            capabilities: ["tools"],
            summary: "Adds a theatre room lighting check tool to Ask Ether.",
            runtime: { kind: "hosted", entry: "src/plugin.ts" },
          },
        },
        null,
        2,
      ),
      "src/plugin.ts": `export function handle(req: { path: string }) {\n  return { status: 200, body: { scene: "Cinema dim" } };\n}\n`,
    },
  },
];

interface StoredProject {
  project: StudioProject;
  calls: StudioCallRecord[];
  hints: StudioValidationHint[];
  journal: StudioJournalEntry[];
  logs: string[];
}

function defaultViewSpec(title: string) {
  return {
    title,
    spec: {
      specVersion: 1,
      layout: "detail",
      header: [
        {
          kind: "page-header",
          title: { $: "title" },
          description: { $: "subtitle" },
        },
      ],
      body: [
        {
          kind: "stat-tile",
          iconKey: "tv",
          accent: "primary",
          label: "Active channels",
          value: { $: "channelCount" },
        },
        {
          kind: "panel",
          title: "Preview output",
          blocks: [
            {
              kind: "text",
              content: { $: "note" },
            },
          ],
        },
      ],
    },
    data: {
      title,
      subtitle: "Live ViewSpec v1 preview from the Studio sandbox.",
      channelCount: "3",
      note: "All channels have numbers, logos and guide data.",
    },
  };
}

function lintFiles(files: Record<string, string>): StudioDiagnostic[] {
  const diags: StudioDiagnostic[] = [];
  const ts = files["src/plugin.ts"] ?? "";
  const lines = ts.split("\n");
  lines.forEach((line, idx) => {
    if (line.includes("SYNTAX_ERROR") || /=\s*;/.test(line)) {
      diags.push({
        file: "src/plugin.ts",
        line: idx + 1,
        column: Math.max(1, line.indexOf(";") + 1),
        text: 'Unexpected ";"',
        severity: "error",
      });
    }
  });
  const man = files["manifest.json"];
  if (man) {
    try {
      JSON.parse(man);
    } catch (e) {
      diags.push({
        file: "manifest.json",
        line: 1,
        column: 1,
        text: e instanceof Error ? e.message : "Invalid JSON",
        severity: "error",
      });
    }
  }
  return diags;
}

export function createStudioMock(onInstallPlugin?: (project: StudioProject) => void): {
  handle: ExtendHandler;
} {
  const projects = new Map<string, StoredProject>();
  let seq = 0;

  const lookup = (id: string): StoredProject | undefined => {
    const clean = decodeURIComponent(id).replace(/^studio:/, "");
    if (projects.has(clean)) return projects.get(clean);
    for (const item of projects.values()) {
      if (item.project.pluginId === clean) return item;
    }
    return undefined;
  };

  const handle: ExtendHandler = (method, seg, _q, body) => {
    // Serve dev PluginView requests at /api/plugins/studio:{id}/views/{page}
    const decodedSeg1 = seg[1] ? decodeURIComponent(seg[1]) : "";
    if (seg[0] === "plugins" && decodedSeg1.startsWith("studio:")) {
      const sp = lookup(decodedSeg1);
      if (!sp) {
        return json({ error: { code: "not_found", message: "Studio project not found." } }, 404);
      }
      if (method === "GET" && seg[2] === "views" && seg[3]) {
        const doc = defaultViewSpec(sp.project.name);
        return json({
          plugin: sp.project.pluginId,
          page: decodeURIComponent(seg[3]),
          ...doc,
        });
      }
      if (method === "POST" && seg[2] === "actions" && seg[3]) {
        return json({ toast: "Action executed in Studio sandbox.", refresh: true });
      }
    }

    if (seg[0] !== "plugins" || seg[1] !== "studio") return null;

    // GET /api/plugins/studio/templates
    if (method === "GET" && seg[2] === "templates" && seg.length === 3) {
      return json({ templates: TEMPLATES });
    }

    // GET /api/plugins/studio/projects
    if (method === "GET" && seg[2] === "projects" && seg.length === 3) {
      const list = Array.from(projects.values()).map(({ project: p }) => ({
        id: p.id,
        template: p.template,
        name: p.name,
        pluginId: p.pluginId,
        slot: p.slot,
        shadow: p.shadow,
        errors: p.diagnostics.filter((d) => d.severity === "error").length,
        updatedAt: p.updatedAt,
      }));
      return json({ projects: list });
    }

    // POST /api/plugins/studio/projects
    if (method === "POST" && seg[2] === "projects" && seg.length === 3) {
      const opts = (body ?? {}) as {
        template?: string;
        name?: string;
        pluginId?: string;
        slot?: string;
        stremioUrl?: string;
      };
      const tpl = TEMPLATES.find((t) => t.id === opts.template) ?? TEMPLATES[1]!;
      seq++;
      const pluginId = opts.pluginId?.trim() || tpl.defaultPluginId;
      const id = projects.has(pluginId) ? `${pluginId}-${seq}` : pluginId;
      const name = opts.name?.trim() || tpl.defaultName;
      const files = { ...tpl.files };

      try {
        const man = JSON.parse(files["manifest.json"] ?? "{}") as Record<string, unknown>;
        man.id = pluginId;
        man.name = name;
        if (opts.slot) {
          const ether = (man.ether ?? {}) as Record<string, unknown>;
          const contributes = (ether.contributes ?? {}) as Record<string, unknown>;
          contributes.slots = [
            {
              slot: opts.slot,
              key: "custom_slot",
              label: name,
              route: "/ether/view/overview",
            },
          ];
          ether.contributes = contributes;
          man.ether = ether;
        }
        files["manifest.json"] = JSON.stringify(man, null, 2);
      } catch {
        // Ignore parse error
      }

      const now = new Date().toISOString();
      const project: StudioProject = {
        id,
        template: tpl.id,
        name,
        pluginId,
        slot: opts.slot,
        stremioUrl: opts.stremioUrl,
        files,
        shadow: true,
        diagnostics: [],
        defaultPage: tpl.defaultPage,
        defaultRoute: tpl.defaultRoute,
        createdAt: now,
        updatedAt: now,
      };
      const initialCall: StudioCallRecord = {
        id: 1,
        plugin: pluginId,
        route: tpl.defaultRoute,
        method: "GET",
        status: 200,
        ms: 8,
        request: JSON.stringify({ apiKey: "[redacted]" }),
        response: JSON.stringify({
          channels: [{ id: "ch-1", number: "101", name: "Ether Cinema" }],
        }),
        created: now,
      };
      const initialHint: StudioValidationHint = {
        callId: 1,
        route: tpl.defaultRoute,
        severity: "info",
        field: "channels[0].logo",
        message: "Channel 1 (Ether Cinema) has no logo link.",
        suggestedFix:
          "Add an SVG or PNG logo URL so the guide renders channel art instead of a text badge.",
      };
      const initialJournal: StudioJournalEntry = {
        id: 1,
        plugin: pluginId,
        op: "collections.upsert",
        detail: name,
        shadow: true,
        undone: false,
        created: now,
      };

      projects.set(id, {
        project,
        calls: [initialCall],
        hints: [initialHint],
        journal: [initialJournal],
        logs: ["info: initialized sandboxed dev instance"],
      });
      return json({ project }, 201);
    }

    // /api/plugins/studio/projects/{id}/...
    if (seg[2] === "projects" && seg[3]) {
      const id = decodeURIComponent(seg[3]);
      const sp = lookup(id);
      if (!sp) {
        return json({ error: { code: "not_found", message: "Project not found." } }, 404);
      }
      const action = seg.slice(4).join("/");

      if (method === "GET" && action === "") {
        return json({ project: sp.project });
      }

      if (method === "PUT" && (action === "" || action === "files")) {
        const req = (body ?? {}) as { files?: Record<string, string>; shadow?: boolean };
        if (req.files) {
          sp.project.files = { ...sp.project.files, ...req.files };
        }
        if (typeof req.shadow === "boolean") {
          sp.project.shadow = req.shadow;
        }
        const diags = lintFiles(sp.project.files);
        sp.project.diagnostics = diags;
        sp.project.updatedAt = new Date().toISOString();
        const ok = !diags.some((d) => d.severity === "error");
        return json({
          project: sp.project,
          compile: { ok, diagnostics: diags, jsSize: 1024, sourcemapSize: 512 },
        });
      }

      if ((method === "GET" || method === "POST") && action === "preview") {
        const doc = defaultViewSpec(sp.project.name);
        return json({
          kind: sp.project.defaultPage ? "view" : "route",
          route: sp.project.defaultRoute || "/ether/health",
          page: sp.project.defaultPage || "overview",
          title: doc.title,
          spec: doc.spec,
          data: doc.data,
          body: {
            channels: [{ id: "ch-1", number: "101", name: "Ether Cinema", group: "Movies" }],
          },
          valid: true,
          hints: sp.hints,
          ms: 7,
        });
      }

      if (method === "POST" && action === "run") {
        const req = (body ?? {}) as { method?: string; path?: string; hotPath?: string };
        const route =
          req.hotPath === "rank"
            ? "hot:rank_key"
            : req.path || sp.project.defaultRoute || "/ether/health";
        const call: StudioCallRecord = {
          id: sp.calls.length + 1,
          plugin: sp.project.pluginId,
          route,
          method: req.method || "GET",
          status: 200,
          ms: 6,
          request: JSON.stringify({ route }),
          response: JSON.stringify({
            ok: true,
            channels: [{ id: "ch-1", number: "101", name: "Ether Cinema" }],
          }),
          created: new Date().toISOString(),
        };
        sp.calls.unshift(call);
        const entry: StudioJournalEntry = {
          id: sp.journal.length + 1,
          plugin: sp.project.pluginId,
          op: "collections.upsert",
          detail: sp.project.name,
          shadow: sp.project.shadow,
          undone: false,
          created: new Date().toISOString(),
        };
        sp.journal.unshift(entry);
        return json({
          status: 200,
          body: {
            ok: true,
            channels: [{ id: "ch-1", number: "101", name: "Ether Cinema" }],
          },
          ms: 6,
          units: 1200,
          logs: sp.logs,
          call,
          hints: sp.hints,
          journal: sp.journal,
        });
      }

      if (method === "POST" && action === "check") {
        const req = (body ?? {}) as { files?: Record<string, string> };
        if (req.files) {
          sp.project.files = { ...sp.project.files, ...req.files };
        }
        const diags = lintFiles(sp.project.files);
        sp.project.diagnostics = diags;
        const ok = !diags.some((d) => d.severity === "error");
        const report: StudioCheckReport = {
          ok,
          diagnostics: diags,
          checks: [
            {
              id: "manifest",
              category: "manifest",
              label: "Manifest and capability rules",
              status: "pass",
              message: `Valid manifest (${sp.project.pluginId} v0.1.0).`,
            },
            {
              id: "copy",
              category: "copy",
              label: "Copy and translation rules",
              status: "pass",
              message: "All labels, summaries and English strings follow Ether copy rules.",
            },
            {
              id: "compile",
              category: "compile",
              label: "TypeScript bundle compilation",
              status: ok ? "pass" : "fail",
              message: ok
                ? "Compiled 1024 bytes of JavaScript with source map."
                : "TypeScript entry failed to compile.",
            },
            {
              id: "runtime",
              category: "runtime",
              label: "Sandboxed WASM runtime probe",
              status: ok ? "pass" : "fail",
              message: "Loaded cleanly into QuickJS sandbox within memory and unit limits.",
            },
          ],
        };
        return json(report);
      }

      if (method === "GET" && action === "calls") {
        return json({ calls: sp.calls, hints: sp.hints });
      }

      if (method === "GET" && action === "journal") {
        return json({ entries: sp.journal });
      }

      if (method === "POST" && action === "shadow") {
        const req = (body ?? {}) as { shadow?: boolean };
        sp.project.shadow = Boolean(req.shadow);
        return json({ project: sp.project, shadow: sp.project.shadow });
      }

      if (method === "POST" && action === "undo") {
        let undone = 0;
        sp.journal.forEach((e) => {
          if (!e.undone) {
            e.undone = true;
            undone++;
          }
        });
        return json({ undone, entries: sp.journal });
      }

      if (method === "POST" && action === "install/preview") {
        return json({
          preview: {
            manifest: {
              id: sp.project.pluginId,
              version: "0.1.0",
              name: sp.project.name,
              description: "Built in Plugin Studio.",
              ether: {
                kind: "livetv_source",
                summary: sp.project.name,
                capabilities: ["livetv_channels", "ui"],
              },
            },
            permissionLines: ["Connects to api.pluto.tv"],
            needsViewers: false,
            needsFolders: false,
            verified: false,
          },
        });
      }

      if (method === "POST" && action === "install") {
        onInstallPlugin?.(sp.project);
        return json(
          {
            plugin: {
              id: sp.project.pluginId,
              name: sp.project.name,
              version: "0.1.0",
              enabled: true,
              shadow: sp.project.shadow,
            },
          },
          201,
        );
      }
    }

    return null;
  };

  return { handle };
}
