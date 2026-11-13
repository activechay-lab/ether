import {
  Badge,
  Button,
  EmptyState,
  Input,
  Switch,
  confirmDialog,
  toast,
  cn,
} from "@braedonsaunders/appkit-ui";
import { CircleDot, Download, RefreshCw, ScrollText, Search, Trash2 } from "lucide-react";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useSearchParams } from "react-router-dom";
import { ErrorPanel, LoadingRows } from "../components/feedback";
import { Segmented } from "../components/fields";
import { getLocale, localized, t, tn } from "../i18n";
import { api, apiBlob, API_BASE, errorMessage, getToken, SESSION_HEADER } from "../lib/api";
import { useDebouncedValue } from "../lib/hooks";
import type { LogEntry } from "../lib/types";
import { createStreamParser } from "../lib/stream";
import { RecordProblem } from "./RecordProblem";

const LEVELS = localized(() => [
  { value: "", label: t("All") },
  { value: "debug", label: t("Debug+") },
  { value: "info", label: t("Info+") },
  { value: "warn", label: t("Warnings+") },
  { value: "error", label: t("Errors") },
]);

function levelVariant(l: string): "destructive" | "warning" | "info" | "secondary" {
  const v = l.toLowerCase();
  return v.startsWith("err")
    ? "destructive"
    : v.startsWith("warn")
      ? "warning"
      : v === "info"
        ? "info"
        : "secondary";
}

