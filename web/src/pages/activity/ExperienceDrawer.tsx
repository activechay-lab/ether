import { Badge, cn, useElementSize } from "@braedonsaunders/appkit-ui";
import { AlertTriangle, ArrowUpRight, Lightbulb, MapPin } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { getLocale, t, tn } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { qualityFallback } from "../../lib/quality-fallback";
import { relativeTime } from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import {
  BUFFER_GOOD_PER_HOUR,
  DASH,
  START_GOOD_MS,
  TONE_DOT,
  TONE_TEXT,
  bufferTone,
  causeSentence,
  decisionLabel,
  directShare,
  failedReason,
  failedTone,
  formatHours,
  formatMbps,
  formatPerHour,
  formatSeconds,
  pct,
  reasonPhrase,
  startTone,
  type Tone,
} from "./experience-format";
import type { ExperienceMetrics, ExperiencePlay, ExperienceViewer } from "./types";

/** A value coloured against its target, with a dot so colour is not the only cue. */
export function ToneValue({
  value,
  tone,
  dot = true,
}: {
  value: string;
  tone?: Tone;
  /** Tiles already carry the tone in their accent; they colour the value only. */
  dot?: boolean;
}) {
  return (
    <span className={cn("inline-flex items-center gap-1.5 tabular-nums", tone && TONE_TEXT[tone])}>
      {tone && dot ? (
        <span aria-hidden className={cn("size-1.5 rounded-full", TONE_DOT[tone])} />
      ) : null}
      {value}
    </span>
  );
}

type Point = { day: string; value: number | null };

/**
 * A small daily line with the target drawn as a dashed rule. Days without a
 * measurement leave a gap rather than inventing a value.
 */
function Sparkline({
  title,
  points,
  target,
  format,
  tone,
}: {
  title: string;
  points: Point[];
  target: number;
  format: (v: number) => string;
  tone?: (v: number) => Tone | undefined;
}) {
  const [ref, size] = useElementSize<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const width = Math.max(160, Math.round(size.width || 280));
  const height = 56;
  const pad = 4;
  const values = points.map((p) => p.value).filter((v): v is number => v != null);
  const max = Math.max(target * 1.5, ...values) || 1;
  const x = (i: number) =>
    points.length > 1 ? pad + (i / (points.length - 1)) * (width - pad * 2) : width / 2;
  const y = (v: number) => height - pad - (v / max) * (height - pad * 2);
  const segments: string[] = [];
  let run = "";
  points.forEach((p, i) => {
    if (p.value == null) {
      if (run) segments.push(run);
      run = "";
      return;
    }
    run += `${run ? "L" : "M"}${x(i).toFixed(1)},${y(p.value).toFixed(1)}`;
  });
  if (run) segments.push(run);
  const last = [...points].reverse().find((p) => p.value != null);
  const shown = hover != null ? points[hover] : last;
  const dayLabel = (day: string) =>
    new Date(`${day}T00:00:00Z`).toLocaleDateString(getLocale(), {
      month: "short",
      day: "numeric",
      timeZone: "UTC",
    });
  const lastTone = shown?.value != null ? tone?.(shown.value) : undefined;
  return (
    <div className="min-w-0 rounded-lg border border-border bg-bg-subtle/40 p-3">
      <div className="flex items-baseline justify-between gap-2">
        <p className="text-xs font-medium text-fg-muted">{title}</p>
        <p className="text-xs text-fg-muted">
          {shown ? (
            <>
              <span className="mr-1">{dayLabel(shown.day)}</span>
              <ToneValue value={shown.value == null ? DASH : format(shown.value)} tone={lastTone} />
            </>
          ) : (
            DASH
          )}
        </p>
      </div>
      <div ref={ref} className="mt-2">
        {values.length ? (
          <svg
            viewBox={`0 0 ${width} ${height}`}
            width="100%"
            height={height}
            role="img"
            aria-label={t("{title} by day", { title })}
            className="block overflow-visible"
            onMouseLeave={() => setHover(null)}
          >
            <line
              x1={pad}
              x2={width - pad}
              y1={y(target)}
              y2={y(target)}
              className="stroke-success"
              strokeOpacity={0.6}
              strokeDasharray="3 3"
              vectorEffect="non-scaling-stroke"
            />
            {segments.map((d, i) => (
              <path
                key={i}
                d={d}
                fill="none"
                className="stroke-primary"
                strokeWidth={1.75}
                strokeLinejoin="round"
                strokeLinecap="round"
                vectorEffect="non-scaling-stroke"
              />
            ))}
            {points.map((p, i) =>
              p.value == null ? null : (
                <circle
                  key={p.day}
                  cx={x(i)}
                  cy={y(p.value)}
                  r={hover === i ? 3.5 : 2}
                  className="fill-primary"
                />
              ),
            )}
            {points.map((p, i) => (
              <rect
                key={`hit-${p.day}`}
                x={x(i) - (width - pad * 2) / Math.max(1, points.length) / 2}
                y={0}
                width={(width - pad * 2) / Math.max(1, points.length)}
                height={height}
                fill="transparent"
                onMouseEnter={() => setHover(i)}
              />
            ))}
          </svg>
        ) : (
          <p className="grid h-14 place-items-center text-xs text-fg-subtle">
            {t("Nothing measured in this period")}
          </p>
        )}
      </div>
      {points.length ? (
        <div className="mt-1 flex justify-between text-[10px] text-fg-subtle">
          <span>{dayLabel(points[0]!.day)}</span>
          <span>{t("Target {value}", { value: format(target) })}</span>
          <span>{dayLabel(points[points.length - 1]!.day)}</span>
        </div>
      ) : null}
    </div>
  );
}

