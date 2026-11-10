import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { Kpi, KpiRow } from "../../components/Kpi";
import { EmptyState, PagedTable } from "@braedonsaunders/appkit-ui";
import { AlertTriangle, Gauge, PlayCircle, Timer, Waves } from "lucide-react";
import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Segmented } from "../../components/fields";
import { t, tn } from "../../i18n";
import { pagedTableLabels } from "../../i18n/appkit-labels";
import { apiFetch } from "../../lib/api";
import { useAsync, useInterval } from "../../lib/hooks";
import { ExperienceDrawer, MetricFacts, ToneValue } from "./ExperienceDrawer";
import {
  BUFFER_GOOD_PER_HOUR,
  START_GOOD_MS,
  badness,
  bufferTone,
  directShare,
  failedTone,
  formatHours,
  formatMbps,
  formatPerHour,
  formatSeconds,
  pct,
  startTone,
} from "./experience-format";
import type { ExperienceOverview } from "./types";

type Row = ExperienceOverview["viewers"][number];

/**
 * Activity › Experience: how playback feels to each viewer — time to the
 * first picture, buffering, failed starts and how often titles direct play.
 */
export function Experience() {
  const [params, setParams] = useSearchParams();
  const days = params.get("days") === "30" ? "30" : "7";
  const viewerParam = params.get("viewer");
  const viewerId = viewerParam && /^\d+$/.test(viewerParam) ? Number(viewerParam) : null;
  const overview = useAsync(
    (signal) => apiFetch<ExperienceOverview>(`/experience?days=${days}`, { signal }),
    [days],
  );
  useInterval(() => void overview.reload(), 60_000);

  const update = (key: string, value: string | null) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value == null) next.delete(key);
        else next.set(key, value);
        return next;
      },
      { preventScrollReset: true },
    );

  const rows = useMemo(
    () =>
      [...(overview.data?.viewers ?? [])].sort((a, b) => badness(b.metrics) - badness(a.metrics)),
    [overview.data],
  );

  const o = overview.data;
  const h = o?.household;
  const direct = h ? directShare(h) : null;
  const viewerName = rows.find((r) => r.userId === viewerId)?.username;

  return (
    <>
      {overview.error && !o ? (
        <ErrorPanel error={overview.error} onRetry={() => void overview.reload()} />
      ) : !o || !h ? (
        <LoadingRows rows={4} />
      ) : (
        <>
          <KpiRow>
            <Kpi
              label={t("Median start time")}
              value={
                <ToneValue
                  value={formatSeconds(h.startMedianMs)}
                  dot={false}
                  tone={startTone(h.startMedianMs)}
                />
              }
              detail={
                h.startP90Ms != null
                  ? t("p90 {time} · target {target}", {
                      time: formatSeconds(h.startP90Ms),
                      target: formatSeconds(START_GOOD_MS),
                    })
                  : t("Target {value}", { value: formatSeconds(START_GOOD_MS) })
              }
              icon={<Timer size={18} />}
              tone={startTone(h.startMedianMs) ?? "primary"}
            />
            <Kpi
              label={t("Stalls")}
              value={
                <ToneValue
                  value={formatPerHour(h.bufferEventsPerHour)}
                  dot={false}
                  tone={bufferTone(h.bufferEventsPerHour)}
                />
              }
              detail={
                h.measuredPlays
                  ? t("{minutes} min in total · target under {target}", {
                      minutes: Math.round(h.bufferMs / 60000),
                      target: formatPerHour(BUFFER_GOOD_PER_HOUR),
                    })
                  : t("Target under {value}", { value: formatPerHour(BUFFER_GOOD_PER_HOUR) })
              }
              icon={<Waves size={18} />}
              tone={bufferTone(h.bufferEventsPerHour) ?? "primary"}
            />
            <Kpi
              label={t("Failed starts")}
              value={
                <ToneValue
                  value={h.measuredPlays ? String(h.failedStarts) : "—"}
                  dot={false}
                  tone={failedTone(h.failedStarts, h.measuredPlays)}
                />
              }
              detail={
                h.measuredPlays
                  ? tn(h.measuredPlays, "of {count} play", "of {count} plays")
                  : undefined
              }
              icon={<AlertTriangle size={18} />}
              tone={failedTone(h.failedStarts, h.measuredPlays) ?? "primary"}
            />
            <Kpi
              label={t("Direct play")}
              value={pct(direct)}
              detail={t("Plays sent to the app as-is")}
              icon={<PlayCircle size={18} />}
              tone="info"
            />
          </KpiRow>

          <DashboardPanel title={t("Household detail")} icon={<Gauge size={16} />}>
            <MetricFacts m={h} wide />
          </DashboardPanel>

          <PagedTable<Row>
            rows={rows}
            rowKey={(r) => String(r.userId)}
            pageSize={20}
            onRowClick={(r) => update("viewer", String(r.userId))}
            labels={pagedTableLabels({
              searchLabel: t("Search viewers"),
              searchPlaceholder: t("Find a viewer…"),
            })}
            empty={
              <EmptyState
                icon={<Timer />}
                title={t("No plays yet")}
                description={t("Viewers appear here once they play something in this period.")}
              />
            }
            columns={[
              {
                key: "viewer",
                header: t("Viewer"),
                search: (r) => r.username,
                sortValue: (r) => r.username.toLowerCase(),
                cell: (r) => (
                  <>
                    <p className="font-medium text-fg">{r.username || t("Unknown viewer")}</p>
                    <p className="text-xs text-fg-muted">
                      {tn(r.metrics.plays, "{count} play", "{count} plays")}
                    </p>
                  </>
                ),
              },
              {
                key: "start",
                header: t("Start time"),
                align: "right",
                sortValue: (r) => r.metrics.startMedianMs ?? -1,
                cell: (r) => (
                  <ToneValue
                    value={formatSeconds(r.metrics.startMedianMs)}
                    tone={startTone(r.metrics.startMedianMs)}
                  />
                ),
              },
              {
                key: "buffering",
                header: t("Stalls"),
                align: "right",
                sortValue: (r) => r.metrics.bufferEventsPerHour ?? -1,
                cell: (r) => (
                  <ToneValue
                    value={formatPerHour(r.metrics.bufferEventsPerHour)}
                    tone={bufferTone(r.metrics.bufferEventsPerHour)}
                  />
                ),
              },
              {
                key: "failed",
                header: t("Failed starts"),
                align: "right",
                sortValue: (r) => (r.metrics.measuredPlays ? r.metrics.failedStarts : -1),
                cell: (r) => (
                  <ToneValue
                    value={r.metrics.measuredPlays ? String(r.metrics.failedStarts) : "—"}
                    tone={failedTone(r.metrics.failedStarts, r.metrics.measuredPlays)}
                  />
                ),
              },
              {
                key: "direct",
                header: t("Direct play"),
                align: "right",
                sortValue: (r) => directShare(r.metrics) ?? -1,
                cell: (r) => <span className="tabular-nums">{pct(directShare(r.metrics))}</span>,
              },
              {
                key: "bitrate",
                header: t("Avg bitrate"),
                align: "right",
                sortValue: (r) => r.metrics.deliveredKbps ?? -1,
                cell: (r) => (
                  <span className="tabular-nums">{formatMbps(r.metrics.deliveredKbps)}</span>
                ),
              },
              {
                key: "device",
                header: t("Most-used device"),
                search: (r) => r.metrics.topDevice?.name ?? "",
                sortValue: (r) => r.metrics.topDevice?.name.toLowerCase() ?? "",
                cell: (r) =>
                  r.metrics.topDevice ? (
                    <>
                      <p className="max-w-48 truncate text-fg">{r.metrics.topDevice.name}</p>
                      <p className="text-xs text-fg-muted">
                        {tn(r.metrics.topDevice.plays, "{count} play", "{count} plays")}
                      </p>
                    </>
                  ) : (
                    "—"
                  ),
              },
              {
                key: "hours",
                header: t("Hours watched"),
                align: "right",
                sortValue: (r) => r.metrics.watchedMs,
                cell: (r) => (
                  <span className="tabular-nums">{formatHours(r.metrics.watchedMs)}</span>
                ),
              },
            ]}
          />
        </>
      )}
      <ExperienceDrawer
        viewerId={viewerId}
        days={Number(days)}
        name={viewerName}
        onClose={() => update("viewer", null)}
      />
    </>
  );
}

/** Experience's period control, on the Activity tab row. */
export function ExperiencePeriod() {
  const [params, setParams] = useSearchParams();
  const days = params.get("days") === "30" ? "30" : "7";
  return (
    <Segmented
      ariaLabel={t("Period")}
      size="sm"
      value={days}
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
      options={[
        { value: "7", label: tn(7, "Last {count} day", "Last {count} days") },
        { value: "30", label: tn(30, "Last {count} day", "Last {count} days") },
      ]}
    />
  );
}
