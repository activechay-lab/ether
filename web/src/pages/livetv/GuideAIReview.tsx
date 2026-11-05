import { Badge, Button, Input, Popover, cn, toast } from "@braedonsaunders/appkit-ui";
import { Check, X, ArrowRight, Wand2, SlidersHorizontal, Info } from "lucide-react";
import {
  useCallback,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { Drawer } from "../../components/Drawer";
import { t, tn } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { errorMessage, isAbort } from "../../lib/api";
import { formatNumber } from "../../lib/format";
import {
  duration,
  tv,
  type ChannelSummary,
  type Coverage,
  type GuideChannel,
  type MatchChange,
  type Source,
} from "./model";

type Decision = "pending" | "accepted" | "rejected";
export type ReviewRow = {
  channel: ChannelSummary;
  proposal?: MatchChange;
  decision: Decision;
  checked: boolean;
  applied?: boolean;
  matched: boolean;
};
const ROW = 128,
  BATCH = 8;
export const highConfidence = (p?: MatchChange) =>
  !!p && !p.reviewRegion && p.method !== "manual" && (p.confidence ?? 1) >= 0.95;
const regionOf = (name: string) => name.match(/^\s*\|?([A-Z]{2,3})\s*[:|]/)?.[1] ?? "";
const statusOf = (r: ReviewRow) =>
  r.applied || r.matched
    ? "matched"
    : r.proposal
      ? r.decision
      : r.checked
        ? "unmatched"
        : "pending";

export function GuideAIAction({
  channels,
  sources,
  coverage,
  sourceId,
  onApplied,
}: {
  channels: ChannelSummary[];
  sources: Source[];
  coverage: Map<string, Coverage> | null;
  sourceId?: string;
  onApplied: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)} disabled={!coverage}>
        <Wand2 size={14} />
        {t("Match with AI")}
      </Button>
      <GuideAIReviewDrawer
        open={open}
        onClose={() => setOpen(false)}
        channels={channels}
        sources={sources}
        coverage={coverage}
        sourceId={sourceId}
        onApplied={onApplied}
      />
    </>
  );
}

