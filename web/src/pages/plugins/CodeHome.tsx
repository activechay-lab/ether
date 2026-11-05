import { Button, cn } from "@braedonsaunders/appkit-ui";
import {
  Bell,
  Bot,
  FileCode2,
  FileJson,
  Film,
  FolderOpen,
  LayoutTemplate,
  ListOrdered,
  PanelLeftClose,
  PanelLeftOpen,
  PanelsTopLeft,
  Play,
  Tv,
  type LucideIcon,
} from "lucide-react";
import { Suspense, lazy, useEffect, useState } from "react";
import { t } from "../../i18n";
import { apiFetch, errorMessage } from "../../lib/api";
import { Reference } from "./CodeStudio";
import { PaneResizer, useStudioPanes } from "./code/panes";
import type { StudioProject, StudioProjectSummary } from "./studio-types";

const CodeMirrorPane = lazy(() => import("./code/CodeMirrorPane"));

interface Template {
  id: string;
  defaultName: string;
  files: Record<string, string>;
}

/** Starters in the order an owner most often wants them. */
const STARTERS: { id: string; icon: LucideIcon; title: () => string; about: () => string }[] = [
  {
    id: "channels",
    icon: Tv,
    title: () => t("Live TV channels"),
    about: () => t("Channels with a programme guide, shown in Live TV."),
  },
  {
    id: "video_source",
    icon: Film,
    title: () => t("Video sources"),
    about: () => t("Playable copies of catalogue titles, found when someone presses play."),
  },
  {
    id: "list",
    icon: LayoutTemplate,
    title: () => t("Home rows"),
    about: () => t("A list of titles for Collections and Home rows."),
  },
  {
    id: "local_files",
    icon: FolderOpen,
    title: () => t("Local files"),
    about: () => t("Files from folders you pick, attached to catalogue titles."),
  },
  {
    id: "notification",
    icon: Bell,
    title: () => t("Notifications"),
    about: () => t("Server alerts sent to a webhook or messaging service."),
  },
  {
    id: "ranking",
    icon: ListOrdered,
    title: () => t("Ranking step"),
    about: () => t("Scores releases while a quality profile picks one."),
  },
  {
    id: "page",
    icon: PanelsTopLeft,
    title: () => t("Console page"),
    about: () => t("A page or panel in this console."),
  },
  {
    id: "tool",
    icon: Bot,
    title: () => t("Assistant tool"),
    about: () => t("Something Ask Ether and connected AI apps can do."),
  },
];

const ENTRY = "src/plugin.ts";

/**
 * Code view with no draft open: the editor straight away. Pick a starter,
 * read and change its code, and the first save turns it into a draft.
 */