export function Fact({
  label,
  children,
  hint,
}: {
  label: string;
  children: React.ReactNode;
  hint?: string;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-fg-muted">{label}</dt>
      <dd className="mt-0.5 text-sm font-medium text-fg">{children}</dd>
      {hint ? <dd className="text-xs text-fg-subtle">{hint}</dd> : null}
    </div>
  );
}

/** The four headline measures plus quality, decisions and endings, compactly. */
export function MetricFacts({ m, wide = false }: { m: ExperienceMetrics; wide?: boolean }) {
  const direct = directShare(m);
  const total = m.decisions.directplay + m.decisions.copy + m.decisions.transcode;
  return (
    <dl
      className={cn(
        "grid grid-cols-2 gap-x-4 gap-y-3",
        wide ? "sm:grid-cols-3 lg:grid-cols-5" : "sm:grid-cols-3",
      )}
    >
      <Fact
        label={t("Start time")}
        hint={
          m.startP90Ms != null ? t("p90 {time}", { time: formatSeconds(m.startP90Ms) }) : undefined
        }
      >
        <ToneValue value={formatSeconds(m.startMedianMs)} tone={startTone(m.startMedianMs)} />
      </Fact>
      <Fact
        label={t("Buffering")}
        hint={
          m.measuredPlays
            ? t("{minutes} min in total", { minutes: Math.round(m.bufferMs / 60000) })
            : undefined
        }
      >
        <ToneValue
          value={formatPerHour(m.bufferEventsPerHour)}
          tone={bufferTone(m.bufferEventsPerHour)}
        />
      </Fact>
      <Fact
        label={t("Failed starts")}
        hint={
          m.measuredPlays ? tn(m.measuredPlays, "of {count} play", "of {count} plays") : undefined
        }
      >
        <ToneValue
          value={m.measuredPlays ? String(m.failedStarts) : DASH}
          tone={failedTone(m.failedStarts, m.measuredPlays)}
        />
      </Fact>
      <Fact
        label={t("Direct play")}
        hint={
          total
            ? t("{stream} direct stream, {transcode} transcode", {
                stream: pct((m.decisions.copy / total) * 100),
                transcode: pct((m.decisions.transcode / total) * 100),
              })
            : undefined
        }
      >
        {pct(direct)}
      </Fact>
      <Fact
        label={t("Bitrate delivered")}
        hint={
          m.sourceKbps != null ? t("source {rate}", { rate: formatMbps(m.sourceKbps) }) : undefined
        }
      >
        {formatMbps(m.deliveredKbps)}
      </Fact>
      <Fact label={t("4K / HDR time")}>
        {t("{uhd} / {hdr}", { uhd: pct(m.uhdPct), hdr: pct(m.hdrPct) })}
      </Fact>
      <Fact
        label={t("Source switches")}
        hint={
          m.downSwitches ? tn(m.downSwitches, "{count} for speed", "{count} for speed") : undefined
        }
      >
        {String(m.switches)}
      </Fact>
      <Fact label={t("Finished / left early")} hint={t("left early = within 2 minutes")}>
        {t("{finished} / {abandoned}", { finished: m.finished, abandoned: m.abandoned })}
      </Fact>
      <Fact label={t("Away from home")}>{pct(m.remotePct)}</Fact>
      <Fact
        label={t("Most-used device")}
        hint={m.topDevice ? tn(m.topDevice.plays, "{count} play", "{count} plays") : undefined}
      >
        <span className="block truncate">{m.topDevice?.name || DASH}</span>
      </Fact>
      <Fact label={t("Hours watched")}>{formatHours(m.watchedMs)}</Fact>
      <Fact label={t("Top transcode reason")}>
        {m.topTranscodeReason ? (
          <span className="block first-letter:uppercase">
            {reasonPhrase(m.topTranscodeReason.kind, m.topTranscodeReason.detail)}
          </span>
        ) : (
          DASH
        )}
      </Fact>
    </dl>
  );
}