export default function Logs() {
  const [entries, setEntries] = useState<LogEntry[] | null>(null);
  const [total, setTotal] = useState(0);
  const [level, setLevel] = useState("");
  const [q, setQ] = useState("");
  const query = useDebouncedValue(q, 250);
  const [follow, setFollow] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [params] = useSearchParams();
  const [record, setRecord] = useState(params.get("record") === "1");

  const load = useCallback(async () => {
    try {
      const r = await api.logs({ limit: 1000, level, q: query });
      setEntries(
        [...(r.entries ?? [])].reverse().sort((a, b) => Date.parse(b.time) - Date.parse(a.time)),
      );
      setTotal(r.total);
      setError(null);
    } catch (e) {
      setError(e);
    }
  }, [level, query]);

  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    if (!follow) return;
    const ctl = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const connect = async () => {
      try {
        const response = await fetch(
          API_BASE + "/logs/stream?" + new URLSearchParams({ level, q: query }),
          {
            headers: { Accept: "text/event-stream", [SESSION_HEADER]: getToken() ?? "" },
            signal: ctl.signal,
          },
        );
        if (response.status === 401 || response.status === 403) return;
        if (!response.ok || !response.body) throw new Error(response.statusText);
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let lastID = 0;
        const parser = createStreamParser((event) => {
          if (ctl.signal.aborted) return;
          if (event.type === "snapshot") {
            const snapshot = JSON.parse(event.data) as {
              entries: LogEntry[] | null;
              total: number;
            };
            lastID = Math.max(0, ...(snapshot.entries ?? []).map((e) => e.id));
            setEntries([...(snapshot.entries ?? [])].reverse());
            setTotal(snapshot.total);
          } else if (event.type === "entry") {
            const update = JSON.parse(event.data) as { entry: LogEntry; total: number };
            const entry = update.entry;
            if (entry.id <= lastID) return;
            lastID = entry.id;
            setTotal(update.total);
            setEntries((old) => {
              if (old?.some((e) => e.id === entry.id)) return old;
              return [entry, ...(old ?? [])].slice(0, 1000);
            });
          }
          setError(null);
        });
        try {
          while (!ctl.signal.aborted) {
            const { done, value } = await reader.read();
            if (done) break;
            parser.push(decoder.decode(value, { stream: true }));
          }
        } finally {
          await reader.cancel();
        }
      } catch (e) {
        if (!ctl.signal.aborted) setError(e);
      }
      if (!ctl.signal.aborted) timer = setTimeout(() => void connect(), 2000);
    };
    void connect();
    return () => {
      ctl.abort();
      clearTimeout(timer);
    };
  }, [follow, level, query]);

  const visible = useMemo(() => {
    const ranks: Record<string, number> = { debug: 0, info: 1, warn: 2, error: 3 };
    const needle = query.trim().toLowerCase();
    return (
      entries?.filter(
        (entry) =>
          (!level || (ranks[entry.level.toLowerCase()] ?? 1) >= (ranks[level] ?? 1)) &&
          (!needle ||
            entry.message.toLowerCase().includes(needle) ||
            Object.entries(entry.attrs ?? {}).some(([k, v]) =>
              (k + "=" + v).toLowerCase().includes(needle),
            )),
      ) ?? []
    );
  }, [entries, level, query]);

  const clear = async () => {
    if (
      !(await confirmDialog({
        title: t("Clear logs?"),
        message: t("The in-memory log buffer is emptied. Log files on disk are not affected."),
        confirmLabel: t("Clear"),
        tone: "danger",
      }))
    )
      return;
    try {
      await api.clearLogs();
      await load();
      toast.success(t("Logs cleared"));
    } catch (e) {
      toast.error(t("Couldn't clear logs"), { description: errorMessage(e) });
    }
  };

  const [downloading, setDownloading] = useState(false);
  // The server's log files with credentials removed, for a bug report.
  const download = async () => {
    setDownloading(true);
    try {
      const blob = await apiBlob(API_BASE + "/logs/download");
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `ether-logs-${new Date().toISOString().slice(0, 10)}.txt`;
      anchor.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (e) {
      toast.error(t("Couldn't download logs"), { description: errorMessage(e) });
    } finally {
      setDownloading(false);
    }
  };

  return (
    <section aria-label={t("Logs")} className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-fg-muted">
          {tn(
            total,
            "Newest entries first. {count} entry retained in memory.",
            "Newest entries first. {count} entries retained in memory.",
          )}
        </p>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => setRecord(true)}>
            <CircleDot size={14} /> {t("Record a problem")}
          </Button>
          <Button variant="outline" size="sm" onClick={() => void load()}>
            <RefreshCw size={14} /> {t("Reload")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={downloading}
            title={t("Log files with passwords, tokens and keys removed, for a bug report.")}
            onClick={() => void download()}
          >
            <Download size={14} /> {t("Download logs")}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => void clear()}>
            <Trash2 size={14} /> {t("Clear")}
          </Button>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-3" role="search">
        <div className="relative min-w-52 flex-1 sm:max-w-sm">
          <Search
            size={14}
            className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-fg-subtle"
            aria-hidden
          />
          <Input
            type="search"
            className="h-9 pl-8"
            placeholder={t("Filter messages…")}
            value={q}
            onChange={(e) => setQ(e.currentTarget.value)}
            aria-label={t("Filter logs")}
          />
        </div>
        <Segmented
          ariaLabel={t("Minimum level")}
          size="sm"
          value={level}
          onChange={setLevel}
          options={LEVELS}
        />
        <label className="flex items-center gap-2 text-sm text-fg-muted">
          <Switch checked={follow} onChange={(e) => setFollow(e.currentTarget.checked)} />{" "}
          {t("Live updates")}
        </label>
      </div>

      {error ? (
        <ErrorPanel error={error} onRetry={() => void load()} />
      ) : !entries ? (
        <LoadingRows rows={6} />
      ) : visible.length === 0 ? (
        <EmptyState
          icon={<ScrollText />}
          title={t("No log entries match")}
          description={
            q || level
              ? t("Try a broader filter.")
              : t("The server hasn't logged anything since it started or was last cleared.")
          }
        />
      ) : (
        <div
          className="overflow-hidden rounded-xl border border-border bg-surface"
          role="log"
          aria-label={t("Server logs")}
        >
          <VirtualLogs entries={visible}>
            {(e) => (
              <div
                key={e.id}
                className={cn(
                  "flex flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-border-subtle px-3 py-1.5 ",
                  e.level.toLowerCase().startsWith("err") && "bg-danger-subtle/40",
                )}
              >
                <time
                  className="shrink-0 tabular-nums text-fg-subtle"
                  dateTime={e.time}
                  title={new Date(e.time).toLocaleString(getLocale())}
                >
                  {new Date(e.time).toLocaleTimeString(getLocale())}
                </time>
                <Badge
                  variant={levelVariant(e.level)}
                  className="w-14 justify-center font-mono text-[10px] uppercase"
                >
                  {e.level}
                </Badge>
                <span className="min-w-0 flex-1 break-words text-fg">{e.message}</span>
                {e.attrs && Object.keys(e.attrs).length ? (
                  <span className="w-full break-all pl-[calc(4.5rem+3.5rem)] text-fg-muted sm:w-auto sm:pl-0">
                    {Object.entries(e.attrs).map(([k, v]) => (
                      <span key={k} className="mr-2">
                        <span className="text-fg-subtle">{k}=</span>
                        {v}
                      </span>
                    ))}
                  </span>
                ) : null}
              </div>
            )}
          </VirtualLogs>
        </div>
      )}
      <RecordProblem open={record} onClose={() => setRecord(false)} />
    </section>
  );
}

