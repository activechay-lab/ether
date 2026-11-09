import { useMemo } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { ErrorPanel, LoadingRows } from "../../../components/feedback";
import { Segmented } from "../../../components/fields";
import { PageSubtabs } from "../../../components/layout";
import { localized, t } from "../../../i18n";
import { useLocale } from "../../../i18n/react";
import { useInterval } from "../../../lib/hooks";
import { usePluginSourceSummaries } from "../PluginSources";
import { periodOf, periodOptions, sourceRows, useSourceStats } from "./data";
import { SourceDashboard } from "./Dashboard";
import { SourceDrawer } from "./SourceDrawer";
import { SourceUsageTable } from "./UsageTable";

const VIEWS = localized(() => [
  { key: "dashboard", label: t("Dashboard") },
  { key: "usage", label: t("Usage") },
]);

/**
 * Sources › Statistics: a dashboard of every source over time, and a usage
 * table whose rows open everything Ether records about that source.
 */
export function SourceStatistics() {
  const { section } = useParams();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const view = section === "usage" ? "usage" : "dashboard";
  const days = periodOf(params.get("days"));
  const stats = useSourceStats(days);
  useInterval(() => void stats.reload(), 60_000);
  const plugins = usePluginSourceSummaries();
  const { locale } = useLocale();
  const rows = useMemo(
    () => (stats.data ? sourceRows(stats.data, plugins) : []),
    [stats.data, plugins, locale],
  );
  const open = params.get("source");
  const selected = rows.find((r) => r.key === open) ?? null;

  const select = (key: string) => {
    const next = new URLSearchParams(params);
    next.delete("source");
    const query = next.toString();
    navigate(`/sources/statistics/${key}${query ? `?${query}` : ""}`);
  };
  const setSource = (key: string | null) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (key == null) next.delete("source");
        else next.set("source", key);
        return next;
      },
      { preventScrollReset: true },
    );

  return (
    <>
      <PageSubtabs
        ariaLabel={t("Statistics sections")}
        active={view}
        onSelect={select}
        tabs={VIEWS}
      />
      {stats.error && !stats.data ? (
        <ErrorPanel error={stats.error} onRetry={() => void stats.reload()} />
      ) : !stats.data ? (
        <LoadingRows rows={4} />
      ) : view === "dashboard" ? (
        <SourceDashboard data={stats.data} rows={rows} />
      ) : (
        <SourceUsageTable
          rows={rows}
          days={stats.data.history.days}
          monthPlays={stats.data.infra?.monthPlays ?? 0}
          onOpen={(r) => setSource(r.key)}
        />
      )}
      <SourceDrawer
        row={selected}
        data={stats.data}
        days={days}
        onClose={() => setSource(null)}
        onSaved={() => stats.reload()}
      />
    </>
  );
}

/** Statistics' period control, on the Sources tab row. */
export function StatisticsPeriod() {
  const [params, setParams] = useSearchParams();
  return (
    <Segmented
      ariaLabel={t("Period")}
      size="sm"
      value={periodOf(params.get("days"))}
      onChange={(v) =>
        setParams(
          (prev) => {
            const next = new URLSearchParams(prev);
            next.set("days", v);
            return next;
          },
          { preventScrollReset: true },
        )
      }
      options={periodOptions()}
    />
  );
}
