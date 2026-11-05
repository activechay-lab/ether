import { copyText } from "../../../components/CopyButton";
import { Badge, Button, Switch, cn, confirmDialog, toast } from "@braedonsaunders/appkit-ui";
import { Check, ChevronRight, Copy, RefreshCw, Undo2 } from "lucide-react";
import { Fragment, useCallback, useEffect, useMemo, useState } from "react";
import { Segmented } from "../../../components/fields";
import { Section } from "../../../components/layout";
import { t, tn } from "../../../i18n";
import { apiFetch } from "../../../lib/api";
import { formatDateTime, formatNumber, relativeTime } from "../../../lib/format";
import { journalOpLabel, type JournalEntry, type PluginDetail } from "../types";
import {
  SLOW_CALL_MS,
  callImdb,
  describeCall,
  resolveTitles,
  resultCount,
  type PluginCall,
  type TitleCardLite,
} from "./calls";
import { HealthTiles } from "./HealthTiles";

type Filter = "all" | "errors" | "slow";

/**
 * What Ether asked the plugin and what came back, the changes it made
 * (each undoable back to that point), and the raw log for anyone who
 * wants it.
 */
export function PluginActivity({
  detail,
  calls,
  onReloadCalls,
  journal,
  onReloadJournal,
  liveLogLines,
  onError,
  onChanged,
}: {
  detail: PluginDetail;
  calls: PluginCall[] | null;
  onReloadCalls: () => Promise<void>;
  journal: JournalEntry[];
  onReloadJournal: () => Promise<void>;
  liveLogLines: string[];
  onError: (e: unknown) => void;
  onChanged: () => void;
}) {
  return (
    <div className="space-y-5">
      <HealthTiles detail={detail} />
      <CallsTable detail={detail} calls={calls} onReload={onReloadCalls} />
      <Changes
        detail={detail}
        journal={journal}
        onReload={onReloadJournal}
        onError={onError}
        onChanged={onChanged}
      />
      <RawLog pluginId={detail.id} liveLogLines={liveLogLines} />
    </div>
  );
}

