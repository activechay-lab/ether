import { cn, useElementSize } from "@braedonsaunders/appkit-ui";
import { useId, useMemo, useState } from "react";
import { getLocale, t } from "../i18n";
import { fixed, formatHour, formatPercent } from "../lib/format";

/**
 * Small token-driven charts. Each is single-series (identity is named by the
 * chart title), uses thin marks with rounded data ends anchored to the
 * baseline, recessive gridlines, text in text tokens, and a hover/focus
 * tooltip. Colors come from semantic tokens so light and dark both work.
 */

function roundedTopBar(x: number, y: number, w: number, h: number, r: number): string {
  if (h <= 0) return "";
  const rr = Math.min(r, w / 2, h);
  return `M${x},${y + h}V${y + rr}Q${x},${y} ${x + rr},${y}H${x + w - rr}Q${x + w},${y} ${x + w},${y + rr}V${y + h}Z`;
}

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(v));
  const n = v / p;
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10;
  return step * p;
}

interface BarDatum {
  key: string;
  label: string;
  value: number;
  detail?: string;
}

/** Vertical bars over time (e.g. plays per day). */
export function TimeBars({
  data,
  height: fixedHeight = 160,
  fill = false,
  ariaLabel,
  valueFormat = (v) => String(v),
  className,
}: {
  data: BarDatum[];
  height?: number;
  /** Take the container's height instead of `height` (give it a definite height). */
  fill?: boolean;
  ariaLabel: string;
  valueFormat?: (v: number) => string;
  className?: string;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const [ref, size] = useElementSize<HTMLDivElement>();
  const width = Math.max(240, Math.round(size.width || 600));
  const height = fill ? Math.max(96, Math.round(size.height || fixedHeight)) : fixedHeight;
  const padL = 32;
  const padB = 22;
  const padT = 8;
  const max = niceMax(Math.max(0, ...data.map((d) => d.value)));
  const plotH = height - padB - padT;
  const slot = data.length ? (width - padL) / data.length : 0;
  const gap = Math.min(2, slot * 0.25);
  const barW = Math.max(1, slot - gap);
  const ticks = [0, max / 2, max];
  const labelIdx = data.length ? [0, Math.floor((data.length - 1) / 2), data.length - 1] : [];
  const h = hover !== null ? data[hover] : undefined;
  return (
    <div ref={ref} className={cn("relative", fill && "h-full min-h-0", className)}>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        width="100%"
        height={height}
        className={cn("block overflow-visible", fill && "absolute inset-0")}
        role="img"
        aria-label={ariaLabel}
        onMouseLeave={() => setHover(null)}
      >
        {ticks.map((tick) => {
          const y = padT + plotH - (tick / max) * plotH;
          return (
            <g key={tick}>
              <line
                x1={padL}
                x2={width}
                y1={y}
                y2={y}
                className="stroke-border-subtle"
                strokeWidth={1}
                vectorEffect="non-scaling-stroke"
              />
              <text
                x={padL - 6}
                y={y + 3}
                textAnchor="end"
                className="fill-fg-subtle text-[10px] tabular-nums"
              >
                {valueFormat(tick)}
              </text>
            </g>
          );
        })}
        {data.map((d, i) => {
          const bh = (d.value / max) * plotH;
          const x = padL + i * slot + gap / 2;
          return (
            <g key={d.key}>
              <rect
                x={padL + i * slot}
                y={padT}
                width={slot}
                height={plotH}
                fill="transparent"
                onMouseEnter={() => setHover(i)}
                onFocus={() => setHover(i)}
                onBlur={() => setHover(null)}
                tabIndex={0}
                aria-label={`${d.label}: ${valueFormat(d.value)}${d.detail ? `, ${d.detail}` : ""}`}
                className="focus:outline-none"
              />
              <path
                d={roundedTopBar(x, padT + plotH - bh, barW, bh, 4)}
                className={cn(
                  "pointer-events-none transition-opacity",
                  hover === null || hover === i ? "fill-primary" : "fill-primary opacity-40",
                )}
              />
            </g>
          );
        })}
        <line
          x1={padL}
          x2={width}
          y1={padT + plotH}
          y2={padT + plotH}
          className="stroke-border"
          strokeWidth={1}
          vectorEffect="non-scaling-stroke"
        />
        {labelIdx.map((i, n) => {
          const d = data[i];
          if (!d || (n > 0 && i === labelIdx[n - 1])) return null;
          const x = padL + i * slot + slot / 2;
          return (
            <text
              key={`l${i}`}
              x={x}
              y={height - 6}
              textAnchor={n === 0 ? "start" : n === 2 ? "end" : "middle"}
              className="fill-fg-subtle text-[10px]"
            >
              {d.label}
            </text>
          );
        })}
      </svg>
      {h && hover !== null ? (
        <div
          className="pointer-events-none absolute top-0 z-10 -translate-x-1/2 whitespace-nowrap rounded-md border border-border bg-elevated px-2.5 py-1.5 text-xs shadow-md"
          style={{ left: `${((padL + hover * slot + slot / 2) / width) * 100}%` }}
        >
          <div className="font-medium text-fg">{h.label}</div>
          <div className="tabular-nums text-fg-muted">
            {valueFormat(h.value)}
            {h.detail ? ` · ${h.detail}` : ""}
          </div>
        </div>
      ) : null}
    </div>
  );
}

interface WeightDatum {
  key: string;
  label: string;
  weight: number;
  hint?: string;
}

/**
 * Horizontal diverging bars for weights in -1..1: likes extend right in the
 * brand hue, dislikes extend left in a contrasting categorical hue, with a
 * neutral zero line. Values are printed in text tokens.
 */
export function DivergingBars({
  data,
  ariaLabel,
  onSelect,
  selectedKey,
}: {
  data: WeightDatum[];
  ariaLabel: string;
  onSelect?: (d: WeightDatum) => void;
  selectedKey?: string;
}) {
  return (
    <ul className="space-y-1.5" aria-label={ariaLabel}>
      {data.map((d) => {
        const w = Math.max(-1, Math.min(1, d.weight));
        const pct = Math.abs(w) * 50;
        const Row = onSelect ? "button" : "div";
        return (
          <li key={d.key}>
            <Row
              {...(onSelect ? { type: "button" as const, onClick: () => onSelect(d) } : {})}
              title={d.hint}
              className={cn(
                "grid w-full grid-cols-[minmax(5rem,9rem)_1fr_2.75rem] items-center gap-2 rounded-md px-1 py-0.5 text-left",
                onSelect &&
                  "hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                selectedKey === d.key && "bg-primary-subtle",
              )}
            >
              <span className="truncate text-sm text-fg">{d.label}</span>
              <span className="relative h-3" aria-hidden>
                <span className="absolute inset-y-0 left-1/2 w-px bg-border-strong" />
                <span
                  className={cn(
                    "absolute inset-y-0.5",
                    w >= 0 ? "left-1/2 rounded-r bg-primary" : "right-1/2 rounded-l bg-accent-rose",
                  )}
                  style={{ width: `${pct}%` }}
                />
              </span>
              <span className="text-right text-xs tabular-nums text-fg-muted">
                {w > 0 ? "+" : ""}
                {fixed(w, 2)}
              </span>
            </Row>
          </li>
        );
      })}
    </ul>
  );
}

/** Sequential single-hue columns (e.g. decades), magnitude by height. */
export function Columns({
  data,
  ariaLabel,
  height = 96,
}: {
  data: { key: string; label: string; value: number; hint?: string }[];
  ariaLabel: string;
  height?: number;
}) {
  const max = Math.max(0.0001, ...data.map((d) => Math.abs(d.value)));
  const [hover, setHover] = useState<string | null>(null);
  return (
    <div role="img" aria-label={ariaLabel} className="flex items-end gap-1.5" style={{ height }}>
      {data.map((d) => {
        const v = Math.max(0, d.value);
        const pct = (v / max) * 100;
        return (
          <div
            key={d.key}
            className="flex h-full min-w-0 flex-1 flex-col items-center gap-1"
            onMouseEnter={() => setHover(d.key)}
            onMouseLeave={() => setHover(null)}
            title={d.hint}
          >
            <div className="relative flex w-full flex-1 items-end">
              {hover === d.key ? (
                <span className="absolute -top-5 left-1/2 -translate-x-1/2 text-[10px] tabular-nums text-fg-muted">
                  {fixed(d.value, 2)}
                </span>
              ) : null}
              <div
                className={cn(
                  "w-full rounded-t bg-primary transition-opacity",
                  d.value <= 0 && "bg-border",
                )}
                style={{
                  height: `${Math.max(pct, d.value <= 0 ? 4 : 2)}%`,
                  opacity: d.value <= 0 ? 1 : 0.35 + 0.65 * (v / max),
                }}
              />
            </div>
            <span className="truncate text-[10px] text-fg-subtle">{d.label}</span>
          </div>
        );
      })}
    </div>
  );
}

/** 24 cells, one per hour; peak hours filled. */
export function HourStrip({ peaks, ariaLabel }: { peaks: number[]; ariaLabel: string }) {
  const set = useMemo(() => new Set(peaks.map((h) => ((h % 24) + 24) % 24)), [peaks]);
  return (
    <div
      role="img"
      aria-label={`${ariaLabel}: ${peaks.length ? peaks.map(formatHour).join(", ") : t("no clear peak")}`}
    >
      <div className="grid grid-cols-24 gap-0.5">
        {Array.from({ length: 24 }, (_, h) => (
          <div
            key={h}
            title={formatHour(h)}
            className={cn(
              "h-5 rounded-sm",
              set.has(h) ? "bg-primary" : "bg-bg-subtle ring-1 ring-inset ring-border-subtle",
            )}
          />
        ))}
      </div>
      <div className="mt-1 flex justify-between text-[10px] text-fg-subtle">
        <span>{formatHour(0)}</span>
        <span>{formatHour(6)}</span>
        <span>{formatHour(12)}</span>
        <span>{formatHour(18)}</span>
        <span>{formatHour(24)}</span>
      </div>
    </div>
  );
}

/** Thin horizontal meter (0..1) with a label and value. */
export function Meter({
  value,
  buffered,
  label,
  valueLabel,
  tone = "primary",
  onScrim = false,
}: {
  value: number;
  /** 0..1 read-ahead already downloaded beyond the playhead. */
  buffered?: number;
  label?: string;
  valueLabel?: string;
  tone?: "primary" | "warning" | "danger";
  /** Drawn over artwork: text and track use the on-scrim colour. */
  onScrim?: boolean;
}) {
  const id = useId();
  const v = Math.max(0, Math.min(1, Number.isFinite(value) ? value : 0));
  const b = Math.max(v, Math.min(1, Number.isFinite(buffered ?? NaN) ? (buffered as number) : v));
  return (
    <div>
      {label || valueLabel ? (
        <div className="mb-1 flex items-baseline justify-between gap-2 text-xs">
          <span id={id} className={onScrim ? "text-on-scrim/80" : "text-fg-muted"}>
            {label}
          </span>
          <span className={cn("tabular-nums", onScrim ? "text-on-scrim" : "text-fg")}>
            {valueLabel ?? formatPercent(v)}
          </span>
        </div>
      ) : null}
      <div
        className={cn(
          "relative h-1.5 overflow-hidden rounded-full",
          onScrim ? "bg-on-scrim/25" : "bg-bg-subtle ring-1 ring-inset ring-border-subtle",
        )}
        role="meter"
        aria-labelledby={label ? id : undefined}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(v * 100)}
        aria-valuetext={valueLabel}
      >
        {b > v && (
          <div
            className={cn(
              "absolute inset-y-0 left-0 rounded-full transition-[width] duration-500",
              onScrim ? "bg-on-scrim/45" : "bg-primary/30",
            )}
            style={{ width: `${b * 100}%` }}
          />
        )}
        <div
          className={cn(
            "relative h-full rounded-full transition-[width] duration-500",
            tone === "primary" ? "bg-primary" : tone === "warning" ? "bg-warning" : "bg-danger",
          )}
          style={{ width: `${v * 100}%` }}
        />
      </div>
    </div>
  );
}

