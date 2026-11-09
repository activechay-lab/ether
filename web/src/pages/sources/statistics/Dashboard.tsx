import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { Cloud, DatabaseZap, Newspaper, Search } from "lucide-react";
import {
  RankedBars,
  SeriesLines,
  SplitBar,
  StackedColumns,
  type Viz,
} from "../../../components/charts";
import { Kpi, KpiRow } from "../../../components/Kpi";
import { t, tn } from "../../../i18n";
import { fixed, formatNumber, formatPercent } from "../../../lib/format";
import {
  daily,
  dayLabel,
  formatGB,
  ratio,
  sum,
  windowDays,
  type SourceRow,
  type StatsData,
} from "./data";
import { SourceOverview } from "./Overview";

/** Fixed colours so each source type reads the same in every chart. */
const ORIGIN_COLOR: Record<string, Viz> = {
  Usenet: "viz-1",
  Debrid: "viz-2",
  HTTP: "viz-4",
  Other: "viz-5",
};
const SPARE: Viz[] = ["viz-3", "viz-5", "viz-4"];

const pct = (v: number | null) => (v == null ? "—" : formatPercent(v, v > 0.995 && v < 1 ? 1 : 0));
const gb = (v: number) => fixed(v, v > 0 && v < 10 ? 1 : 0);

/**
 * A day with no requests has no rate: it keeps the previous day's (or, before
 * the first, the first measured day's) instead of falling to zero.
 */
function holdGaps(values: number[]): number[] {
  let last = values.find((v) => !Number.isNaN(v)) ?? 0;
  return values.map((v) => (Number.isNaN(v) ? last : (last = v)));
}

function Empty() {
  return (
    <p className="py-6 text-center text-sm text-fg-muted">
      {t("Nothing recorded in this period.")}
    </p>
  );
}