function CallsTable({
  detail,
  calls,
  onReload,
}: {
  detail: PluginDetail;
  calls: PluginCall[] | null;
  onReload: () => Promise<void>;
}) {
  const [filter, setFilter] = useState<Filter>("all");
  const [open, setOpen] = useState<number | null>(null);
  const [titles, setTitles] = useState<Record<string, TitleCardLite>>({});
  const [refreshing, setRefreshing] = useState(false);

  const imdbs = useMemo(
    () => [...new Set((calls ?? []).map((c) => callImdb(c.route)).filter(Boolean))] as string[],
    [calls],
  );
  useEffect(() => {
    let live = true;
    void resolveTitles(imdbs).then((m) => live && setTitles(m));
    return () => {
      live = false;
    };
  }, [imdbs]);

  // Routine health checks run every minute; the Responding tile covers them.
  const all = (calls ?? []).filter((c) => c.route !== "/ether/health");
  const errors = all.filter((c) => c.status !== 200 || c.error);
  const slow = all.filter((c) => c.ms >= SLOW_CALL_MS);
  const shown = (filter === "errors" ? errors : filter === "slow" ? slow : all).slice(0, 100);

  return (
    <Section
      title={t("Recent calls")}
      actions={
        <div className="flex items-center gap-2">
          <Segmented
            size="sm"
            ariaLabel={t("Show calls")}
            value={filter}
            onChange={setFilter}
            options={[
              { value: "all", label: t("All") },
              { value: "errors", label: tn(errors.length, "{count} error", "{count} errors") },
              { value: "slow", label: tn(slow.length, "{count} slow", "{count} slow") },
            ]}
          />
          <Button
            variant="ghost"
            size="sm"
            aria-label={t("Refresh")}
            disabled={refreshing}
            onClick={() => {
              setRefreshing(true);
              void onReload().finally(() => setRefreshing(false));
            }}
          >
            <RefreshCw size={13} className={cn(refreshing && "animate-spin")} />
          </Button>
        </div>
      }
      bodyClassName="p-0"
    >
      {calls === null ? (
        <p className="px-5 py-4 text-sm text-fg-muted">{t("Loading…")}</p>
      ) : !shown.length ? (
        <p className="px-5 py-4 text-sm text-fg-muted">
          {filter === "all"
            ? t("Ether hasn't called {name} yet.", { name: detail.name })
            : filter === "errors"
              ? t("No failed calls.")
              : t("No slow calls.")}
        </p>
      ) : (
        <table className="w-full table-fixed text-sm">
          <thead>
            <tr className="border-b border-border text-left text-xs text-fg-subtle">
              <th className="w-36 px-5 py-2 font-medium">{t("When")}</th>
              <th className="px-2 py-2 font-medium">{t("What Ether asked")}</th>
              <th className="w-28 px-2 py-2 font-medium">{t("Result")}</th>
              <th className="w-28 px-5 py-2 text-right font-medium">{t("Took")}</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((c) => {
              const failed = c.status !== 200 || Boolean(c.error);
              const n = resultCount(c);
              const expanded = open === c.id;
              return (
                <Fragment key={c.id}>
                  <tr
                    onClick={() => setOpen(expanded ? null : c.id)}
                    aria-expanded={expanded}
                    className="cursor-pointer border-b border-border-subtle last:border-b-0 hover:bg-surface-hover"
                  >
                    <td
                      className="truncate whitespace-nowrap px-5 py-2.5 text-xs text-fg-muted"
                      title={formatDateTime(c.created)}
                    >
                      {relativeTime(c.created)}
                    </td>
                    <td className="px-2 py-2.5">
                      <span className="flex min-w-0 items-center gap-1.5">
                        <ChevronRight
                          size={13}
                          className={cn(
                            "shrink-0 text-fg-subtle transition-transform",
                            expanded && "rotate-90",
                          )}
                          aria-hidden
                        />
                        <span className="truncate text-fg">
                          {describeCall(c.route, detail, titles)}
                        </span>
                      </span>
                    </td>
                    <td className="px-2 py-2.5">
                      {failed ? (
                        <Badge variant="destructive">
                          {c.status ? t("Error {status}", { status: c.status }) : t("Failed")}
                        </Badge>
                      ) : n !== undefined ? (
                        <Badge variant={n ? "success" : "secondary"}>
                          {tn(n, "{count} result", "{count} results")}
                        </Badge>
                      ) : (
                        <Badge variant="success">{t("OK")}</Badge>
                      )}
                    </td>
                    <td
                      className={cn(
                        "whitespace-nowrap px-5 py-2.5 text-right text-xs tabular-nums",
                        c.ms >= SLOW_CALL_MS ? "font-medium text-warning" : "text-fg-muted",
                      )}
                    >
                      {t("{ms} ms", { ms: formatNumber(c.ms, 0) })}
                    </td>
                  </tr>
                  {expanded ? (
                    <tr className="border-b border-border-subtle bg-bg-subtle">
                      <td colSpan={4} className="px-5 py-3">
                        <p className="mb-2 break-all font-mono text-xs text-fg-muted">
                          {c.method} {c.route}
                        </p>
                        {c.error ? (
                          <p className="mb-2 break-words font-mono text-xs text-danger">
                            {c.error}
                          </p>
                        ) : null}
                        {c.response ? (
                          <pre className="max-h-64 overflow-auto rounded-lg border border-border bg-surface p-3 font-mono text-[11px] leading-relaxed text-fg">
                            {pretty(c.response)}
                          </pre>
                        ) : null}
                      </td>
                    </tr>
                  ) : null}
                </Fragment>
              );
            })}
          </tbody>
        </table>
      )}
    </Section>
  );
}

function pretty(body: string): string {
  try {
    return JSON.stringify(JSON.parse(body), null, 2);
  } catch {
    return body;
  }
}