/** The validated chart palette (styles.css --viz-1..5), as literal classes. */
export type Viz = "viz-1" | "viz-2" | "viz-3" | "viz-4" | "viz-5";
const VIZ: Record<Viz, { bg: string; fill: string; stroke: string }> = {
  "viz-1": { bg: "bg-viz-1", fill: "fill-viz-1", stroke: "stroke-viz-1" },
  "viz-2": { bg: "bg-viz-2", fill: "fill-viz-2", stroke: "stroke-viz-2" },
  "viz-3": { bg: "bg-viz-3", fill: "fill-viz-3", stroke: "stroke-viz-3" },
  "viz-4": { bg: "bg-viz-4", fill: "fill-viz-4", stroke: "stroke-viz-4" },
  "viz-5": { bg: "bg-viz-5", fill: "fill-viz-5", stroke: "stroke-viz-5" },
};
/** Categorical order for parts with no fixed colour of their own. */
const VIZ_ORDER: Viz[] = ["viz-4", "viz-2", "viz-3", "viz-5", "viz-1"];

interface RankDatum {
  key: string;
  label: string;
  value: number;
  /** Shown after the value, e.g. "12 plays". */
  detail?: string;
  /** Optional leading visual (poster, avatar). */
  leading?: React.ReactNode;
}

