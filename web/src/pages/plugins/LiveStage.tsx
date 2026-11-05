import { Sparkles } from "lucide-react";
import { useEffect, useState } from "react";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { MomentView, type GuideChannel, type Moment, type StreamTile } from "./Stage";

/** One SSE chunk of Ether's agent stream (AI SDK UI message stream). */
interface AgentChunk {
  type: string;
  /** The assistant message (turn) id, on the start chunk. */
  messageId?: string;
  delta?: string;
  toolCallId?: string;
  toolName?: string;
  input?: Record<string, unknown>;
  output?: Record<string, unknown>;
  errorText?: string;
}

/** Reads an agent turn response chunk by chunk. */
export async function readAgentStream(res: Response, onChunk: (c: AgentChunk) => void) {
  if (!res.body) return;
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let i: number;
    while ((i = buf.indexOf("\n\n")) >= 0) {
      const frame = buf.slice(0, i);
      buf = buf.slice(i + 2);
      for (const line of frame.split("\n")) {
        if (!line.startsWith("data: ")) continue;
        const data = line.slice(6).trim();
        if (!data || data === "[DONE]") continue;
        try {
          onChunk(JSON.parse(data) as AgentChunk);
        } catch {
          // A partial or foreign frame is not a stage moment.
        }
      }
    }
  }
}

const SAMPLE_FILM = { imdb: "tt0063350", title: "Night of the Living Dead", year: "1968" };

interface RunResult {
  status: number;
  body?: Record<string, unknown>;
  ms: number;
}

function runDraft(id: string, path: string, query?: Record<string, string>) {
  return apiFetch<RunResult>(`/plugins/studio/projects/${encodeURIComponent(id)}/run`, {
    method: "POST",
    body: JSON.stringify({ method: "GET", path, query }),
  });
}

interface Programme {
  title?: string;
  start?: string;
  stop?: string;
}

/** The first channels of a draft's real lineup, with what is on now and next. */
async function draftGuide(id: string, limit = 6): Promise<GuideChannel[]> {
  const r = await runDraft(id, "/ether/channels.json");
  const list = (r.body?.channels as Record<string, unknown>[] | undefined) ?? [];
  const now = Date.now();
  const out: GuideChannel[] = [];
  for (const c of list) {
    const progs = ((c.programmes as Programme[] | undefined) ?? []).filter(
      (p) => p.title && p.start && p.stop,
    );
    const idx = progs.findIndex((p) => Date.parse(p.start!) <= now && now < Date.parse(p.stop!));
    if (idx < 0) continue;
    const cur = progs[idx]!;
    const start = Date.parse(cur.start!);
    const stop = Date.parse(cur.stop!);
    out.push({
      number: Number(c.number) || out.length + 1,
      name: String(c.name ?? ""),
      now: cur.title!,
      next: progs[idx + 1]?.title ?? "",
      progress: Math.min(1, Math.max(0, (now - start) / Math.max(1, stop - start))),
    });
    if (out.length >= limit) break;
  }
  return out;
}

/** Sources a draft finds for a sample catalogue title, as stream tiles. */
async function draftSources(id: string): Promise<{ title: string; tiles: StreamTile[] }> {
  const r = await runDraft(id, `/stream/movie/${SAMPLE_FILM.imdb}.json`, {
    title: SAMPLE_FILM.title,
    year: SAMPLE_FILM.year,
  });
  const streams = (r.body?.streams as { title?: string; name?: string }[] | undefined) ?? [];
  return {
    title: `${SAMPLE_FILM.title} (${SAMPLE_FILM.year})`,
    tiles: streams.slice(0, 4).map((s) => {
      const [name, detail] = String(s.title ?? s.name ?? "").split("\n");
      return { name: name || String(s.name ?? ""), score: detail };
    }),
  };
}

type CatalogPreview = {
  title: string;
  items: { key: string; title: string; poster?: string }[];
  total: number;
};