// Same viewport windowing approach as the guide/catalogue; measured heights
// preserve wrapped messages and attributes without mounting the entire buffer.
function VirtualLogs({
  entries,
  children,
}: {
  entries: LogEntry[];
  children: (entry: LogEntry) => ReactNode;
}) {
  const viewport = useRef<HTMLDivElement>(null);
  const heights = useRef(new Map<number, number>());
  const [view, setView] = useState({ top: 0, height: 640, revision: 0 });
  const offsets = [0];
  for (const entry of entries)
    offsets.push(offsets[offsets.length - 1]! + (heights.current.get(entry.id) ?? 48));
  const start = offsets.findIndex((offset) => offset >= view.top);
  const first = Math.max(0, (start < 0 ? entries.length : start) - 5);
  const edge = offsets.findIndex((offset) => offset > view.top + view.height);
  const end = edge < 0 ? entries.length : Math.min(entries.length, edge + 5);
  useLayoutEffect(() => {
    const el = viewport.current;
    if (!el) return;
    const measure = () =>
      setView((v) => ({ ...v, top: el.scrollTop, height: el.clientHeight || 640 }));
    measure();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    observer?.observe(el);
    return () => observer?.disconnect();
  }, []);
  useLayoutEffect(() => {
    const el = viewport.current;
    if (!el) return;
    const live = new Set(entries.map((entry) => entry.id));
    for (const id of heights.current.keys()) if (!live.has(id)) heights.current.delete(id);
    const measure = () => {
      let changed = false;
      for (const row of el.querySelectorAll<HTMLElement>("[data-log-id]")) {
        const id = Number(row.dataset.logId);
        const height = row.getBoundingClientRect().height;
        if (height > 0 && heights.current.get(id) !== height) {
          heights.current.set(id, height);
          changed = true;
        }
      }
      if (changed) setView((v) => ({ ...v, revision: v.revision + 1 }));
    };
    measure();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    el.querySelectorAll("[data-log-id]").forEach((row) => observer?.observe(row));
    return () => observer?.disconnect();
  }, [entries, first, end, view.revision]);
  return (
    <div
      ref={viewport}
      className="max-h-[calc(100vh-15rem)] overflow-auto font-mono text-xs"
      onScroll={(event) => {
        const top = event.currentTarget.scrollTop;
        setView((v) => ({ ...v, top }));
      }}
    >
      <div style={{ height: offsets[first] }} />
      {entries.slice(first, end).map((entry) => (
        <div key={entry.id} data-log-id={entry.id}>
          {children(entry)}
        </div>
      ))}
      <div style={{ height: offsets[entries.length]! - offsets[end]! }} />
    </div>
  );
}
