/**
 * One plugin view, loaded and rendered natively. Fetches the plugin's
 * {spec, data} document, validates it again in the browser, renders it with
 * the ViewSpec renderer, and owns the action loop: toasts, refreshes, page
 * and drawer opens, and approval cards for proposed writes. Slot hosts
 * (title panels, viewer tabs, dashboard panels) and full plugin pages both
 * render this component.
 */
import { Badge, Button, toast } from "@braedonsaunders/appkit-ui";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { getLocale, t } from "../i18n";
import { apiFetch, errorMessage } from "../lib/api";
import { Drawer } from "../components/Drawer";
import { ErrorPanel, LoadingRows } from "../components/feedback";
import {
  PluginViewContext,
  type PluginActionResult,
  type PluginOpen,
  type PluginProposal,
  type PluginViewContextValue,
} from "./context";
import { ViewSpecPage } from "./Render";
import { validatePluginSpec } from "./validate";
import type { PageSpec } from "@braedonsaunders/appkit-viewspec";

export interface PluginViewDoc {
  spec: PageSpec;
  data: unknown;
  /** Resolved page title from the plugin's server, when it sent one. */
  title?: string;
  icon?: string;
}

interface ViewResponse {
  spec: unknown;
  data?: unknown;
  title?: string;
  icon?: string;
}

function toParams(
  raw: Record<string, string | number | undefined> | undefined,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(raw ?? {})) {
    if (value !== undefined && value !== "") out[key] = String(value);
  }
  return out;
}

function pluginViewPath(plugin: string, page: string, params: Record<string, string>): string {
  // The server resolves the plugin's "$t:" text in the console's language.
  const q = new URLSearchParams({ ...params, locale: getLocale() });
  const base = `/plugins/${encodeURIComponent(plugin)}/views/${encodeURIComponent(page)}`;
  return q.toString() ? `${base}?${q}` : base;
}

export function pluginActionPath(plugin: string, action: string): string {
  return `/plugins/${encodeURIComponent(plugin)}/actions/${encodeURIComponent(action)}`;
}

export function pluginProposalPath(plugin: string, proposalId: string): string {
  return `/plugins/${encodeURIComponent(plugin)}/proposals/${encodeURIComponent(proposalId)}`;
}

/**
 * The approval card for a plugin proposal. Same shape as an Ask Ether
 * approval: what will happen, in the plugin server's own words, then an
 * explicit decision. Nothing runs until the owner chooses.
 */
export function ProposalCard({
  proposal,
  busy,
  onDecision,
}: {
  proposal: PluginProposal;
  busy: boolean;
  onDecision: (decision: "approved" | "rejected") => void;
}) {
  return (
    <section
      aria-label={proposal.title}
      className="rounded-xl border border-border bg-surface px-5 py-4"
    >
      <div className="flex flex-wrap items-center gap-2">
        <h3 className="text-sm font-semibold text-fg">{proposal.title}</h3>
        <Badge variant="warning">{t("Needs your approval")}</Badge>
      </div>
      {proposal.description ? (
        <p className="mt-1 text-sm text-fg-muted">{proposal.description}</p>
      ) : null}
      {proposal.lines?.length ? (
        <ul className="mt-2 list-disc space-y-0.5 pl-5 text-sm text-fg">
          {proposal.lines.map((line, i) => (
            <li key={i}>{line}</li>
          ))}
        </ul>
      ) : null}
      <div className="mt-3 flex flex-wrap justify-end gap-2">
        <Button
          type="button"
          size="sm"
          variant="outline"
          disabled={busy}
          onClick={() => onDecision("rejected")}
        >
          {t("Reject")}
        </Button>
        <Button type="button" size="sm" disabled={busy} onClick={() => onDecision("approved")}>
          {t("Approve")}
        </Button>
      </div>
    </section>
  );
}

