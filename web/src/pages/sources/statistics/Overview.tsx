import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { cn } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { ComboChart, ComboSwatch, type ComboSeries } from "../../../components/charts";
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

/** Plays as columns, data as an area on its own scale, failures in marker lanes. */
const MARK: Record<Metric, Pick<ComboSeries, "mark" | "color" | "axis" | "shape">> = {
  plays: { mark: "bar", color: "viz-4" },
  data: { mark: "area", color: "viz-1", axis: "right" },
  failovers: { mark: "marker", color: "viz-3", shape: "dot" },
  failed: { mark: "marker", color: "viz-5", shape: "diamond" },
};
const METRICS: Metric[] = ["plays", "data", "failovers", "failed"];
const PLAY: Record<Exclude<Metric, "data">, (p: PlayDay | PlayHour) => number> = {
  plays: (p) => p.plays,
  failovers: (p) => p.failovers,
  failed: (p) => p.failed,
};

const gb = (v: number) => fixed(v, v > 0 && v < 10 ? 1 : 0);

/**
 * The Statistics headline: one chart of the period with plays as columns,
 * data downloaded as an area against its own scale on the right, and
 * failovers and failed plays as sized markers in thin lanes underneath. The legend
 * above it carries each measure's total and turns it on or off.
 */
export function SourceOverview({ data }: { data: StatsData }) {
  const [on, setOn] = useState<Set<Metric>>(() => new Set(METRICS));
  const h = data.history;
  const today = h.days === 1;
  const plays = h.plays ?? [];
  const hours = h.hours ?? [];
  const days = windowDays(h);
  const labels = today ? hourLabels(h) : days.map(dayLabel);
  const usenet = days.map((d) =>
    sum(
      (h.providerDays ?? []).filter((x) => x.day === d),
      (x) => x.bytes,
    ),
  );
  const debrid = days.map((d) =>
    sum(
      (h.streamDays ?? []).filter((x) => x.day === d),
      (x) => x.bytesFetched,
    ),
  );
  const bytes = days.map((_, i) => usenet[i]! + debrid[i]!);

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
  // Data is recorded per day, so a one-day window has no hourly data series.
  const canChart = (m: Metric) => !(today && m === "data");
  const visible = (m: Metric) => on.has(m) && canChart(m);
  const toggle = (m: Metric) =>
    setOn((prev) => {
      const next = new Set(prev);
      if (next.has(m)) {
        // At least one series stays on the chart.
        if (METRICS.filter((x) => x !== m && next.has(x) && canChart(x)).length) next.delete(m);
      } else next.add(m);
      return next;
    });

  const series: ComboSeries[] = METRICS.filter(visible).map((m) => ({
    key: m,
    label: label[m],
    ...MARK[m],
    values:
      m === "data"
        ? bytes.map((b) => b / 1e9)
        : today
          ? hourly(h, hours, PLAY[m])
          : daily(days, plays, PLAY[m]),
    format: m === "data" ? (v: number) => formatGB(v * 1e9) : undefined,
  }));

  return (
    <DashboardPanel title={t("Overview")} bodyClassName="space-y-3 px-4 pb-3 pt-3">
      <div role="group" aria-label={t("Measures to show")} className="flex flex-wrap gap-1.5">
        {METRICS.map((m) => {
          const pressed = visible(m);
          return (
            <button
              key={m}
              type="button"
              aria-pressed={pressed}
              disabled={!canChart(m)}
              title={canChart(m) ? undefined : t("Data is recorded per day")}
              onClick={() => toggle(m)}
              className={cn(
                "inline-flex h-7 min-w-0 items-center gap-1.5 rounded-full border px-2.5 text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                pressed
                  ? "border-border bg-elevated text-fg"
                  : "border-dashed border-border text-fg-muted hover:text-fg",
                !canChart(m) && "cursor-default opacity-60 hover:text-fg-muted",
              )}
            >
              <ComboSwatch {...MARK[m]} muted={!pressed} />
              <span className="truncate">{label[m]}</span>
              <span className="font-semibold tabular-nums">{shown[m]}</span>
            </button>
          );
        })}
      </div>
      <ComboChart
        ariaLabel={
          today
            ? t("Plays, failovers and failed plays each hour today")
            : t("Plays, data downloaded, failovers and failed plays each day")
        }
        labels={labels}
        height={156}
        series={series}
        leftUnit={t("Plays")}
        rightUnit={t("GB")}
        leftFormat={(v) => formatNumber(v, 0)}
        rightFormat={gb}
        detail={
          today || !visible("data")
            ? undefined
            : (i) =>
                t("Usenet {usenet} · Debrid {debrid}", {
                  usenet: formatGB(usenet[i]!),
                  debrid: formatGB(debrid[i]!),
                })
        }
      />
    </DashboardPanel>
  );
}
