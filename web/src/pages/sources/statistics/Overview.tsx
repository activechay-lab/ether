import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { cn } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { SeriesLines, TimeBars, type Viz } from "../../../components/charts";
import { t } from "../../../i18n";
import { fixed, formatNumber } from "../../../lib/format";
import {
  daily,
  dayLabel,
  formatGB,
  hourLabels,
  hourly,
  sum,
  windowDays,
  type PlayDay,
  type PlayHour,
  type StatsData,
} from "./data";

type Metric = "plays" | "data" | "failovers" | "failed";

/** Count series share one axis; data downloaded has its own chart below them. */
const COUNT: {
  key: Exclude<Metric, "data">;
  color: Viz;
  value: (p: PlayDay | PlayHour) => number;
}[] = [
  { key: "plays", color: "viz-4", value: (p) => p.plays },
  { key: "failovers", color: "viz-3", value: (p) => p.failovers },
  { key: "failed", color: "viz-5", value: (p) => p.failed },
];

const SWATCH: Record<Metric, string> = {
  plays: "bg-viz-4",
  failovers: "bg-viz-3",
  failed: "bg-viz-5",
  data: "bg-primary",
};

/**
 * The Statistics headline: plays, data downloaded, failovers and failed
 * plays over the period. Each measure is a toggle that also carries its
 * total. Counts share one chart; data downloaded is a second chart under it
 * on the same days, so two units never share an axis.
 */
export function SourceOverview({ data }: { data: StatsData }) {
  const [on, setOn] = useState<Set<Metric>>(() => new Set(["plays", "data"]));
  const h = data.history;
  const today = h.days === 1;
  const plays = h.plays ?? [];
  const hours = h.hours ?? [];
  const days = windowDays(h);
  const labels = today ? hourLabels(h) : days.map(dayLabel);
  const usenet = (d: string) =>
    sum(
      (h.providerDays ?? []).filter((x) => x.day === d),
      (x) => x.bytes,
    );
  const debrid = (d: string) =>
    sum(
      (h.streamDays ?? []).filter((x) => x.day === d),
      (x) => x.bytesFetched,
    );
  const bytes = days.map((d) => usenet(d) + debrid(d));

  const totals: Record<Metric, number> = {
    plays: sum(plays, (p) => p.plays),
    failovers: sum(plays, (p) => p.failovers),
    failed: sum(plays, (p) => p.failed),
    data: sum(bytes, (b) => b),
  };
  const label: Record<Metric, string> = {
    plays: t("Plays"),
    data: t("Data downloaded"),
    failovers: t("Failovers"),
    failed: t("Failed plays"),
  };
  const shown: Record<Metric, string> = {
    plays: formatNumber(totals.plays, 0),
    data: formatGB(totals.data),
    failovers: formatNumber(totals.failovers, 0),
    failed: formatNumber(totals.failed, 0),
  };
  // Data is recorded per day, so a one-day window has no hourly data chart.
  const canChart = (m: Metric) => !(today && m === "data");
  const toggle = (m: Metric) =>
    setOn((prev) => {
      const next = new Set(prev);
      if (next.has(m)) {
        // At least one chart stays visible.
        if ([...next].filter(canChart).length > 1 || !canChart(m)) next.delete(m);
      } else next.add(m);
      return next;
    });

  const counts = COUNT.filter((c) => on.has(c.key)).map((c) => ({
    key: c.key,
    label: label[c.key],
    color: c.color,
    values: today ? hourly(h, hours, c.value) : daily(days, plays, c.value),
  }));
  const showData = on.has("data") && canChart("data");
  const metrics: Metric[] = ["plays", "data", "failovers", "failed"];

  return (
    <DashboardPanel title={t("Overview")}>
      <div className="space-y-4">
        <div
          role="group"
          aria-label={t("Measures to show")}
          className="grid grid-cols-2 gap-2 sm:grid-cols-4"
        >
          {metrics.map((m) => {
            const pressed = on.has(m) && canChart(m);
            return (
              <button
                key={m}
                type="button"
                aria-pressed={pressed}
                disabled={!canChart(m)}
                title={canChart(m) ? undefined : t("Data is recorded per day")}
                onClick={() => toggle(m)}
                className={cn(
                  "min-w-0 rounded-lg border px-3 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  pressed
                    ? "border-border bg-elevated"
                    : "border-border-subtle bg-transparent opacity-70 hover:opacity-100",
                  !canChart(m) && "cursor-default hover:opacity-70",
                )}
              >
                <span className="flex items-center gap-1.5 text-xs text-fg-muted">
                  <span
                    aria-hidden
                    className={cn(
                      "size-2.5 shrink-0 rounded-sm",
                      pressed ? SWATCH[m] : "border border-border bg-transparent",
                    )}
                  />
                  <span className="truncate">{label[m]}</span>
                </span>
                <span className="mt-0.5 block truncate text-lg font-semibold tabular-nums text-fg">
                  {shown[m]}
                </span>
              </button>
            );
          })}
        </div>
        {counts.length ? (
          <SeriesLines
            ariaLabel={
              today ? t("Plays each hour today") : t("Plays, failovers and failed plays each day")
            }
            labels={labels}
            height={220}
            valueFormat={(v) => formatNumber(v, 0)}
            series={counts}
          />
        ) : null}
        {showData ? (
          <div>
            <p className="mb-1 text-xs font-medium text-fg-muted">{t("Data downloaded (GB)")}</p>
            <TimeBars
              ariaLabel={t("Gigabytes downloaded each day")}
              height={counts.length ? 120 : 220}
              valueFormat={(v) => fixed(v, v > 0 && v < 10 ? 1 : 0)}
              data={days.map((day, i) => ({
                key: day,
                label: labels[i]!,
                value: bytes[i]! / 1e9,
                detail: t("Usenet {usenet} · Debrid {debrid}", {
                  usenet: formatGB(usenet(day)),
                  debrid: formatGB(debrid(day)),
                }),
              }))}
            />
          </div>
        ) : null}
      </div>
    </DashboardPanel>
  );
}