export function CodeHome({ onOpenProject }: { onOpenProject: (id: string) => void }) {
  const [drafts, setDrafts] = useState<StudioProjectSummary[]>([]);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [starter, setStarter] = useState(STARTERS[0]!.id);
  const [file, setFile] = useState(ENTRY);
  const [edits, setEdits] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const panes = useStudioPanes();
  const [error, setError] = useState("");

  useEffect(() => {
    apiFetch<{ projects: StudioProjectSummary[] }>("/plugins/studio/projects")
      .then((r) =>
        setDrafts(
          [...r.projects].sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt)),
        ),
      )
      .catch((e) => setError(errorMessage(e)));
    apiFetch<{ templates: Template[] }>("/plugins/studio/templates")
      .then((r) => setTemplates(r.templates))
      .catch((e) => setError(errorMessage(e)));
  }, []);

  const tpl = templates.find((x) => x.id === starter);
  const files = Object.keys(tpl?.files ?? {}).sort((a, b) =>
    a === ENTRY ? -1 : b === ENTRY ? 1 : a.localeCompare(b),
  );
  const current = edits[file] ?? tpl?.files[file] ?? "";
  const meta = STARTERS.find((s) => s.id === starter)!;

  const pick = (id: string) => {
    if (id === starter) return;
    if (Object.keys(edits).length && !window.confirm(t("Discard your changes to this starter?")))
      return;
    setStarter(id);
    setEdits({});
    setFile(ENTRY);
  };

  // The first save makes the draft: the starter, plus whatever was typed.
  const save = async () => {
    if (!tpl || busy) return;
    setBusy(true);
    setError("");
    try {
      const r = await apiFetch<{ project: StudioProject }>("/plugins/studio/projects", {
        method: "POST",
        body: JSON.stringify({ template: tpl.id, name: meta.title() }),
      });
      if (Object.keys(edits).length)
        await apiFetch(`/plugins/studio/projects/${encodeURIComponent(r.project.id)}/files`, {
          method: "PUT",
          body: JSON.stringify({ files: edits }),
        });
      onOpenProject(r.project.id);
    } catch (e) {
      setError(errorMessage(e));
      setBusy(false);
    }
  };

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "s") {
        e.preventDefault();
        void save();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {drafts.length > 0 ? (
        <section
          aria-label={t("Saved drafts")}
          className="shrink-0 border-b border-border px-4 py-3"
        >
          <h2 className="mb-2 text-sm font-semibold">{t("Saved drafts")}</h2>
          <div className="flex max-h-28 flex-wrap gap-2 overflow-y-auto">
            {drafts.map((draft) => (
              <Button
                key={draft.id}
                size="sm"
                variant="outline"
                onClick={() => {
                  if (
                    Object.keys(edits).length &&
                    !window.confirm(t("Discard your changes to this starter?"))
                  )
                    return;
                  onOpenProject(draft.id);
                }}
              >
                {draft.name}
              </Button>
            ))}
          </div>
        </section>
      ) : null}
      <div ref={panes.grid} className={panes.className} style={panes.style}>
        <aside
          aria-label={t("Start from")}
          className="relative flex min-h-0 flex-col border-r border-border bg-bg-subtle"
        >
          {panes.collapsed ? (
            <div className="flex flex-col items-center gap-1 py-2">
              <button
                type="button"
                onClick={() => panes.setCollapsed(false)}
                aria-label={t("Show files")}
                title={t("Show files")}
                className="mb-1 rounded-md p-1.5 text-fg-muted hover:bg-surface-hover hover:text-fg"
              >
                <PanelLeftOpen size={16} />
              </button>
              {STARTERS.map((st) => (
                <button
                  key={st.id}
                  type="button"
                  onClick={() => pick(st.id)}
                  aria-label={st.title()}
                  title={st.title()}
                  aria-current={starter === st.id ? "true" : undefined}
                  className={cn(
                    "rounded-md p-1.5 transition",
                    starter === st.id
                      ? "bg-primary/10 text-fg"
                      : "text-fg-muted hover:bg-surface-hover hover:text-fg",
                  )}
                >
                  <st.icon size={15} aria-hidden />
                </button>
              ))}
            </div>
          ) : (
            <>
              <div className="min-h-0 flex-1 overflow-y-auto">
                <div className="flex items-center justify-between px-3 pb-1 pt-2">
                  <p className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
                    {t("Start from")}
                  </p>
                  <button
                    type="button"
                    onClick={() => panes.setCollapsed(true)}
                    aria-label={t("Hide files")}
                    title={t("Hide files")}
                    className="rounded-md p-1 text-fg-subtle hover:bg-surface-hover hover:text-fg"
                  >
                    <PanelLeftClose size={15} />
                  </button>
                </div>
                <ul className="space-y-0.5 px-1.5 pb-2">
                  {STARTERS.map((s) => (
                    <li key={s.id}>
                      <button
                        type="button"
                        onClick={() => pick(s.id)}
                        aria-current={starter === s.id ? "true" : undefined}
                        className={cn(
                          "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition",
                          starter === s.id
                            ? "bg-primary/10 text-fg"
                            : "text-fg-muted hover:bg-surface-hover hover:text-fg",
                        )}
                      >
                        <s.icon size={15} className="shrink-0" aria-hidden />
                        <span className="truncate">{s.title()}</span>
                      </button>
                    </li>
                  ))}
                </ul>
                {files.length ? (
                  <>
                    <p className="border-t border-border px-3 pb-1 pt-3 text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
                      {t("Files")}
                    </p>
                    <ul className="space-y-0.5 px-1.5 pb-3">
                      {files.map((f) => (
                        <li key={f}>
                          <button
                            type="button"
                            onClick={() => setFile(f)}
                            aria-current={file === f ? "page" : undefined}
                            className={cn(
                              "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left font-mono text-xs transition",
                              file === f
                                ? "bg-primary/10 text-fg"
                                : "text-fg-muted hover:bg-surface-hover hover:text-fg",
                            )}
                          >
                            {f.endsWith(".ts") ? <FileCode2 size={14} /> : <FileJson size={14} />}
                            <span className="min-w-0 flex-1 truncate">{f}</span>
                            {f in edits ? (
                              <span
                                className="size-1.5 rounded-full bg-primary"
                                aria-label={t("Not saved")}
                              />
                            ) : null}
                          </button>
                        </li>
                      ))}
                    </ul>
                  </>
                ) : null}
              </div>
              <PaneResizer
                side="right"
                width={panes.left}
                bounds={panes.leftBounds}
                onWidth={panes.setLeft}
                onReset={panes.resetLeft}
                label={t("Resize files")}
              />
            </>
          )}
        </aside>

        <section
          aria-label={t("Code")}
          className="flex min-h-[32rem] flex-col bg-surface lg:min-h-0"
        >
          <div className="flex shrink-0 items-center gap-2 border-b border-border bg-bg-subtle px-3 py-1.5">
            <span className="font-mono text-xs text-fg">{file}</span>
            <span className="ml-auto text-xs text-fg-muted">
              {t("Starter code. Saving makes it your draft.")}
            </span>
            <Button size="sm" disabled={busy || !tpl} onClick={() => void save()}>
              <Play size={13} /> {busy ? t("Creating…") : t("Save and run")}
            </Button>
          </div>
          <div className="min-h-0 flex-1">
            {tpl ? (
              <Suspense
                fallback={<p className="p-4 text-xs text-fg-muted">{t("Loading editor…")}</p>}
              >
                <CodeMirrorPane
                  key={`${starter}:${file}`}
                  file={file === ENTRY ? "plugin.ts" : file}
                  value={current}
                  onChange={(v) => setEdits((all) => ({ ...all, [file]: v }))}
                />
              </Suspense>
            ) : (
              <p className="p-4 text-xs text-fg-muted">{t("Loading editor…")}</p>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-3 border-t border-border bg-bg-subtle px-3 py-1 text-[11px] text-fg-muted">
            <span>{file.endsWith(".ts") ? "TypeScript" : "JSON"}</span>
            <span>⌘S {t("saves, compiles and refreshes the preview")}</span>
          </div>
          {error ? (
            <p role="alert" className="border-t border-border px-3 py-2 text-xs text-danger">
              {error}
            </p>
          ) : null}
        </section>

        <section
          aria-label={t("Tools")}
          className="relative flex min-h-0 flex-col border-t border-border lg:border-l lg:border-t-0"
        >
          <PaneResizer
            side="left"
            width={panes.right}
            bounds={panes.rightBounds}
            onWidth={panes.setRight}
            onReset={panes.resetRight}
            label={t("Resize tools")}
          />
          <div className="min-h-0 flex-1 overflow-y-auto p-4">
            <div className="mb-5 flex items-start gap-3">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                <meta.icon size={18} aria-hidden />
              </span>
              <div>
                <h2 className="text-sm font-semibold text-fg">{meta.title()}</h2>
                <p className="text-sm text-fg-muted">{meta.about()}</p>
              </div>
            </div>
            <Reference template={starter} />
          </div>
        </section>
      </div>
    </div>
  );
}
