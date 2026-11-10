import { Spinner, cn } from "@braedonsaunders/appkit-ui";
import { ArrowUpRight, CalendarDays, Moon } from "lucide-react";
import { Link } from "react-router-dom";
import { Artwork } from "../../../components/Artwork";
import { ErrorPanel } from "../../../components/feedback";
import { t, tn } from "../../../i18n";
import type { TitleDetail } from "../../../lib/contract";
import type { AsyncState } from "../../../lib/hooks";
import { daysBetween, formatDay, localToday, relativeDay } from "../../calendar/model";
import { statusLabel } from "./TitleInspector";
import { EpisodeBadges } from "./TitleEpisodes";
import {
  capitalized,
  longDate,
  shortCode,
  type ScheduleEpisode,
  type ShowSchedule,
} from "./titleSchedule";

function group(list: ScheduleEpisode[]): [string, ScheduleEpisode[]][] {
  const m = new Map<string, ScheduleEpisode[]>();
  for (const e of list) {
    const d = e.airDate ?? "";
    const got = m.get(d);
    if (got) got.push(e);
    else m.set(d, [e]);
  }
  return [...m.entries()];
}

/** "This week", "Next week", "Later this month"… for the agenda's section labels. */
function horizon(date: string, today: string): string {
  const n = daysBetween(today, date);
  if (n <= 6) return t("This week");
  if (n <= 13) return t("Next week");
  if (n <= 31) return t("Later this month");
  return t("Further out");
}

/**
 * An agenda of the show's upcoming episodes that fits the narrow drawer:
 * today first, then dates grouped into weeks, then announced episodes
 * without a date, then the last few weeks of aired episodes.
 */
export function TitleCalendar({
  title,
  schedule,
  onOpen,
}: {
  title: TitleDetail;
  schedule: AsyncState<ShowSchedule | null>;
  onOpen: (e: ScheduleEpisode) => void;
}) {
  const today = localToday();
  if (schedule.error)
    return (
      <ErrorPanel className="m-5" error={schedule.error} onRetry={() => void schedule.reload()} />
    );
  if (!schedule.data)
    return (
      <div className="grid min-h-64 place-items-center">
        <Spinner label={t("Loading the episode schedule")} />
      </div>
    );
  const all = schedule.data.episodes ?? [];
  const upcoming = all.filter((e) => e.airDate && e.airDate >= today);
  const undated = all.filter((e) => !e.airDate);
  const recent = all.filter((e) => e.airDate && e.airDate < today).reverse();
  const days = group(upcoming);
  const hasToday = days.some(([d]) => d === today);
  let lastHorizon = "";

  return (
    <div className="catalog-tab">
      <div className="catalog-cal-summary">
        <CalendarDays size={18} aria-hidden />
        <p>
          {upcoming.length
            ? tn(upcoming.length, "{count} upcoming episode", "{count} upcoming episodes")
            : undated.length
              ? t("New episodes announced; dates to follow")
              : t("Nothing scheduled")}
          {title.status ? <span> · {statusLabel(title.status)}</span> : null}
        </p>
        <Link to="/calendar" className="catalog-link">
          {t("Household calendar")}
          <ArrowUpRight size={13} aria-hidden />
        </Link>
      </div>

      <ol className="catalog-agenda">
        {!hasToday ? (
          <li className="catalog-agenda-day is-today">
            <DayChip date={today} today={today} />
            <p className="catalog-agenda-none">
              <Moon size={14} aria-hidden />
              {t("Nothing airs today")}
            </p>
          </li>
        ) : null}
        {days.map(([date, eps]) => {
          const h = date === today ? "" : horizon(date, today);
          const heading = h && h !== lastHorizon ? h : "";
          if (h) lastHorizon = h;
          return (
            <li key={date} className={cn("catalog-agenda-day", date === today && "is-today")}>
              {heading ? <p className="catalog-agenda-horizon">{heading}</p> : null}
              <DayChip date={date} today={today} />
              <div className="catalog-agenda-items">
                {eps.length > 2 ? (
                  <p className="catalog-agenda-batch">
                    {t("{count} episodes drop at once", { count: eps.length })}
                  </p>
                ) : null}
                {eps.map((e) => (
                  <AgendaItem key={e.key} e={e} onOpen={onOpen} />
                ))}
              </div>
            </li>
          );
        })}
      </ol>
      {!upcoming.length && !undated.length ? (
        <p className="catalog-empty">
          {schedule.data.last
            ? t("No future episodes are announced. TMDB adds them as networks confirm them.")
            : t("TMDB has no episode dates for this show.")}
        </p>
      ) : null}

      {undated.length ? (
        <section className="catalog-agenda-section">
          <h3>{t("Announced, date to follow")}</h3>
          <div className="catalog-agenda-items">
            {undated.map((e) => (
              <AgendaItem key={e.key} e={e} onOpen={onOpen} />
            ))}
          </div>
        </section>
      ) : null}

      {recent.length ? (
        <section className="catalog-agenda-section">
          <h3>{t("Recently aired")}</h3>
          <div className="catalog-agenda-items">
            {recent.map((e) => (
              <AgendaItem key={e.key} e={e} onOpen={onOpen} withDate today={today} />
            ))}
          </div>
        </section>
      ) : null}
    </div>
  );
}

function DayChip({ date, today }: { date: string; today: string }) {
  const near = Math.abs(daysBetween(today, date)) <= 6;
  return (
    <div className="catalog-agenda-date">
      <span className="catalog-agenda-chip" aria-hidden>
        <small>{formatDay.monthShort(date)}</small>
        <strong>{formatDay.number(date)}</strong>
      </span>
      <span>
        <strong>
          {near ? capitalized(relativeDay(date, today)) : formatDay.weekdayLong(date)}
        </strong>
        <small>{longDate(date)}</small>
      </span>
    </div>
  );
}

function AgendaItem({
  e,
  onOpen,
  withDate,
  today,
}: {
  e: ScheduleEpisode;
  onOpen: (e: ScheduleEpisode) => void;
  withDate?: boolean;
  today?: string;
}) {
  return (
    <button type="button" className="catalog-agenda-item" onClick={() => onOpen(e)}>
      <Artwork src={e.still} alt="" className="aspect-video w-24 shrink-0 rounded" />
      <span className="min-w-0 flex-1">
        <span className="catalog-agenda-code">{shortCode(e)}</span>
        <span className="catalog-agenda-title">{e.title}</span>
        <span className="catalog-agenda-meta">
          {withDate && e.airDate && today ? capitalized(relativeDay(e.airDate, today)) : null}
          {withDate && e.runtimeMinutes ? " · " : null}
          {e.runtimeMinutes ? t("{minutes} min", { minutes: e.runtimeMinutes }) : null}
          <EpisodeBadges e={e} />
        </span>
      </span>
    </button>
  );
}