function PlayRow({ p }: { p: ExperiencePlay }) {
  const decision = decisionLabel(p.decision);
  const why = p.decision === "directplay" ? "" : reasonPhrase(p.reasonKind, p.reasonDetail);
  return (
    <li className="space-y-1.5 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium text-fg">{p.title}</p>
          <p className="truncate text-xs text-fg-muted">
            {[
              p.device || p.platform || t("Unknown device"),
              relativeTime(new Date(p.started * 1000)),
            ]
              .filter(Boolean)
              .join(" · ")}
          </p>
        </div>
        <Link
          to={`/activity/history?play=${encodeURIComponent(p.id)}`}
          className="inline-flex shrink-0 items-center gap-0.5 text-xs font-medium text-primary hover:underline"
        >
          {t("In History")} <ArrowUpRight size={12} />
        </Link>
      </div>
      {p.qualityFallback ? (
        <p className="text-xs text-fg-muted">{qualityFallback(p.qualityFallback)}</p>
      ) : null}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-fg-muted">
        {p.failed ? (
          <span className="inline-flex items-center gap-1 text-danger">
            <AlertTriangle size={12} /> {t("Never started")}
          </span>
        ) : (
          <span>
            {t("Start")}{" "}
            <ToneValue
              value={p.measured ? formatSeconds(p.startMs) : DASH}
              tone={p.measured ? startTone(p.startMs) : undefined}
            />
          </span>
        )}
        {p.failed ? null : (
          <span>
            {t("Buffering")}{" "}
            {p.measured && p.bufferEvents != null ? (
              <span className="tabular-nums text-fg">
                {p.bufferEvents
                  ? tn(p.bufferEvents, "{count} time, {time}", "{count} times, {time}", {
                      time: formatSeconds(p.bufferMs),
                    })
                  : t("none")}
              </span>
            ) : (
              DASH
            )}
          </span>
        )}
        {decision ? (
          <Badge variant={p.decision === "transcode" ? "warning" : "secondary"}>{decision}</Badge>
        ) : null}
        {p.origin ? <Badge variant="secondary">{p.origin}</Badge> : null}
        {p.remote ? (
          <span className="inline-flex items-center gap-0.5">
            <MapPin size={11} /> {t("Away")}
          </span>
        ) : null}
        {p.switches ? <span>{tn(p.switches, "{count} switch", "{count} switches")}</span> : null}
      </div>
      {p.failed && p.error ? <p className="text-xs text-danger">{failedReason(p.error)}</p> : null}
      {why ? (
        <p className="text-xs text-fg-subtle">
          {t("Converted because of {reason}", { reason: why })}
        </p>
      ) : null}
    </li>
  );
}