export function PluginView({
  plugin,
  page,
  params,
  onDoc,
  liftPageHeader = false,
}: {
  plugin: string;
  page: string;
  params?: Record<string, string | number | undefined>;
  /** Called with each loaded document, so a page chrome can show its title. */
  onDoc?: (doc: PluginViewDoc) => void;
  /** The page chrome shows the plugin's page header, so skip it in the body. */
  liftPageHeader?: boolean;
}) {
  const navigate = useNavigate();
  const [viewParams, setViewParams] = useState<Record<string, string>>(() => toParams(params));
  const [tick, setTick] = useState(0);
  const [doc, setDoc] = useState<PluginViewDoc | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [proposal, setProposal] = useState<PluginProposal | null>(null);
  const [proposalBusy, setProposalBusy] = useState(false);
  const [drawer, setDrawer] = useState<PluginOpen | null>(null);
  const onDocRef = useRef(onDoc);
  onDocRef.current = onDoc;

  // Slot params from the host replace the view's base parameters (a title
  // panel re-points at another title); parameters the owner set inside the
  // view (page, tab) reset with them.
  const paramsKey = JSON.stringify(toParams(params));
  useEffect(() => {
    setViewParams((prev) =>
      JSON.stringify(prev) === paramsKey ? prev : (JSON.parse(paramsKey) as Record<string, string>),
    );
  }, [paramsKey]);

  const refresh = useCallback(() => setTick((n) => n + 1), []);

  useEffect(() => {
    const ctl = new AbortController();
    setError(null);
    apiFetch<ViewResponse>(pluginViewPath(plugin, page, viewParams), {
      signal: ctl.signal,
    })
      .then((res) => {
        const checked = validatePluginSpec(res.spec);
        if (!checked.ok) {
          console.warn("Ether plugin view refused:", checked.errors.join("; "));
          throw new Error(t("This plugin sent a page Ether cannot show."));
        }
        const loaded: PluginViewDoc = {
          spec: checked.spec,
          data: res.data ?? {},
          title: res.title,
          icon: res.icon,
        };
        setDoc(loaded);
        onDocRef.current?.(loaded);
      })
      .catch((e: unknown) => {
        if (ctl.signal.aborted) return;
        setError(e);
      });
    return () => ctl.abort();
  }, [plugin, page, viewParams, tick]);

  const open = useCallback(
    (target: PluginOpen) => {
      if (target.drawer) {
        setDrawer(target);
        return;
      }
      if (target.page) {
        const q = new URLSearchParams(target.params ?? {});
        const suffix = q.toString() ? `?${q}` : "";
        navigate(`/p/${encodeURIComponent(plugin)}/${encodeURIComponent(target.page)}${suffix}`);
        return;
      }
      if (target.route && target.route.startsWith("/") && !target.route.startsWith("//")) {
        navigate(target.route);
      }
    },
    [navigate, plugin],
  );

  const applyResult = useCallback(
    (result: PluginActionResult | null) => {
      if (!result) return;
      if (result.toast) {
        const toastSpec =
          typeof result.toast === "string"
            ? { text: result.toast, kind: "success" as const }
            : result.toast;
        if (toastSpec.kind === "error") toast.error(toastSpec.text);
        else if (toastSpec.kind === "info") toast.info(toastSpec.text);
        else toast.success(toastSpec.text);
      }
      if (result.job) return;
      if (result.proposal) setProposal(result.proposal);
      if (result.open) open(result.open);
      if (result.refresh) refresh();
    },
    [open, refresh],
  );

  const postAction = useCallback(
    async (
      action: string,
      actionParams: Record<string, unknown> = {},
    ): Promise<PluginActionResult | null> => {
      try {
        const result = await apiFetch<PluginActionResult>(pluginActionPath(plugin, action), {
          method: "POST",
          body: JSON.stringify({ params: actionParams }),
        });
        applyResult(result);
        return result;
      } catch (e) {
        toast.error(errorMessage(e));
        return null;
      }
    },
    [plugin, applyResult],
  );

  const decideProposal = useCallback(
    async (decision: "approved" | "rejected") => {
      if (!proposal) return;
      setProposalBusy(true);
      try {
        const result = await apiFetch<PluginActionResult>(pluginProposalPath(plugin, proposal.id), {
          method: "POST",
          body: JSON.stringify({ decision }),
        });
        setProposal(null);
        applyResult(result);
        if (!result?.toast && !result?.refresh) refresh();
      } catch (e) {
        toast.error(errorMessage(e));
      } finally {
        setProposalBusy(false);
      }
    },
    [plugin, proposal, applyResult, refresh],
  );

  const context = useMemo<PluginViewContextValue>(
    () => ({
      plugin,
      params: viewParams,
      setParams: (patch) =>
        setViewParams((prev) => {
          const next = { ...prev };
          for (const [key, value] of Object.entries(patch)) {
            if (value === undefined || value === "") delete next[key];
            else next[key] = value;
          }
          return next;
        }),
      refresh,
      postAction,
      open,
    }),
    [plugin, viewParams, refresh, postAction, open],
  );

  return (
    <PluginViewContext.Provider value={context}>
      <div className="space-y-4">
        {proposal ? (
          <ProposalCard
            proposal={proposal}
            busy={proposalBusy}
            onDecision={(decision) => void decideProposal(decision)}
          />
        ) : null}
        {error ? (
          <ErrorPanel error={error} onRetry={refresh} />
        ) : !doc ? (
          <LoadingRows rows={3} />
        ) : (
          <ViewSpecPage
            spec={
              liftPageHeader
                ? { ...doc.spec, header: doc.spec.header.filter((b) => b.kind !== "page-header") }
                : doc.spec
            }
            data={doc.data}
          />
        )}
      </div>
      <Drawer
        open={drawer !== null}
        onClose={() => setDrawer(null)}
        title={doc?.title ?? t("Plugin")}
      >
        {drawer?.drawer ? (
          <PluginView plugin={plugin} page={drawer.drawer} params={drawer.params} />
        ) : null}
      </Drawer>
    </PluginViewContext.Provider>
  );
}

/** The plain title and description of a document's page header, if any. */
export function pageHeaderText(doc: PluginViewDoc): { title?: string; description?: string } {
  const header = doc.spec.header.find((b) => b.kind === "page-header") as
    { title?: unknown; description?: unknown } | undefined;
  if (!header) return {};
  const read = (v: unknown): string | undefined => {
    if (typeof v === "string") return v;
    if (v && typeof v === "object" && typeof (v as { $?: unknown }).$ === "string") {
      let cur: unknown = doc.data;
      for (const part of (v as { $: string }).$.split(".")) {
        cur = cur && typeof cur === "object" ? (cur as Record<string, unknown>)[part] : undefined;
      }
      return typeof cur === "string" || typeof cur === "number" ? String(cur) : undefined;
    }
    return undefined;
  };
  return { title: read(header.title), description: read(header.description) };
}