/** The first catalogue an add-on draft answers, as the posters Ether shows. */
async function draftCatalog(
  id: string,
  manifestText: string | undefined,
): Promise<CatalogPreview | null> {
  let man: {
    name?: string;
    catalogs?: { type: string; id: string; name?: string; extra?: { isRequired?: boolean }[] }[];
  } = {};
  if (manifestText === undefined) {
    const p = await apiFetch<{ project: { files: Record<string, string> } }>(
      `/plugins/studio/projects/${encodeURIComponent(id)}`,
    );
    manifestText = p.project.files["manifest.json"];
  }
  try {
    man = JSON.parse(manifestText ?? "{}");
  } catch {
    return null;
  }
  for (const c of man.catalogs ?? []) {
    if ((c.extra ?? []).some((e) => e.isRequired)) continue;
    const r = await runDraft(id, `/catalog/${c.type}/${c.id}.json`);
    const metas =
      (r.body?.metas as { id?: string; name?: string; poster?: string }[] | undefined) ?? [];
    if (!metas.length) continue;
    return {
      // The plugin's own catalogue, named as the plugin names it, so it
      // can never read as one of Ether's lists.
      title: t("{plugin} · {catalog}", { plugin: man.name || id, catalog: c.name || c.id }),
      items: metas.map((m, i) => ({ key: `${m.id ?? i}`, title: m.name ?? "", poster: m.poster })),
      total: metas.length,
    };
  }
  return null;
}

/** The draft's own data in the surface it will extend, for the editor. */
export function NativePreview({
  projectId,
  template,
  manifest,
  epoch,
}: {
  projectId: string;
  template: string;
  manifest?: string;
  epoch?: number;
}) {
  const [guide, setGuide] = useState<GuideChannel[] | null>(null);
  const [catalog, setCatalog] = useState<CatalogPreview | null>(null);
  const [sources, setSources] = useState<{ title: string; tiles: StreamTile[] } | null>(null);
  useEffect(() => {
    let live = true;
    setGuide(null);
    setSources(null);
    setCatalog(null);
    if (template === "addon") {
      void draftCatalog(projectId, manifest)
        .then((c) => live && setCatalog(c))
        .catch(() => undefined);
    } else if (template === "channels") {
      void draftGuide(projectId)
        .then((g) => live && setGuide(g))
        .catch(() => live && setGuide([]));
    } else if (template === "video_source") {
      void draftSources(projectId)
        .then((s) => live && setSources(s))
        .catch(() => live && setSources({ title: "", tiles: [] }));
    }
    return () => {
      live = false;
    };
  }, [projectId, template, epoch]);
  if (catalog?.items.length) {
    return (
      <MomentView
        moment={{
          kind: "catalog",
          tool: "catalog",
          title: catalog.title,
          items: catalog.items,
          total: catalog.total,
        }}
      />
    );
  }
  if (template === "channels" && guide?.length) {
    return (
      <MomentView
        moment={{ kind: "guide", tool: "livetv", title: t("On now in Live TV"), channels: guide }}
      />
    );
  }
  if (template === "video_source" && sources?.tiles.length) {
    return (
      <MomentView
        moment={{
          kind: "found",
          tool: "sources",
          title: t("Sources for {title}", { title: sources.title }),
          cards: sources.tiles.map((s) => ({ name: s.name, url: s.score ?? "", via: "source" })),
        }}
      />
    );
  }
  return null;
}

/**
 * The live stage: every tool call the agent makes becomes a moment, drawn
 * with the approved stage widgets and filled with the call's real result.
 */
export function LiveStageView({ moments, narration }: { moments: Moment[]; narration: string }) {
  const current = moments[moments.length - 1];
  const result = [...moments].reverse().find((m) => m.kind === "guide" || m.kind === "found");
  const done = moments.slice(0, -1);
  return (
    <div className="mx-auto w-full max-w-4xl space-y-4 px-4 py-6 sm:px-6">
      {narration ? (
        <p className="flex items-start gap-2 text-sm text-fg-muted">
          <Sparkles size={14} className="mt-0.5 shrink-0 text-primary" aria-hidden />
          <span>{narration}</span>
        </p>
      ) : null}
      {current ? (
        <div className="stage-rise" key={moments.length}>
          <MomentView moment={current} />
        </div>
      ) : null}
      {result && result !== current ? <MomentView moment={result} /> : null}
      {done.length ? (
        <details className="rounded-xl border border-border bg-surface p-4">
          <summary className="cursor-pointer text-sm text-fg-muted">
            {t("Completed steps ({count})", { count: done.length })}
          </summary>
          <ul className="mt-3 space-y-2 text-sm text-fg-muted">
            {done.map((m, i) => (
              <li key={i}>{"title" in m ? m.title : m.text}</li>
            ))}
          </ul>
        </details>
      ) : null}
    </div>
  );
}

