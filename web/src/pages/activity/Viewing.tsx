import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { CheckCircle2, Clock, PlayCircle, Users2 } from "lucide-react";
import {
  RankedBars,
  SeriesLines,
  SplitBar,
  TimeBars,
  WeekHeatmap,
  type Viz,
} from "../../components/charts";
import { Artwork } from "../../components/Artwork";
import { useTitle } from "../../lib/titles";
import type { EtherUser } from "../../lib/types";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Kpi, KpiRow } from "../../components/Kpi";
import { t, tn } from "../../i18n";
import { fixed, formatNumber, formatPercent } from "../../lib/format";
import { hoursOf, type Bucket, type Summary } from "./types";

function TitleThumb({ ratingKey, label }: { ratingKey?: string; label: string }) {
  const title = useTitle(ratingKey || undefined);
  return (
    <Artwork
      src={title?.poster}
      alt=""
      title={label}
      className="aspect-[2/3] w-8 shrink-0 rounded"
    />
  );
}

function ViewerAvatar({ user, label }: { user?: EtherUser; label: string }) {
  return user?.thumb ? (
    <img src={user.thumb} alt="" className="size-8 shrink-0 rounded-full object-cover" />
  ) : (
    <span className="grid size-8 shrink-0 place-items-center rounded-full bg-viz-5/20 text-xs font-semibold uppercase text-fg">
      {label.slice(0, 1)}
    </span>
  );
}

const rank = (items: Bucket[] | undefined, leading?: (b: Bucket) => React.ReactNode) =>
  (items ?? [])
    .filter((b) => b.label)
    .slice(0, 10)
    .map((b, i) => ({
      key: `${b.label}-${i}`,
      label: b.label,
      value: hoursOf(b.watchedMs),
      detail: tn(b.plays, "{count} play", "{count} plays"),
      leading: leading?.(b),
    }));

const TV: Viz = "viz-1";
const MOVIES: Viz = "viz-2";

function NoPlays() {
  return <p className="py-4 text-sm text-fg-muted">{t("No recorded plays in this period.")}</p>;
}