/** Statistics › Dashboard: what every source did over the period, day by day. */
export function SourceDashboard({ data, rows }: { data: StatsData; rows: SourceRow[] }) {
  const h = data.history;
  const today = h.days === 1;
  const days = windowDays(h);
  const labels = days.map(dayLabel);
  const plays = h.plays ?? [];
  const providers = h.providerDays ?? [];
  const indexers = h.indexerDays ?? [];
  const debrid = h.debridDays ?? [];
  const stream = h.streamDays ?? [];

  const totalPlays = sum(plays, (p) => p.plays);
  const usenetBytes = sum(providers, (d) => d.bytes);
  const debridBytes = sum(stream, (d) => d.bytesFetched);
  const searches = sum(indexers, (d) => d.searches);
  const found = sum(indexers, (d) => d.successes);
  const articles = sum(providers, (d) => d.articles);
  const missing = sum(providers, (d) => d.misses + d.errors);
  const requests = sum(debrid, (d) => d.requests);
  const requestFailures = sum(debrid, (d) => d.failures);
  const hits = sum(stream, (d) => d.memoryHits + d.diskHits);
  const fetched = sum(stream, (d) => d.blocksFetched);

  const origins = [...new Set(plays.map((p) => p.origin))].sort(
    (a, b) =>
      sum(
        plays.filter((p) => p.origin === b),
        (p) => p.plays,
      ) -
      sum(
        plays.filter((p) => p.origin === a),
        (p) => p.plays,
      ),
  );
  const originLabel = (o: string) => (o === "Other" ? t("Other") : o);

  const successSeries = [
    {
      key: "usenet",
      label: t("Usenet articles"),
      color: "viz-1" as Viz,
      rows: providers.length,
      values: days.map((day) => {
        const d = providers.filter((p) => p.day === day);
        const ok = sum(d, (p) => p.articles);
        return (ratio(ok, ok + sum(d, (p) => p.misses + p.errors)) ?? NaN) * 100;
      }),
    },
    {
      key: "indexers",
      label: t("Indexer searches"),
      color: "viz-3" as Viz,
      rows: indexers.length,
      values: days.map((day) => {
        const d = indexers.filter((p) => p.day === day);
        return (
          (ratio(
            sum(d, (p) => p.successes),
            sum(d, (p) => p.searches),
          ) ?? NaN) * 100
        );
      }),
    },
    {
      key: "debrid",
      label: t("Debrid requests"),
      color: "viz-2" as Viz,
      rows: debrid.length,
      values: days.map((day) => {
        const d = debrid.filter((p) => p.day === day);
        const n = sum(d, (p) => p.requests);
        return (ratio(n - sum(d, (p) => p.failures), n) ?? NaN) * 100;
      }),
    },
  ]
    .filter((s) => s.rows > 0)
    .map((s) => ({ ...s, values: holdGaps(s.values) }));

  const busiest = (kind: SourceRow["kind"]) =>
    rows
      .filter((r) => r.kind === kind && r.usageSort > 0)
      .slice()
      .sort((a, b) => b.usageSort - a.usageSort)
      .slice(0, 6);

  return (
    <>
      <KpiRow>
        <Kpi
          label={t("Article success")}
          value={pct(ratio(articles, articles + missing))}
          detail={
            articles + missing
              ? tn(
                  missing,
                  "{count} missing or failed article",
                  "{count} missing or failed articles",
                )
              : t("No Usenet downloads in this period")
          }
          icon={<Newspaper size={18} />}
        />
        <Kpi
          label={t("Debrid success")}
          value={pct(ratio(requests - requestFailures, requests))}
          detail={tn(requests, "{count} debrid request", "{count} debrid requests")}
          icon={<Cloud size={18} />}
          tone="info"
        />
        <Kpi
          label={t("Search success")}
          value={pct(ratio(found, searches))}
          detail={tn(searches, "{count} indexer search", "{count} indexer searches")}
          icon={<Search size={18} />}
          tone="success"
        />
        <Kpi
          label={t("Stream cache hits")}
          value={pct(ratio(hits, hits + fetched))}
          detail={
            hits + fetched
              ? t("{hits} of {total} debrid blocks", {
                  hits: formatNumber(hits, 0),
                  total: formatNumber(hits + fetched, 0),
                })
              : t("Debrid reads served from cache")
          }
          icon={<DatabaseZap size={18} />}
          tone="info"
        />
      </KpiRow>

      <SourceOverview data={data} />

      <div className="grid gap-6 lg:grid-cols-2">
        <DashboardPanel title={today ? t("Data downloaded") : t("Data downloaded (GB)")}>
          {!(usenetBytes + debridBytes) ? (
            <Empty />
          ) : today ? (
            <SplitBar
              ariaLabel={t("Data downloaded by source")}
              valueFormat={formatGB}
              data={rows
                .filter((r) => r.kind === "usenet" || r.kind === "debrid")
                .map((r) => ({
                  key: r.key,
                  label: r.name,
                  value:
                    r.kind === "usenet"
                      ? sum(
                          providers.filter((d) => d.id === r.id),
                          (d) => d.bytes,
                        )
                      : sum(
                          debrid.filter((d) => d.id === r.id),
                          (d) => d.bytes ?? 0,
                        ),
                }))}
            />
          ) : (
            <SeriesLines
              ariaLabel={t("Gigabytes downloaded each day, Usenet and debrid")}
              labels={labels}
              valueFormat={gb}
              series={[
                {
                  key: "usenet",
                  label: t("Usenet"),
                  color: "viz-1",
                  values: daily(days, providers, (d) => d.bytes / 1e9),
                },
                ...(debridBytes
                  ? [
                      {
                        key: "debrid",
                        label: t("Debrid"),
                        color: "viz-2" as Viz,
                        values: daily(days, stream, (d) => d.bytesFetched / 1e9),
                      },
                    ]
                  : []),
              ]}
            />
          )}
        </DashboardPanel>
        <DashboardPanel title={today ? t("Success rate") : t("Success rate (%)")}>
          {!successSeries.length ? (
            <Empty />
          ) : today ? (
            <RankedBars
              color="viz-4"
              ariaLabel={t("Success rate by source type")}
              valueFormat={(v) => pct(v / 100)}
              data={successSeries.map((s) => ({
                key: s.key,
                label: s.label,
                value: s.values[0] ?? 0,
              }))}
            />
          ) : (
            <SeriesLines
              ariaLabel={t("Success rate each day by source type")}
              labels={labels}
              valueFormat={(v) => fixed(v, 0)}
              series={successSeries.map(({ key, label, color, values }) => ({
                key,
                label,
                color,
                values,
              }))}
            />
          )}
        </DashboardPanel>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        <DashboardPanel title={t("Indexer searches")}>
          {searches && today ? (
            <SplitBar
              ariaLabel={t("Indexer searches, with and without results")}
              valueFormat={(v) => formatNumber(v, 0)}
              data={[
                { key: "found", label: t("Found releases"), value: found, color: "viz-1" },
                { key: "none", label: t("Nothing found"), value: searches - found, color: "viz-3" },
              ]}
            />
          ) : searches ? (
            <StackedColumns
              ariaLabel={t("Indexer searches each day, with and without results")}
              aLabel={t("Found releases")}
              bLabel={t("Nothing found")}
              valueFormat={(v) => formatNumber(v, 0)}
              data={days.map((day, i) => {
                const d = indexers.filter((x) => x.day === day);
                const ok = sum(d, (x) => x.successes);
                return { key: day, label: labels[i]!, a: ok, b: sum(d, (x) => x.searches) - ok };
              })}
            />
          ) : (
            <Empty />
          )}
        </DashboardPanel>
        <DashboardPanel title={t("Debrid stream cache")}>
          {hits + fetched && today ? (
            <SplitBar
              ariaLabel={t("Debrid blocks from cache and from the service")}
              valueFormat={(v) => formatNumber(v, 0)}
              data={[
                { key: "cache", label: t("From cache"), value: hits, color: "viz-1" },
                { key: "fetched", label: t("Downloaded"), value: fetched, color: "viz-3" },
              ]}
            />
          ) : hits + fetched ? (
            <StackedColumns
              ariaLabel={t("Debrid blocks each day, from cache and from the service")}
              aLabel={t("From cache")}
              bLabel={t("Downloaded")}
              valueFormat={(v) => formatNumber(v, 0)}
              data={days.map((day, i) => {
                const d = stream.filter((x) => x.day === day);
                return {
                  key: day,
                  label: labels[i]!,
                  a: sum(d, (x) => x.memoryHits + x.diskHits),
                  b: sum(d, (x) => x.blocksFetched),
                };
              })}
            />
          ) : (
            <Empty />
          )}
        </DashboardPanel>
      </div>

      <div className="grid gap-6 lg:grid-cols-3">
        <DashboardPanel title={t("Plays by source")}>
          {totalPlays ? (
            <SplitBar
              ariaLabel={t("Plays by source")}
              valueFormat={(v) => tn(v, "{count} play", "{count} plays")}
              data={origins.map((o, i) => ({
                key: o,
                label: originLabel(o),
                value: sum(
                  plays.filter((p) => p.origin === o),
                  (p) => p.plays,
                ),
                color: ORIGIN_COLOR[o] ?? SPARE[i % SPARE.length],
              }))}
            />
          ) : (
            <Empty />
          )}
        </DashboardPanel>
        <DashboardPanel title={t("Busiest indexers")}>
          <RankedBars
            color="viz-3"
            ariaLabel={t("Busiest indexers")}
            data={busiest("indexer").map((r) => ({
              key: r.key,
              label: r.name,
              value: r.usageSort,
              detail:
                r.success == null ? undefined : t("{percent} found", { percent: pct(r.success) }),
            }))}
            valueFormat={(v) => tn(v, "{count} search", "{count} searches")}
            empty={<Empty />}
          />
        </DashboardPanel>
        <DashboardPanel title={t("Usenet providers")}>
          <RankedBars
            color="viz-1"
            ariaLabel={t("Usenet providers by data downloaded")}
            data={busiest("usenet").map((r) => ({
              key: r.key,
              label: r.name,
              value: r.usageSort,
              detail:
                r.success == null
                  ? undefined
                  : t("{percent} articles found", { percent: pct(r.success) }),
            }))}
            valueFormat={formatGB}
            empty={<Empty />}
          />
        </DashboardPanel>
      </div>
      <p className="text-xs text-fg-muted">
        {t(
          "Days follow UTC, like most indexers. Debrid counts start when this version of Ether first ran.",
        )}
      </p>
    </>
  );
}