const host = (u: unknown) => {
  try {
    return new URL(String(u)).host;
  } catch {
    return String(u ?? "");
  }
};

/** Turns one finished tool call into the moments it shows on stage. */
export async function momentsForTool(
  name: string,
  input: Record<string, unknown>,
  output: Record<string, unknown> | undefined,
): Promise<Moment[]> {
  const project = output?.project as { id?: string; template?: string } | undefined;
  switch (name) {
    case "web_search": {
      const results = (output?.results as { title?: string; url?: string }[] | undefined) ?? [];
      return [
        {
          kind: "found",
          tool: name,
          title: t("Found on the web: {query}", { query: String(input.query ?? "") }),
          cards: results
            .slice(0, 4)
            .map((r) => ({ name: r.title ?? host(r.url), url: host(r.url), via: "web" })),
        },
      ];
    }
    case "web_fetch":
      return [{ kind: "repair", text: t("Read {site}", { site: host(input.url) }) }];
    case "studio_project_create":
    case "studio_project_save": {
      const out: Moment[] = [];
      if (name === "studio_project_save") {
        const compile = output?.compile as
          | { ok?: boolean; diagnostics?: { file?: string; line?: number; text?: string }[] }
          | undefined;
        const preview = output?.preview as
          { ok?: boolean; error?: string; errors?: unknown[]; problems?: string[] } | undefined;
        const install = output?.installCheck;
        const checks = [
          { text: compile?.ok ? t("Compiles") : t("Has compile errors"), failed: !compile?.ok },
          ...(preview
            ? [
                {
                  text: preview.ok ? t("Preview renders") : t("Preview needs fixes"),
                  failed: !preview.ok,
                },
              ]
            : []),
          ...(typeof install === "string"
            ? [
                {
                  text: install === "ok" ? t("Ready to install") : t("Install check has notes"),
                  failed: install !== "ok",
                },
              ]
            : []),
        ];
        const issues = [
          ...(compile?.diagnostics ?? []).map((d) =>
            `${d.file ?? ""}${d.line ? `:${d.line}` : ""} ${d.text ?? ""}`.trim(),
          ),
          preview?.error,
          ...(preview?.problems ?? []),
          ...(preview?.errors ?? []).map((e) =>
            typeof e === "string" ? e : (e as { message?: string })?.message,
          ),
          typeof install === "string" && install !== "ok" ? install : undefined,
        ].filter((s): s is string => typeof s === "string" && s.length > 0);
        out.push({
          kind: "checks",
          title: t("Checked the draft"),
          items: checks.map((check) => check.text),
          failed: checks.map((check) => check.failed),
          issues: [...new Set(issues)].slice(0, 6).map((issue) => issue.slice(0, 600)),
        });
      }
      if (project?.id && project.template === "channels") {
        const channels = await draftGuide(project.id).catch(() => []);
        if (channels.length)
          out.push({ kind: "guide", tool: name, title: t("On now in Live TV"), channels });
      } else if (project?.id && project.template === "addon") {
        const c = await draftCatalog(project.id, undefined).catch(() => null);
        if (c)
          out.push({ kind: "catalog", tool: name, title: c.title, items: c.items, total: c.total });
      } else if (project?.id && project.template === "video_source") {
        const s = await draftSources(project.id).catch(() => null);
        if (s?.tiles.length)
          out.push({
            kind: "found",
            tool: name,
            title: t("Sources for {title}", { title: s.title }),
            cards: s.tiles.map((x) => ({ name: x.name, url: x.score ?? "", via: "source" })),
          });
      }
      if (!out.length) out.push({ kind: "repair", text: t("Saved the draft") });
      return out;
    }
    case "plugin_install":
      return [{ kind: "repair", text: t("Prepared the install for your approval") }];
    default:
      return [];
  }
}