/** What people watched, when, and who watched most, for the period and viewer. */
export function Viewing({
  summary,
  viewers = [],
  error,
  onRetry,
}: {
  summary: Summary | undefined;
  viewers?: EtherUser[];
  error: unknown;
  onRetry: () => void;
}) {
  if (error && !summary) return <ErrorPanel error={error} onRetry={onRetry} />;
  if (!summary) return <LoadingRows rows={4} />;
  const s = summary;
  const kinds = (s.kinds ?? []).map((b) => ({
    key: b.label,
    label: b.label === "movie" ? t("Movies") : b.label === "episode" ? t("TV episodes") : b.label,
    value: hoursOf(b.watchedMs),
    color: b.label === "movie" ? MOVIES : b.label === "episode" ? TV : undefined,
  }));
  const userById = new Map(viewers.map((v) => [String(v.id), v]));
  const h = (v: number) => t("{hours} h", { hours: fixed(v, 1) });
  return (
    <>
      <KpiRow>
        <Kpi
          label={t("Hours watched")}
          value={fixed(hoursOf(s.watchedMs), 1)}
          detail={
            s.plays
              ? t("{minutes} min per play", { minutes: fixed(s.watchedMs / s.plays / 60000, 0) })
              : undefined
          }
          icon={<Clock size={18} />}
        />
        <Kpi
          label={t("Plays")}
          value={formatNumber(s.plays, 0)}
          detail={tn(s.users.length, "{count} viewer", "{count} viewers")}
          icon={<PlayCircle size={18} />}
          tone="info"
        />
        <Kpi
          label={t("Completed")}
          value={formatNumber(s.completed, 0)}
          detail={
            s.plays
              ? t("{percent} of plays", { percent: formatPercent(s.completed / s.plays) })
              : undefined
          }
          icon={<CheckCircle2 size={18} />}
          tone="success"
        />
        <Kpi
          label={t("Peak streams")}
          value={s.peakStreams != null ? formatNumber(s.peakStreams, 0) : "—"}
          detail={t("Playing at the same time")}
          icon={<Users2 size={18} />}
          tone="info"
        />
      </KpiRow>

      <DashboardPanel title={t("Watch time")}>
        {s.dailyKinds && s.dailyKinds.length ? (
          <SeriesLines
            ariaLabel={t("Hours watched by day, TV and movies")}
            labels={s.dailyKinds.map((d) => d.label)}
            valueFormat={(v) => fixed(v, v < 10 ? 1 : 0)}
            series={[
              {
                key: "tv",
                label: t("TV episodes"),
                color: TV,
                values: s.dailyKinds.map((d) => hoursOf(d.episodeMs)),
              },
              {
                key: "movies",
                label: t("Movies"),
                color: MOVIES,
                values: s.dailyKinds.map((d) => hoursOf(d.movieMs)),
              },
            ]}
          />
        ) : s.daily.length ? (
          <TimeBars
            ariaLabel={t("Hours watched by day")}
            height={180}
            valueFormat={(v) => fixed(v, v < 10 ? 1 : 0)}
            data={s.daily.map((b, i) => ({
              key: `${b.label}-${i}`,
              label: b.label,
              value: hoursOf(b.watchedMs),
              detail: tn(b.plays, "{count} play", "{count} plays"),
            }))}
          />
        ) : (
          <NoPlays />
        )}
      </DashboardPanel>

      <div className="grid items-stretch gap-6 lg:grid-cols-5">
        {s.heatmap ? (
          <DashboardPanel title={t("When people watch")} className="lg:col-span-3" fill>
            <WeekHeatmap
              fill
              ariaLabel={t("Plays by weekday and hour")}
              cells={s.heatmap.map((c) => ({ weekday: c.weekday, hour: c.hour, value: c.plays }))}
              valueFormat={(v) => tn(v, "{count} play", "{count} plays")}
            />
          </DashboardPanel>
        ) : null}
        <div className={s.heatmap ? "space-y-6 lg:col-span-2" : "space-y-6 lg:col-span-5"}>
          {kinds.length ? (
            <DashboardPanel title={t("Movies and TV")}>
              <SplitBar ariaLabel={t("Watch time by kind")} data={kinds} valueFormat={h} />
            </DashboardPanel>
          ) : null}
          {s.genres ? (
            <DashboardPanel title={t("Top genres")}>
              <RankedBars
                color="viz-3"
                ariaLabel={t("Top genres")}
                data={rank(s.genres).slice(0, 6)}
                valueFormat={h}
                empty={<NoPlays />}
              />
            </DashboardPanel>
          ) : null}
        </div>
      </div>

      <div className={s.shows ? "grid gap-6 lg:grid-cols-3" : "grid gap-6 lg:grid-cols-2"}>
        <DashboardPanel title={t("Top titles")}>
          <RankedBars
            color="viz-4"
            ariaLabel={t("Top titles")}
            data={rank(s.titles, (b) => (
              <TitleThumb ratingKey={b.key} label={b.label} />
            ))}
            valueFormat={h}
            empty={<NoPlays />}
          />
        </DashboardPanel>
        {s.shows ? (
          <DashboardPanel title={t("Top shows")}>
            <RankedBars
              color={TV}
              ariaLabel={t("Top shows")}
              data={rank(s.shows, (b) => (
                <TitleThumb ratingKey={b.key} label={b.label} />
              ))}
              valueFormat={h}
              empty={<NoPlays />}
            />
          </DashboardPanel>
        ) : null}
        <DashboardPanel title={t("Top viewers")}>
          <RankedBars
            color="viz-5"
            ariaLabel={t("Top viewers")}
            data={rank(s.users, (b) => (
              <ViewerAvatar user={b.key ? userById.get(b.key) : undefined} label={b.label} />
            ))}
            valueFormat={h}
            empty={<NoPlays />}
          />
        </DashboardPanel>
      </div>
      <p className="text-xs text-fg-muted">
        {t(
          "Days and hours use this browser's time zone. Watched time leaves out gaps longer than 60 seconds and simultaneous sessions that can't be matched to a device.",
        )}
      </p>
    </>
  );
}