/**
 * Ranked horizontal bars, largest first, one hue: for "top" lists where the
 * reader compares parts with each other. Bars share one baseline.
 */
export function RankedBars({
  data,
  valueFormat = (v) => String(v),
  empty,
  ariaLabel,
  color = "viz-1",
}: {
  data: RankDatum[];
  valueFormat?: (v: number) => string;
  empty?: React.ReactNode;
  ariaLabel: string;
  color?: Viz;
}) {
  const rows = [...data].sort((a, b) => b.value - a.value);
  const max = Math.max(0, ...rows.map((r) => r.value));
  if (!rows.length) return <>{empty ?? null}</>;
  return (
    <ol className="space-y-2.5" aria-label={ariaLabel}>
      {rows.map((r) => (
        <li key={r.key} className="flex items-center gap-3">
          {r.leading ? <span className="shrink-0">{r.leading}</span> : null}
          <div className="min-w-0 flex-1">
            <div className="flex items-baseline justify-between gap-3 text-sm">
              <span className="truncate text-fg" title={r.label}>
                {r.label}
              </span>
              <span className="shrink-0 text-xs tabular-nums text-fg-muted">
                <span className="font-medium text-fg">{valueFormat(r.value)}</span>
                {r.detail ? ` · ${r.detail}` : ""}
              </span>
            </div>
            <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-bg-subtle">
              <div
                className={cn("h-full rounded-full", VIZ[color].bg)}
                style={{ width: `${max > 0 ? Math.max(2, (r.value / max) * 100) : 0}%` }}
              />
            </div>
          </div>
        </li>
      ))}
    </ol>
  );
}

