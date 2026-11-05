import { useLayoutEffect, useRef, useState } from "react";
import { cn } from "@braedonsaunders/appkit-ui";
import { t } from "../../i18n";
import { MINUTE, clock, hueOf, type Programme } from "./model";

/**
 * A channel's schedule as one ribbon: each programme a segment sized by
 * its running time, the live edge marked, labels where they fit.
 */
export function Timeline({
  programmes,
  from,
  until,
  now,
  label,
  className,
}: {
  programmes: Programme[];
  from: number;
  until: number;
  now: number;
  label: string;
  className?: string;
}) {
  const root = useRef<HTMLDivElement>(null);
  const [availableWidth, setAvailableWidth] = useState(600);
  useLayoutEffect(() => {
    const el = root.current;
    if (!el) return;
    const measure = () => {
      if (el.clientWidth) setAvailableWidth(el.clientWidth);
    };
    measure();
    const observer = typeof ResizeObserver !== "undefined" ? new ResizeObserver(measure) : null;
    observer?.observe(el);
    return () => observer?.disconnect();
  }, []);
  const span = until - from;
  const hourCount = span / (60 * MINUTE);
  const minWidth = hourCount > 30 ? Math.round(hourCount * 32) : undefined;
  const labelHours = Math.max(
    span > 12 * 60 * MINUTE ? 3 : 1,
    Math.ceil((hourCount * 72) / Math.max(1, minWidth ?? availableWidth)),
  );
  const pct = (ms: number) => ((Math.min(until, Math.max(from, ms)) - from) / span) * 100;
  const hours: number[] = [];
  const step = labelHours * 60 * MINUTE;
  for (let ms = Math.ceil(from / step) * step; ms < until; ms += step) hours.push(ms);
  // A day fits the drawer. A week keeps each hour readable and scrolls sideways.
  return (
    <div
      ref={root}
      className={cn("min-w-0 overflow-x-auto", className)}
      role="img"
      aria-label={label}
    >
      <div className="space-y-1.5" style={minWidth ? { minWidth } : undefined}>
        <div className="relative h-14 overflow-hidden rounded-xl border border-border bg-bg-subtle">
          {programmes.map((p) => {
            const s = Date.parse(p.start);
            const e = Date.parse(p.end);
            if (e <= from || s >= until) return null;
            const left = pct(s);
            const width = pct(e) - left;
            const hue = hueOf(p.item.title || p.id);
            const past = e <= now;
            return (
              <div
                key={p.id}
                title={`${p.item.title}\n${clock(p.start)} – ${clock(p.end)}`}
                className={cn(
                  "absolute inset-y-0 overflow-hidden border-r border-bg/80 px-2 py-1.5",
                  past && "opacity-45",
                )}
                style={{
                  left: `${left}%`,
                  width: `${width}%`,
                  background: `linear-gradient(180deg, hsl(${hue} 55% 50% / 0.22), hsl(${hue} 55% 45% / 0.12))`,
                  boxShadow: `inset 0 2px 0 hsl(${hue} 60% 50%)`,
                }}
              >
                {width > 7 ? (
                  <>
                    <p className="truncate text-xs font-medium text-fg">{p.item.title}</p>
                    <p className="truncate text-[10.5px] tabular-nums text-fg-muted">
                      {clock(p.start)}
                    </p>
                  </>
                ) : null}
              </div>
            );
          })}
          {now > from && now < until ? (
            <div
              className="absolute inset-y-0 w-0.5 bg-danger shadow-[0_0_6px] shadow-danger/70"
              style={{ left: `${pct(now)}%` }}
              aria-hidden
            />
          ) : null}
          {programmes.length === 0 ? (
            <p className="absolute inset-0 grid place-items-center text-xs text-fg-subtle">
              {t("Nothing scheduled in this window")}
            </p>
          ) : null}
        </div>
        <div className="relative h-4 text-[10.5px] tabular-nums text-fg-subtle" aria-hidden>
          {hours.map((ms) => (
            <span
              key={ms}
              className="absolute -translate-x-1/2 whitespace-nowrap"
              style={{ left: `${pct(ms)}%` }}
            >
              {clock(new Date(ms))}
            </span>
          ))}
        </div>
      </div>
    </div>
  );
}