function Changes({
  detail,
  journal,
  onReload,
  onError,
  onChanged,
}: {
  detail: PluginDetail;
  journal: JournalEntry[];
  onReload: () => Promise<void>;
  onError: (e: unknown) => void;
  onChanged: () => void;
}) {
  const [busy, setBusy] = useState(false);
  if (!journal.length) return null;
  const undoTo = async (entry: JournalEntry) => {
    const newer = journal.filter((j) => !j.undone && j.id >= entry.id).length;
    const ok = await confirmDialog({
      title: t("Undo back to this change?"),
      message:
        newer > 1
          ? t("This reverses this change and the {count} made after it.", { count: newer - 1 })
          : t("This reverses this change."),
      confirmLabel: t("Undo"),
      tone: "danger",
    });
    if (!ok) return;
    setBusy(true);
    try {
      const r = await apiFetch<{ undone: number }>(
        `/plugins/${encodeURIComponent(detail.id)}/undo?since=${entry.id}`,
        { method: "POST" },
      );
      toast.success(tn(r.undone, "Undid {count} change", "Undid {count} changes"));
      await onReload();
      onChanged();
    } catch (e) {
      onError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Section title={t("Changes it made")} bodyClassName="p-0">
      <ul className="divide-y divide-border">
        {journal.map((entry) => (
          <li key={entry.id} className="flex items-center justify-between gap-3 px-5 py-3">
            <div className="min-w-0">
              <p className="flex items-center gap-2 text-sm font-medium text-fg">
                {journalOpLabel(entry.op)}
                {entry.undone ? <Badge variant="secondary">{t("Undone")}</Badge> : null}
              </p>
              <p className="truncate text-xs text-fg-muted">
                {[entry.detail, relativeTime(entry.created)].filter(Boolean).join(" · ")}
              </p>
            </div>
            {!entry.undone ? (
              <Button variant="ghost" size="sm" disabled={busy} onClick={() => void undoTo(entry)}>
                <Undo2 size={13} /> {t("Undo back to here")}
              </Button>
            ) : null}
          </li>
        ))}
      </ul>
    </Section>
  );
}

function RawLog({ pluginId, liveLogLines }: { pluginId: string; liveLogLines: string[] }) {
  const [show, setShow] = useState(false);
  const [lines, setLines] = useState<string[]>([]);
  const [copied, setCopied] = useState(false);
  const load = useCallback(async () => {
    try {
      const r = await apiFetch<{ lines: string[] }>(
        `/plugins/${encodeURIComponent(pluginId)}/logs?limit=200`,
      );
      setLines(r.lines ?? []);
    } catch {
      setLines([]);
    }
  }, [pluginId]);
  useEffect(() => {
    if (show) void load();
  }, [show, load]);
  const all = [...lines, ...liveLogLines].slice(-200);
  return (
    <div className="space-y-3">
      <label className="flex items-center gap-2 text-sm text-fg-muted">
        <Switch checked={show} onChange={(e) => setShow(e.currentTarget.checked)} />
        {t("Show raw log")}
      </label>
      {show ? (
        <div className="space-y-2">
          <div className="flex justify-end gap-2">
            <Button variant="ghost" size="sm" onClick={() => void load()}>
              <RefreshCw size={13} /> {t("Refresh")}
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={!all.length}
              onClick={() => {
                void copyText(all.join("\n")).then(setCopied);
                setTimeout(() => setCopied(false), 1500);
              }}
            >
              {copied ? <Check size={13} /> : <Copy size={13} />} {copied ? t("Copied") : t("Copy")}
            </Button>
          </div>
          {all.length ? (
            <pre
              aria-label={t("Log output")}
              className="max-h-80 overflow-auto rounded-lg border border-border bg-bg-subtle p-3.5 font-mono text-xs leading-relaxed text-fg"
            >
              {all.join("\n")}
            </pre>
          ) : (
            <p className="text-sm text-fg-muted">{t("No log lines yet.")}</p>
          )}
        </div>
      ) : null}
    </div>
  );
}