interface SplitDatum {
  key: string;
  label: string;
  value: number;
  /** Fixed colour for this part, so it matches the same entity elsewhere. */
  color?: Viz;
}

/**
 * A tall segmented bar for one whole split into at most six parts: each
 * part's share printed on it where it fits, and a legend of names and values.
 * Parts beyond six fold into "Other".
 */
export function SplitBar({
  data,
  valueFormat = (v) => String(v),
  ariaLabel,
}: {
  data: SplitDatum[];
  valueFormat?: (v: number) => string;
  ariaLabel: string;
}) {
  const sorted = [...data].filter((d) => d.value > 0).sort((a, b) => b.value - a.value);
  const parts =
    sorted.length > 6
      ? [
          ...sorted.slice(0, 5),
          {
            key: "other",
            label: t("Other"),
            value: sorted.slice(5).reduce((n, d) => n + d.value, 0),
          } as SplitDatum,
        ]
      : sorted;
  const total = parts.reduce((n, d) => n + d.value, 0);
  if (!total) return <p className="text-sm text-fg-muted">{t("Nothing in this period.")}</p>;
  let next = 0;
  const colorOf = parts.map((p) =>
    p.key === "other"
      ? "bg-fg-subtle/40"
      : VIZ[p.color ?? VIZ_ORDER[next++ % VIZ_ORDER.length]!].bg,
  );
  return (
    <div>
      <div
        className="flex h-9 gap-0.5 overflow-hidden rounded-lg"
        role="img"
        aria-label={`${ariaLabel}: ${parts
          .map((p) => `${p.label} ${formatPercent(p.value / total)}`)
          .join(", ")}`}
      >
        {parts.map((p, i) => {
          const share = p.value / total;
          return (
            <div
              key={p.key}
              title={`${p.label}: ${valueFormat(p.value)} (${formatPercent(share)})`}
              className={cn(
                "flex min-w-0 items-center justify-center first:rounded-l-lg last:rounded-r-lg",
                colorOf[i],
              )}
              style={{ width: `${share * 100}%` }}
            >
              {share >= 0.1 ? (
                <span className="truncate px-1 text-xs font-semibold tabular-nums text-white">
                  {formatPercent(share)}
                </span>
              ) : null}
            </div>
          );
        })}
      </div>
      <ul className="mt-3 flex flex-wrap gap-x-5 gap-y-1.5 text-xs">
        {parts.map((p, i) => (
          <li key={p.key} className="flex items-center gap-1.5">
            <span className={cn("size-2.5 rounded-sm", colorOf[i])} />
            <span className="text-fg">{p.label}</span>
            <span className="tabular-nums text-fg-muted">
              {valueFormat(p.value)} · {formatPercent(p.value / total)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

interface HeatCell {
  weekday: number;
  hour: number;
  value: number;
}

/** One hue (blue) that deepens with the value. */
const HEAT_STEPS = ["bg-viz-4/20", "bg-viz-4/40", "bg-viz-4/60", "bg-viz-4/80", "bg-viz-4"];

/**
 * When something happens across the week: weekdays down, hours across, one
 * hue that deepens with the value. Hover a cell for its figure.
 */
export function WeekHeatmap({
  cells,
  valueFormat = (v) => String(v),
  ariaLabel,
  fill = false,
}: {
  cells: HeatCell[];
  valueFormat?: (v: number) => string;
  ariaLabel: string;
  /** 0 = Sunday, 1 = Monday. */
  /** Stretch the rows to the container's height. */
  fill?: boolean;
}) {
  const [hover, setHover] = useState<HeatCell | null>(null);
  const grid = useMemo(() => {
    const m = new Map<string, number>();
    for (const c of cells)
      m.set(`${c.weekday}:${c.hour}`, (m.get(`${c.weekday}:${c.hour}`) ?? 0) + c.value);
    return m;
  }, [cells]);
  const max = Math.max(0, ...grid.values());
  const days = Array.from({ length: 7 }, (_, i) => (i + 1) % 7);
  // 2023-01-01 was a Sunday: weekday n is that date plus n days.
  const dayName = (n: number) =>
    new Date(Date.UTC(2023, 0, 1 + n)).toLocaleDateString(getLocale(), {
      weekday: "short",
      timeZone: "UTC",
    });
  const step = (v: number) =>
    v <= 0 || max <= 0
      ? -1
      : Math.min(HEAT_STEPS.length - 1, Math.floor((v / max) * HEAT_STEPS.length - 1e-9));
  return (
    <div
      role="img"
      aria-label={ariaLabel}
      onMouseLeave={() => setHover(null)}
      className={cn(fill && "flex h-full min-h-[16rem] flex-col")}
    >
      <div
        className={cn(
          "grid grid-cols-[2.5rem_1fr] gap-x-2 gap-y-1",
          fill && "flex-1 grid-rows-[repeat(7,minmax(1.25rem,1fr))_auto]",
        )}
      >
        {days.map((d) => (
          <div key={d} className="contents">
            <div className="self-center text-right text-[11px] text-fg-subtle">{dayName(d)}</div>
            <div className="grid grid-cols-24 gap-0.5">
              {Array.from({ length: 24 }, (_, h) => {
                const v = grid.get(`${d}:${h}`) ?? 0;
                const s = step(v);
                return (
                  <div
                    key={h}
                    onMouseEnter={() => setHover({ weekday: d, hour: h, value: v })}
                    title={`${dayName(d)} ${formatHour(h)}: ${valueFormat(v)}`}
                    className={cn(
                      fill ? "h-full min-h-5 rounded-[3px]" : "h-5 rounded-[3px]",
                      s < 0 ? "bg-bg-subtle ring-1 ring-inset ring-border-subtle" : HEAT_STEPS[s],
                      hover && hover.weekday === d && hover.hour === h && "ring-2 ring-fg/60",
                    )}
                  />
                );
              })}
            </div>
          </div>
        ))}
        <div />
        <div className="flex justify-between text-[10px] text-fg-subtle">
          <span>{formatHour(0)}</span>
          <span>{formatHour(6)}</span>
          <span>{formatHour(12)}</span>
          <span>{formatHour(18)}</span>
          <span>{formatHour(24)}</span>
        </div>
      </div>
      <div className="mt-3 flex items-center justify-between gap-3 text-xs">
        <span className="tabular-nums text-fg-muted">
          {hover
            ? `${dayName(hover.weekday)} ${formatHour(hover.hour)} · ${valueFormat(hover.value)}`
            : " "}
        </span>
        <span className="flex items-center gap-1.5 text-fg-subtle">
          {t("Less")}
          {HEAT_STEPS.map((c) => (
            <span key={c} className={cn("size-3 rounded-[3px]", c)} />
          ))}
          {t("More")}
        </span>
      </div>
    </div>
  );
}

interface StackDatum {
  key: string;
  label: string;
  a: number;
  b: number;
}

/**
 * Columns over time, each split into two series (e.g. direct vs transcoded),
 * stacked from the baseline with a 2px surface gap and a legend.
 */
export function StackedColumns({
  data,
  aLabel,
  bLabel,
  height = 180,
  ariaLabel,
  valueFormat = (v) => String(v),
}: {
  data: StackDatum[];
  aLabel: string;
  bLabel: string;
  height?: number;
  ariaLabel: string;
  valueFormat?: (v: number) => string;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const [ref, size] = useElementSize<HTMLDivElement>();
  const width = Math.max(240, Math.round(size.width || 600));
  const padL = 32;
  const padB = 22;
  const padT = 8;
  const max = niceMax(Math.max(0, ...data.map((d) => d.a + d.b)));
  const plotH = height - padB - padT;
  const slot = data.length ? (width - padL) / data.length : 0;
  const gap = Math.min(2, slot * 0.25);
  const barW = Math.max(1, slot - gap);
  const labelIdx = data.length ? [0, Math.floor((data.length - 1) / 2), data.length - 1] : [];
  const h = hover !== null ? data[hover] : undefined;
  return (
    <div>
      <div ref={ref} className="relative">
        <svg
          viewBox={`0 0 ${width} ${height}`}
          width="100%"
          height={height}
          className="block overflow-visible"
          role="img"
          aria-label={ariaLabel}
          onMouseLeave={() => setHover(null)}
        >
          {[0, max / 2, max].map((tick) => {
            const y = padT + plotH - (tick / max) * plotH;
            return (
              <g key={tick}>
                <line
                  x1={padL}
                  x2={width}
                  y1={y}
                  y2={y}
                  className="stroke-border-subtle"
                  strokeWidth={1}
                  vectorEffect="non-scaling-stroke"
                />
                <text
                  x={padL - 6}
                  y={y + 3}
                  textAnchor="end"
                  className="fill-fg-subtle text-[10px] tabular-nums"
                >
                  {valueFormat(tick)}
                </text>
              </g>
            );
          })}
          {data.map((d, i) => {
            const x = padL + i * slot + gap / 2;
            const ah = (d.a / max) * plotH;
            const bh = (d.b / max) * plotH;
            const dim = hover !== null && hover !== i;
            const base = padT + plotH;
            return (
              <g key={d.key} className={cn("transition-opacity", dim && "opacity-40")}>
                <rect
                  x={padL + i * slot}
                  y={padT}
                  width={slot}
                  height={plotH}
                  fill="transparent"
                  onMouseEnter={() => setHover(i)}
                  onFocus={() => setHover(i)}
                  onBlur={() => setHover(null)}
                  tabIndex={0}
                  aria-label={`${d.label}: ${aLabel} ${valueFormat(d.a)}, ${bLabel} ${valueFormat(d.b)}`}
                  className="focus:outline-none"
                />
                {ah > 0 ? (
                  <path
                    d={
                      bh > 0
                        ? `M${x},${base}V${base - ah}H${x + barW}V${base}Z`
                        : roundedTopBar(x, base - ah, barW, ah, 4)
                    }
                    className={cn("pointer-events-none", VIZ["viz-1"].fill)}
                  />
                ) : null}
                {bh > 0 ? (
                  <path
                    d={roundedTopBar(x, base - ah - bh - (ah > 0 ? 2 : 0), barW, bh, 4)}
                    className={cn("pointer-events-none", VIZ["viz-3"].fill)}
                  />
                ) : null}
              </g>
            );
          })}
          <line
            x1={padL}
            x2={width}
            y1={padT + plotH}
            y2={padT + plotH}
            className="stroke-border"
            strokeWidth={1}
            vectorEffect="non-scaling-stroke"
          />
          {labelIdx.map((i, n) => {
            const d = data[i];
            if (!d || (n > 0 && i === labelIdx[n - 1])) return null;
            return (
              <text
                key={`l${i}`}
                x={padL + i * slot + slot / 2}
                y={height - 6}
                textAnchor={n === 0 ? "start" : n === 2 ? "end" : "middle"}
                className="fill-fg-subtle text-[10px]"
              >
                {d.label}
              </text>
            );
          })}
        </svg>
        {h && hover !== null ? (
          <div
            className="pointer-events-none absolute top-0 z-10 -translate-x-1/2 whitespace-nowrap rounded-md border border-border bg-elevated px-2.5 py-1.5 text-xs shadow-md"
            style={{ left: `${((padL + hover * slot + slot / 2) / width) * 100}%` }}
          >
            <div className="font-medium text-fg">{h.label}</div>
            <div className="tabular-nums text-fg-muted">
              {aLabel} {valueFormat(h.a)} · {bLabel} {valueFormat(h.b)}
            </div>
          </div>
        ) : null}
      </div>
      <ul className="mt-2 flex gap-4 text-xs">
        <li className="flex items-center gap-1.5">
          <span className={cn("size-2.5 rounded-sm", VIZ["viz-1"].bg)} />
          <span className="text-fg">{aLabel}</span>
        </li>
        <li className="flex items-center gap-1.5">
          <span className={cn("size-2.5 rounded-sm", VIZ["viz-3"].bg)} />
          <span className="text-fg">{bLabel}</span>
        </li>
      </ul>
    </div>
  );
}

interface LineSeries {
  key: string;
  label: string;
  color: Viz;
  values: number[];
}

/**
 * Lines over time for a few series that share one unit (never two y-scales).
 * 2px lines, a soft area under the first series, a crosshair with every
 * series' value on hover, and a legend.
 */
export function SeriesLines({
  labels,
  series,
  height = 200,
  ariaLabel,
  valueFormat = (v) => String(v),
}: {
  labels: string[];
  series: LineSeries[];
  height?: number;
  ariaLabel: string;
  valueFormat?: (v: number) => string;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const [ref, size] = useElementSize<HTMLDivElement>();
  const width = Math.max(240, Math.round(size.width || 600));
  const padL = 32;
  const padR = 8;
  const padB = 22;
  const padT = 10;
  const n = labels.length;
  const max = niceMax(Math.max(0, ...series.flatMap((s) => s.values)));
  const plotW = width - padL - padR;
  const plotH = height - padB - padT;
  const x = (i: number) => padL + (n <= 1 ? plotW / 2 : (i / (n - 1)) * plotW);
  const y = (v: number) => padT + plotH - (v / max) * plotH;
  const path = (vals: number[]) => vals.map((v, i) => `${i ? "L" : "M"}${x(i)},${y(v)}`).join("");
  const labelIdx = n ? [0, Math.floor((n - 1) / 2), n - 1] : [];
  const pick = (clientX: number, el: SVGSVGElement) => {
    const r = el.getBoundingClientRect();
    const px = ((clientX - r.left) / r.width) * width;
    const i = n <= 1 ? 0 : Math.round(((px - padL) / plotW) * (n - 1));
    setHover(Math.max(0, Math.min(n - 1, i)));
  };
  return (
    <div>
      <div ref={ref} className="relative">
        <svg
          viewBox={`0 0 ${width} ${height}`}
          width="100%"
          height={height}
          className="block overflow-visible"
          role="img"
          aria-label={ariaLabel}
          onMouseMove={(e) => pick(e.clientX, e.currentTarget)}
          onMouseLeave={() => setHover(null)}
        >
          {[0, max / 2, max].map((tick) => (
            <g key={tick}>
              <line
                x1={padL}
                x2={width - padR}
                y1={y(tick)}
                y2={y(tick)}
                className="stroke-border-subtle"
                strokeWidth={1}
                vectorEffect="non-scaling-stroke"
              />
              <text
                x={padL - 6}
                y={y(tick) + 3}
                textAnchor="end"
                className="fill-fg-subtle text-[10px] tabular-nums"
              >
                {valueFormat(tick)}
              </text>
            </g>
          ))}
          {series[0] && n > 1 ? (
            <path
              d={`${path(series[0].values)}L${x(n - 1)},${padT + plotH}L${x(0)},${padT + plotH}Z`}
              className={cn(VIZ[series[0].color].fill, "opacity-15")}
            />
          ) : null}
          {series.map((s) => (
            <path
              key={s.key}
              d={path(s.values)}
              fill="none"
              strokeWidth={2}
              strokeLinejoin="round"
              strokeLinecap="round"
              vectorEffect="non-scaling-stroke"
              className={VIZ[s.color].stroke}
            />
          ))}
          {n <= 2
            ? // One or two points make no visible line; mark each point.
              series.map((s) =>
                s.values.map((v, i) => (
                  <circle
                    key={`${s.key}-${i}`}
                    cx={x(i)}
                    cy={y(v)}
                    r={4}
                    className={cn(VIZ[s.color].fill, "pointer-events-none")}
                  />
                )),
              )
            : null}
          {hover !== null ? (
            <g>
              <line
                x1={x(hover)}
                x2={x(hover)}
                y1={padT}
                y2={padT + plotH}
                className="stroke-border"
                strokeWidth={1}
                vectorEffect="non-scaling-stroke"
              />
              {series.map((s) => (
                <circle
                  key={s.key}
                  cx={x(hover)}
                  cy={y(s.values[hover] ?? 0)}
                  r={4.5}
                  strokeWidth={2}
                  className={cn(VIZ[s.color].fill, "stroke-surface")}
                />
              ))}
            </g>
          ) : null}
          <line
            x1={padL}
            x2={width - padR}
            y1={padT + plotH}
            y2={padT + plotH}
            className="stroke-border"
            strokeWidth={1}
            vectorEffect="non-scaling-stroke"
          />
          {labelIdx.map((i, k) =>
            k > 0 && i === labelIdx[k - 1] ? null : (
              <text
                key={`l${i}`}
                x={x(i)}
                y={height - 6}
                textAnchor={k === 0 ? "start" : k === 2 ? "end" : "middle"}
                className="fill-fg-subtle text-[10px]"
              >
                {labels[i]}
              </text>
            ),
          )}
        </svg>
        {hover !== null ? (
          <div
            className="pointer-events-none absolute top-0 z-10 -translate-x-1/2 whitespace-nowrap rounded-md border border-border bg-elevated px-2.5 py-1.5 text-xs shadow-md"
            style={{ left: `${(x(hover) / width) * 100}%` }}
          >
            <div className="font-medium text-fg">{labels[hover]}</div>
            {series.map((s) => (
              <div key={s.key} className="flex items-center gap-1.5 tabular-nums text-fg-muted">
                <span className={cn("size-2 rounded-full", VIZ[s.color].bg)} />
                {s.label} <span className="text-fg">{valueFormat(s.values[hover] ?? 0)}</span>
              </div>
            ))}
          </div>
        ) : null}
      </div>
      <ul className="mt-2 flex flex-wrap gap-4 text-xs">
        {series.map((s) => (
          <li key={s.key} className="flex items-center gap-1.5">
            <span className={cn("h-0.5 w-3 rounded-full", VIZ[s.color].bg)} />
            <span className="text-fg">{s.label}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