/** One viewer's experience: likely cause, trend, measures and recent plays. */
export function ExperienceDrawer({
  viewerId,
  days,
  name,
  onClose,
}: {
  viewerId: number | null;
  days: number;
  name?: string;
  onClose: () => void;
}) {
  const detail = useAsync(
    (signal) =>
      viewerId == null
        ? Promise.resolve(null)
        : apiFetch<ExperienceViewer>(`/experience/viewers/${viewerId}?days=${days}`, { signal }),
    [viewerId, days],
  );
  const d = detail.data && detail.data.userId === viewerId ? detail.data : null;
  return (
    <Drawer
      open={viewerId != null}
      onClose={onClose}
      size="lg"
      title={d?.username || name || t("Viewer")}
      description={tn(
        days,
        "Experience over the last {count} day",
        "Experience over the last {count} days",
      )}
    >
      {detail.error && !d ? (
        <ErrorPanel error={detail.error} onRetry={() => void detail.reload()} />
      ) : !d ? (
        <LoadingRows rows={5} />
      ) : (
        <div className="space-y-6">
          <section
            aria-label={t("Most likely cause")}
            className="flex gap-3 rounded-lg border border-border bg-bg-subtle/60 p-4"
          >
            <Lightbulb size={18} className="mt-0.5 shrink-0 text-primary" />
            <div className="min-w-0">
              <h3 className="text-xs font-semibold uppercase tracking-wide text-fg-muted">
                {t("Most likely cause")}
              </h3>
              <p className="mt-1 text-sm text-fg">{causeSentence(d.cause, d.userId)}</p>
            </div>
          </section>

          <section aria-label={t("Trend")} className="grid gap-3 sm:grid-cols-2">
            <Sparkline
              title={t("Start time")}
              points={d.trend.map((x) => ({ day: x.day, value: x.startMedianMs }))}
              target={START_GOOD_MS}
              format={(v) => formatSeconds(v)}
              tone={startTone}
            />
            <Sparkline
              title={t("Buffering per hour")}
              points={d.trend.map((x) => ({ day: x.day, value: x.bufferEventsPerHour }))}
              target={BUFFER_GOOD_PER_HOUR}
              format={(v) => formatPerHour(v)}
              tone={bufferTone}
            />
          </section>

          <section aria-label={t("Measures")}>
            <MetricFacts m={d.metrics} />
            {d.metrics.failedReasons.length ? (
              <div className="mt-4">
                <p className="text-xs font-medium text-fg-muted">{t("Why starts failed")}</p>
                <ul className="mt-1 space-y-0.5 text-sm">
                  {d.metrics.failedReasons.map((r) => (
                    <li key={r.reason} className="flex justify-between gap-3">
                      <span className="min-w-0 truncate text-fg">{failedReason(r.reason)}</span>
                      <span className="tabular-nums text-fg-muted">{r.count}</span>
                    </li>
                  ))}
                </ul>
              </div>
            ) : null}
          </section>

          <section aria-label={t("Recent plays")}>
            <h3 className="text-sm font-semibold text-fg">{t("Last 20 plays")}</h3>
            {d.plays.length ? (
              <ul className="divide-y divide-border">
                {d.plays.map((p) => (
                  <PlayRow key={p.id} p={p} />
                ))}
              </ul>
            ) : (
              <p className="py-4 text-sm text-fg-muted">{t("No recorded plays yet.")}</p>
            )}
          </section>
        </div>
      )}
    </Drawer>
  );
}