export function GuideAIReviewDrawer({
  open,
  onClose,
  channels,
  sources,
  coverage,
  sourceId,
  onApplied,
}: {
  open: boolean;
  onClose: () => void;
  channels: ChannelSummary[];
  sources: Source[];
  coverage: Map<string, Coverage> | null;
  sourceId?: string;
  onApplied: () => Promise<void>;
}) {
  const { locale } = useLocale();
  const [rows, setRows] = useState<ReviewRow[]>([]),
    rowsRef = useRef(rows);
  rowsRef.current = rows;
  const [ready, setReady] = useState(false),
    [busy, setBusy] = useState(false),
    [running, setRunning] = useState(false),
    [configured, setConfigured] = useState<boolean | null>(null);
  const [progress, setProgress] = useState({ done: 0, total: 0 }),
    [error, setError] = useState("");
  const [query, setQuery] = useState(""),
    search = useDeferredValue(query.toLowerCase());
  const [filters, setFilters] = useState(false),
    [band, setBand] = useState("all"),
    [source, setSource] = useState("all"),
    [region, setRegion] = useState("all"),
    [group, setGroup] = useState("all"),
    [status, setStatus] = useState("all"),
    [sort, setSort] = useState("name");
  const [active, setActive] = useState(0),
    [undoToken, setUndoToken] = useState("");
  const [undoIDs, setUndoIDs] = useState<string[]>([]);
  const ctl = useRef<AbortController | null>(null),
    runningRef = useRef(false),
    initialized = useRef("");
  const [changing, setChanging] = useState<string | null>(null),
    [guideQuery, setGuideQuery] = useState(""),
    [guides, setGuides] = useState<GuideChannel[]>([]),
    [guideError, setGuideError] = useState("");
  const storageKey = `ether-guide-review-v2:${sourceId ?? "all"}:${channels.length === 1 ? channels[0]?.id : "lineup"}`;
  useEffect(() => () => ctl.current?.abort(), []);
  const cancel = () => {
    ctl.current?.abort();
    runningRef.current = false;
    setRunning(false);
    setBusy(false);
  };
  const close = () => {
    if (busy) return;
    cancel();
    if (!ready) initialized.current = "";
    onClose();
  };
  // Drafts survive closing the drawer and browser reloads. Mapping mismatches
  // invalidate restored proposals; only review metadata is stored, never URLs.
  useEffect(() => {
    if (!open || initialized.current === storageKey) return;
    initialized.current = storageKey;
    setReady(false);
    setConfigured(null);
    setError("");
    let saved: Record<
      string,
      {
        proposal?: MatchChange;
        decision: Decision;
        checked: boolean;
        guideId?: string;
        guideSourceId?: string;
        applied?: boolean;
        matched?: boolean;
      }
    > = {};
    try {
      const data = JSON.parse(sessionStorage.getItem(storageKey) ?? "{}");
      saved = data.rows ?? {};
      setUndoToken(data.undoToken ?? "");
      setUndoIDs(data.undoIDs ?? []);
    } catch {
      /* optional draft */
    }
    const next = channels
      .filter((c) => c.kind === "iptv" && c.enabled)
      .map((channel) => {
        const old = saved[channel.id],
          beforeValid =
            old &&
            (old.guideId ?? "") === (channel.guideId ?? "") &&
            (old.guideSourceId ?? "") === (channel.guideSourceId ?? "");
        const afterValid =
          old?.applied &&
          old.proposal &&
          old.proposal.guideId === channel.guideId &&
          (old.proposal.guideSourceId || channel.sourceId) ===
            (channel.guideSourceId || channel.sourceId);
        const valid = beforeValid || afterValid;
        return {
          channel:
            valid && old.applied
              ? { ...channel, guideId: old.guideId ?? "", guideSourceId: old.guideSourceId ?? "" }
              : channel,
          decision: valid ? old.decision : "pending",
          checked: valid ? old.checked : false,
          proposal: valid ? old.proposal : undefined,
          applied: valid ? old.applied : false,
          matched: valid
            ? (old.matched ?? (coverage?.get(channel.id)?.programmes ?? 0) > 0)
            : (coverage?.get(channel.id)?.programmes ?? 0) > 0,
        } as ReviewRow;
      });
    setRows(next);
    rowsRef.current = next;
    setProgress({
      done: next.filter((r) => r.checked).length,
      total: next.filter((r) => !r.matched).length,
    });
    const controller = new AbortController();
    ctl.current = controller;
    const unresolved = next.filter((r) => !r.matched && !r.checked).map((r) => r.channel.id);
    // Assess one bounded batch for readiness. The displayed call/time estimate
    // is an upper bound for the full queue; cached and deterministic matches reduce it.
    tv.match(
      {
        ai: "estimate",
        guideSourceId: sourceId,
        channelIds: (unresolved.length ? unresolved : next.map((r) => r.channel.id)).slice(
          0,
          BATCH,
        ),
      },
      controller.signal,
    )
      .then((r) => {
        if (!controller.signal.aborted) {
          setConfigured(r.ai?.configured ?? false);
          setReady(true);
        }
      })
      .catch((e) => {
        if (!isAbort(e)) {
          setError(errorMessage(e));
          setReady(true);
        }
      });
  }, [open, storageKey, channels, coverage, sourceId]);
  useEffect(() => {
    if (!initialized.current || !rows.length) return;
    const timer = setTimeout(() => {
      const draft = Object.fromEntries(
        rows
          .filter((r) => r.checked || r.proposal || r.applied)
          .map((r) => [
            r.channel.id,
            {
              proposal: r.proposal,
              decision: r.decision,
              checked: r.checked,
              guideId: r.channel.guideId,
              guideSourceId: r.channel.guideSourceId,
              applied: r.applied,
              matched: r.matched,
            },
          ]),
      );
      try {
        sessionStorage.setItem(storageKey, JSON.stringify({ rows: draft, undoToken, undoIDs }));
      } catch {
        /* storage quota must never block review */
      }
    }, 250);
    return () => clearTimeout(timer);
  }, [rows, storageKey, undoToken, undoIDs]);
  const run = async () => {
    if (runningRef.current || !configured) return;
    runningRef.current = true;
    setRunning(true);
    setError("");
    const controller = new AbortController();
    ctl.current = controller;
    const pending = rowsRef.current
      .filter((r) => !r.matched && !r.checked)
      .map((r) => r.channel.id);
    const completedBefore = rowsRef.current.filter((r) => r.checked && !r.matched).length;
    setProgress({ done: completedBefore, total: completedBefore + pending.length });
    try {
      for (let i = 0; i < pending.length && i < BATCH * 32; i += BATCH) {
        if (controller.signal.aborted) break;
        const batch = pending.slice(i, i + BATCH);
        const result = await tv.match(
          { ai: "propose", guideSourceId: sourceId, channelIds: batch },
          controller.signal,
        );
        if (controller.signal.aborted) break;
        const proposals = new Map((result.changes ?? []).map((p) => [p.channelId, p])),
          ids = new Set(batch);
        setRows((previous) =>
          previous.map((row) => {
            if (!ids.has(row.channel.id)) return row;
            const proposal = proposals.get(row.channel.id);
            return {
              ...row,
              checked: true,
              proposal,
              decision:
                row.decision !== "pending"
                  ? row.decision
                  : highConfidence(proposal)
                    ? "accepted"
                    : "pending",
            };
          }),
        );
        setProgress({
          done: completedBefore + Math.min(i + BATCH, pending.length),
          total: completedBefore + pending.length,
        });
      }
    } catch (e) {
      if (!isAbort(e)) setError(errorMessage(e));
    } finally {
      if (ctl.current === controller) {
        runningRef.current = false;
        setRunning(false);
      }
    }
  };
  const counts = useMemo(
    () => ({
      matched: rows.filter((r) => r.matched || r.applied).length,
      unmatched: rows.filter((r) => !r.matched && !r.applied && !r.proposal).length,
      high: rows.filter((r) => !r.applied && highConfidence(r.proposal)).length,
      review: rows.filter((r) => r.proposal && !r.applied && !highConfidence(r.proposal)).length,
      accepted: rows.filter((r) => r.proposal && r.decision === "accepted" && !r.applied).length,
      pending: rows.filter((r) => !r.matched && !r.checked).length,
    }),
    [rows],
  );
  const sourceNames = useMemo(() => new Map(sources.map((s) => [s.id, s.name])), [sources]);
  const groups = useMemo(
    () => [...new Set(rows.map((r) => r.channel.group).filter((v): v is string => !!v))].sort(),
    [rows],
  );
  const regions = useMemo(
    () => [...new Set(rows.map((r) => regionOf(r.channel.name)).filter(Boolean))].sort(),
    [rows],
  );
  const filtered = useMemo(() => {
    const list = rows.filter((r) => {
      const p = r.proposal;
      return (
        (!search ||
          `${r.channel.name} ${r.channel.number} ${p?.guideName ?? ""}`
            .toLowerCase()
            .includes(search)) &&
        (band === "all" || (band === "high" ? highConfidence(p) : !!p && !highConfidence(p))) &&
        (source === "all" || (p?.guideSourceId ?? r.channel.sourceId) === source) &&
        (region === "all" || regionOf(r.channel.name) === region) &&
        (group === "all" || r.channel.group === group) &&
        (status === "all" || statusOf(r) === status)
      );
    });
    const collator = new Intl.Collator(locale, { numeric: true });
    return list.sort((a, b) =>
      sort === "confidence"
        ? (b.proposal?.confidence ?? (b.proposal ? 1 : -1)) -
          (a.proposal?.confidence ?? (a.proposal ? 1 : -1))
        : sort === "source"
          ? collator.compare(
              sourceNames.get(a.proposal?.guideSourceId ?? a.channel.sourceId ?? "") ?? "",
              sourceNames.get(b.proposal?.guideSourceId ?? b.channel.sourceId ?? "") ?? "",
            )
          : sort === "status"
            ? collator.compare(statusOf(a), statusOf(b))
            : collator.compare(a.channel.name, b.channel.name),
    );
  }, [rows, search, band, source, region, group, status, sort, sourceNames, locale]);
  const grid = useRef<HTMLDivElement>(null),
    [view, setView] = useState({ top: 0, height: 400 });
  useEffect(() => {
    if (!open) return;
    const el = grid.current;
    if (!el) return;
    const measure = () => setView({ top: el.scrollTop, height: el.clientHeight || 400 });
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => observer.disconnect();
  }, [open, ready]);
  useEffect(() => {
    setActive(0);
    if (grid.current) grid.current.scrollTop = 0;
    setView((v) => ({ ...v, top: 0 }));
  }, [search, band, source, region, group, status, sort]);
  const first = Math.max(0, Math.floor(view.top / ROW) - 3),
    last = Math.min(filtered.length, Math.ceil((view.top + view.height) / ROW) + 3);
  const visible = filtered.slice(first, last);
  const choose = useCallback(
    (id: string, decision: Decision) =>
      setRows((previous) =>
        previous.map((r) =>
          r.channel.id === id && r.proposal && !r.applied ? { ...r, decision } : r,
        ),
      ),
    [],
  );
  const bulk = (decision: Decision, high = false) => {
    const ids = new Set(
      (high ? rows : filtered)
        .filter((r) => r.proposal && !r.applied && (!high || highConfidence(r.proposal)))
        .map((r) => r.channel.id),
    );
    setRows((previous) => previous.map((r) => (ids.has(r.channel.id) ? { ...r, decision } : r)));
  };
  const move = (index: number) => {
    index = Math.max(0, Math.min(filtered.length - 1, index));
    setActive(index);
    const el = grid.current;
    if (el) {
      const top = index * ROW;
      if (top < el.scrollTop) el.scrollTop = top;
      else if (top + ROW > el.scrollTop + el.clientHeight)
        el.scrollTop = top + ROW - el.clientHeight;
    }
  };
  const keyboard = (event: KeyboardEvent) => {
    if (
      event.altKey ||
      event.ctrlKey ||
      event.metaKey ||
      /^(INPUT|TEXTAREA|SELECT)$/.test((event.target as HTMLElement).tagName)
    )
      return;
    const key = event.key.toLowerCase();
    if (!["j", "k", "a", "r", "arrowdown", "arrowup"].includes(key)) return;
    event.preventDefault();
    if (key === "j" || key === "arrowdown") move(active + 1);
    else if (key === "k" || key === "arrowup") move(active - 1);
    else {
      const row = filtered[active];
      if (row) choose(row.channel.id, key === "a" ? "accepted" : "rejected");
    }
  };
  const apply = async () => {
    const accepted = rowsRef.current.filter(
      (r) => r.proposal && r.decision === "accepted" && !r.applied,
    );
    if (!accepted.length) return;
    setBusy(true);
    setError("");
    try {
      const result = await tv.match({
        ai: "commit",
        guideSourceId: sourceId,
        selections: accepted.map(({ channel, proposal: p }) => ({
          channelId: channel.id,
          guideSourceId: p!.guideSourceId,
          guideId: p!.guideId,
          expectedGuideSourceId: channel.guideSourceId ?? "",
          expectedGuideId: channel.guideId ?? "",
          proposalKey: p!.proposalKey,
        })),
      });
      setUndoToken(result.undoToken ?? "");
      setUndoIDs(accepted.map((r) => r.channel.id));
      const ids = new Set(accepted.map((r) => r.channel.id));
      setRows((previous) =>
        previous.map((r) => (ids.has(r.channel.id) ? { ...r, applied: true } : r)),
      );
      toast.success(
        tn(
          result.matched,
          "{count} channel matched to a guide",
          "{count} channels matched to a guide",
        ),
      );
      await onApplied();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const undo = async () => {
    setBusy(true);
    setError("");
    try {
      await tv.match({ ai: "undo", undoToken });
      setUndoToken("");
      const ids = new Set(undoIDs);
      setUndoIDs([]);
      setRows((previous) =>
        previous.map((r) =>
          ids.has(r.channel.id) ? { ...r, applied: false, decision: "pending" } : r,
        ),
      );
      await onApplied();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    if (!changing) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      tv.guideChannels(guideQuery, controller.signal, 30, sourceId)
        .then((r) => {
          setGuides(r ?? []);
          setGuideError("");
        })
        .catch((e) => {
          if (!isAbort(e)) setGuideError(errorMessage(e));
        });
    }, 180);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [changing, guideQuery, sourceId]);
  const openChange = (r: ReviewRow) => {
    setChanging(r.channel.id);
    setGuideQuery(r.channel.name);
    setGuides([]);
    setGuideError("");
  };
  const useGuide = (g: GuideChannel) => {
    setRows((previous) =>
      previous.map((r) =>
        r.channel.id === changing
          ? {
              ...r,
              checked: true,
              decision: "accepted",
              proposal: {
                channelId: r.channel.id,
                channelName: r.channel.name,
                guideSourceId: g.sourceId,
                guideSourceName: g.sourceName,
                guideId: g.guideId,
                guideName: g.name,
                method: "manual",
              },
            }
          : r,
      ),
    );
    setChanging(null);
  };
  const selectClass =
    "min-w-0 rounded-lg border border-border bg-surface px-2 py-1.5 text-xs text-fg";
  const estimatedCalls = Math.ceil(counts.pending / BATCH);
  return (
    <Drawer
      open={open}
      onClose={close}
      title={t("Match with AI")}
      size="xl"
      stacked
      bodyClassName="flex min-h-0 flex-1 flex-col overflow-hidden p-0"
      footer={
        <div className="flex w-full flex-wrap items-center gap-2">
          <span className="mr-auto text-xs text-fg-muted">
            {t("Accepted")} · {formatNumber(counts.accepted)}
          </span>
          {undoToken ? (
            <Button variant="outline" disabled={busy || running} onClick={() => void undo()}>
              {t("Undo guide changes")}
            </Button>
          ) : null}
          <Button disabled={busy || running || !counts.accepted} onClick={() => void apply()}>
            {t("Apply {count}", { count: formatNumber(counts.accepted) })}
          </Button>
        </div>
      }
    >
      <div className="max-h-[52vh] shrink-0 space-y-3 overflow-y-auto border-b border-border px-4 py-3 sm:max-h-none sm:px-6">
        <p className="text-xs text-fg-muted">
          {t("AI suggestions need your review. Confidence is an estimate.")}
        </p>
        <div className="grid grid-cols-4 gap-2">
          {[
            [t("Already matched"), counts.matched],
            [t("Unmatched"), counts.unmatched],
            [t("High confidence"), counts.high],
            [t("Needs review"), counts.review],
          ].map(([label, count]) => (
            <div key={label} className="min-w-0 rounded-xl bg-surface p-2">
              <p className="text-lg font-semibold tabular-nums">{formatNumber(Number(count))}</p>
              <p className="text-[11px] leading-tight text-fg-muted">{label}</p>
            </div>
          ))}
        </div>
        {configured === false ? (
          <p className="text-xs text-fg-muted">
            {t("Set up AI in Integrations → LLM to match channels.")}{" "}
            <a href="/ether/services" className="text-primary">
              {t("Integrations")}
            </a>
          </p>
        ) : (
          <div className="flex flex-wrap items-center gap-2">
            <p className="min-w-0 flex-1 text-xs text-fg-muted">
              {t("Estimated calls: {calls} · up to {time}", {
                calls: formatNumber(estimatedCalls),
                time: duration(estimatedCalls * 20000),
              })}
            </p>
            {running ? (
              <Button size="sm" variant="outline" onClick={cancel}>
                {t("Pause matching")}
              </Button>
            ) : ready && !counts.pending ? (
              <Badge variant="success">{t("Completed")}</Badge>
            ) : (
              <Button
                size="sm"
                disabled={!ready || !configured || busy || !counts.pending}
                onClick={() => void run()}
              >
                <Wand2 size={13} />
                {rows.some((r) => r.checked) ? t("Resume matching") : t("Find suggestions")}
              </Button>
            )}
          </div>
        )}
        {running || progress.done > 0 ? (
          <div className="space-y-1" role="status">
            <div className="h-1 overflow-hidden rounded bg-bg-subtle">
              <div
                className="h-full bg-primary transition-all"
                style={{
                  width: `${progress.total ? (progress.done / progress.total) * 100 : 100}%`,
                }}
              />
            </div>
            <p className="text-[11px] text-fg-muted">
              {t("{done} of {total} channels checked", {
                done: formatNumber(progress.done),
                total: formatNumber(progress.total),
              })}
            </p>
          </div>
        ) : null}
        {error ? (
          <p role="alert" className="text-xs text-danger">
            {error}
          </p>
        ) : null}
        <div className="flex gap-2">
          <Input
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
            placeholder={t("Search channels or guides")}
            aria-label={t("Search channels or guides")}
            className="min-w-0 flex-1"
          />
          <Button
            size="sm"
            variant="outline"
            aria-expanded={filters}
            onClick={() => setFilters((v) => !v)}
          >
            <SlidersHorizontal size={14} />
            {t("Filters")}
          </Button>
        </div>
        {filters ? (
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
            <select
              aria-label={t("Confidence")}
              className={selectClass}
              value={band}
              onChange={(e) => setBand(e.target.value)}
            >
              <option value="all">{t("All confidence levels")}</option>
              <option value="high">{t("High confidence")}</option>
              <option value="review">{t("Needs review")}</option>
            </select>
            <select
              aria-label={t("Source")}
              className={selectClass}
              value={source}
              onChange={(e) => setSource(e.target.value)}
            >
              <option value="all">{t("All sources")}</option>
              {sources.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
            <select
              aria-label={t("Region")}
              className={selectClass}
              value={region}
              onChange={(e) => setRegion(e.target.value)}
            >
              <option value="all">{t("All regions")}</option>
              {regions.map((r) => (
                <option key={r}>{r}</option>
              ))}
            </select>
            <select
              aria-label={t("Group")}
              className={selectClass}
              value={group}
              onChange={(e) => setGroup(e.target.value)}
            >
              <option value="all">{t("All groups")}</option>
              {groups.map((g) => (
                <option key={g}>{g}</option>
              ))}
            </select>
            <select
              aria-label={t("Status")}
              className={selectClass}
              value={status}
              onChange={(e) => setStatus(e.target.value)}
            >
              <option value="all">{t("All statuses")}</option>
              <option value="pending">{t("Pending")}</option>
              <option value="accepted">{t("Accepted")}</option>
              <option value="rejected">{t("Rejected")}</option>
              <option value="matched">{t("Matched")}</option>
              <option value="unmatched">{t("Unmatched")}</option>
            </select>
            <select
              aria-label={t("Sort by")}
              className={selectClass}
              value={sort}
              onChange={(e) => setSort(e.target.value)}
            >
              <option value="name">{t("Name")}</option>
              <option value="confidence">{t("Confidence")}</option>
              <option value="source">{t("Source")}</option>
              <option value="status">{t("Status")}</option>
            </select>
          </div>
        ) : null}
        <div className="flex flex-wrap items-center gap-1.5">
          <Button
            size="sm"
            variant="outline"
            disabled={busy || !counts.high}
            onClick={() => bulk("accepted", true)}
          >
            {t("Accept high confidence")}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={busy || !filtered.some((r) => r.proposal && !r.applied)}
            onClick={() => bulk("accepted")}
          >
            {t("Accept filtered")}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={busy || !filtered.some((r) => r.proposal && !r.applied)}
            onClick={() => bulk("rejected")}
          >
            {t("Reject filtered")}
          </Button>
          <span className="ml-auto text-[11px] tabular-nums text-fg-muted">
            {tn(filtered.length, "{count} channel", "{count} channels")}
          </span>
        </div>
      </div>
      <div
        ref={grid}
        role="grid"
        aria-label={t("Guide match review")}
        aria-rowcount={filtered.length}
        aria-colcount={2}
        aria-activedescendant={
          active >= first && active < last
            ? `guide-review-${filtered[active]?.channel.id}`
            : undefined
        }
        tabIndex={0}
        onKeyDown={keyboard}
        onScroll={(e) => {
          const el = e.currentTarget;
          setView({ top: el.scrollTop, height: el.clientHeight });
        }}
        className="min-h-32 flex-1 overflow-auto outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary"
        data-guide-review-grid
      >
        <div style={{ height: filtered.length * ROW, position: "relative" }}>
          {visible.map((row, index) => {
            const position = first + index,
              p = row.proposal,
              currentStatus = statusOf(row);
            return (
              <div
                role="row"
                aria-rowindex={position + 1}
                key={row.channel.id}
                id={`guide-review-${row.channel.id}`}
                data-review-row
                onClick={() => setActive(position)}
                className={cn(
                  "absolute left-0 right-0 border-b border-border px-4 py-2 sm:px-6",
                  position === active ? "bg-primary-subtle/30" : "hover:bg-surface-hover",
                )}
                style={{ top: position * ROW, height: ROW }}
              >
                <div role="gridcell" className="flex items-start gap-2">
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-fg" title={row.channel.name}>
                      {row.channel.name}
                    </p>
                    <p className="truncate text-xs text-fg-muted" title={p?.guideName}>
                      {p ? (
                        <>
                          <ArrowRight size={11} className="mr-1 inline" />
                          {p.guideName}
                        </>
                      ) : row.matched ? (
                        t("Already matched")
                      ) : (
                        t("No suggestion yet")
                      )}
                    </p>
                    <p className="truncate text-[11px] text-fg-subtle">
                      {p?.guideSourceName ?? sourceNames.get(row.channel.sourceId ?? "")}
                      {row.channel.group ? ` · ${row.channel.group}` : ""}
                    </p>
                  </div>
                  {p ? (
                    <Badge variant={highConfidence(p) ? "success" : "warning"}>
                      {p.method === "manual"
                        ? t("Manual")
                        : t("{confidence}% confidence", {
                            confidence: Math.round((p.confidence ?? 1) * 100),
                          })}
                    </Badge>
                  ) : null}
                </div>
                <div role="gridcell" className="mt-2 flex items-center gap-1.5">
                  <Button
                    size="sm"
                    variant={row.decision === "accepted" ? "default" : "ghost"}
                    disabled={!p || row.applied || busy}
                    onClick={() => choose(row.channel.id, "accepted")}
                    aria-pressed={row.decision === "accepted"}
                    aria-label={t("Accept match for {channel}", { channel: row.channel.name })}
                  >
                    <Check size={13} />
                    {t("Accept")}
                  </Button>
                  <Button
                    size="sm"
                    variant={row.decision === "rejected" ? "outline" : "ghost"}
                    disabled={!p || row.applied || busy}
                    onClick={() => choose(row.channel.id, "rejected")}
                    aria-pressed={row.decision === "rejected"}
                    aria-label={t("Reject match for {channel}", { channel: row.channel.name })}
                  >
                    <X size={13} />
                    {t("Reject")}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={running || busy || row.applied}
                    onClick={() => openChange(row)}
                    aria-label={t("Change guide for {channel}", { channel: row.channel.name })}
                  >
                    {t("Change")}
                  </Button>
                  {p?.reason ? <Reason reason={p.reason} /> : null}
                  <span className="ml-auto text-[10px] text-fg-muted">
                    {currentStatus === "matched"
                      ? t("Matched")
                      : currentStatus === "accepted"
                        ? t("Accepted")
                        : currentStatus === "rejected"
                          ? t("Rejected")
                          : null}
                  </span>
                </div>
              </div>
            );
          })}
        </div>
        {!filtered.length ? (
          <p className="p-6 text-center text-sm text-fg-muted">{t("No channels match.")}</p>
        ) : null}
      </div>
      <p className="shrink-0 border-t border-border px-4 py-2 text-[11px] text-fg-subtle">
        {t("j/k move · a accept · r reject")}
      </p>
      <Drawer
        open={changing !== null}
        onClose={() => setChanging(null)}
        title={t("Change guide")}
        size="md"
        stacked
      >
        <Input
          aria-label={t("Search guide channels")}
          value={guideQuery}
          onChange={(e) => setGuideQuery(e.currentTarget.value)}
          autoFocus
        />
        {guideError ? (
          <p role="alert" className="mt-2 text-xs text-danger">
            {guideError}
          </p>
        ) : null}
        <ul className="mt-3 space-y-1">
          {guides.map((g) => (
            <li key={`${g.sourceId}/${g.guideId}`}>
              <button
                className="w-full rounded-lg border border-border p-3 text-left hover:bg-surface-hover"
                onClick={() => useGuide(g)}
              >
                <span className="block text-sm font-medium">{g.name}</span>
                <span className="text-xs text-fg-muted">{g.sourceName}</span>
              </button>
            </li>
          ))}
        </ul>
      </Drawer>
    </Drawer>
  );
}
function Reason({ reason }: { reason: string }) {
  const [open, setOpen] = useState(false);
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      className="max-w-72 p-3 text-xs"
      trigger={
        <Button
          size="sm"
          variant="ghost"
          aria-label={t("Why this match")}
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          <Info size={14} />
        </Button>
      }
    >
      {reason}
    </Popover>
  );
}
