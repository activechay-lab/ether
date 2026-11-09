import { Badge, Button, Spinner, cn } from "@braedonsaunders/appkit-ui";
import { CalendarClock, ChevronDown, Clock, Pin, Search, Star, Tv } from "lucide-react";
import { SeasonPinDrawer } from "../releases/SeasonPinDrawer";
import { useEffect, useRef, useState } from "react";
import { Artwork } from "../../components/Artwork";
import { ErrorPanel } from "../../components/feedback";
import { t } from "../../i18n";
import type { TitleDetail } from "../../lib/contract";
import { fixed } from "../../lib/format";
import { useAsync, type AsyncState } from "../../lib/hooks";
import { daysBetween, localToday, relativeDay } from "../calendar/model";
import type { EpisodeFocus } from "./TitleInspector";
import {
  capitalized,
  longDate,
  shortCode,
  shortDate,
  titleScheduleApi,
  type ScheduleEpisode,
  type ShowSchedule,
} from "./titleSchedule";

/** Aired, airing today, upcoming, or not yet dated — judged in the browser's day. */
type EpisodeState = "aired" | "today" | "upcoming" | "unannounced";

export function episodeState(e: Pick<ScheduleEpisode, "airDate">, today: string): EpisodeState {
  if (!e.airDate) return "unannounced";
  if (e.airDate === today) return "today";
  return e.airDate < today ? "aired" : "upcoming";
}

/** "Tomorrow · Thu, Nov 12" near today, "Nov 12, 2026"-ish otherwise. */
export function airLabel(date: string | undefined, today: string): string {
  if (!date) return t("Not announced");
  const n = Math.abs(daysBetween(today, date));
  if (n <= 6) return `${capitalized(relativeDay(date, today))} · ${shortDate(date, today)}`;
  return shortDate(date, today);
}

function StateBadge({ state }: { state: EpisodeState }) {
  if (state === "today") return <Badge variant="success">{t("Airs today")}</Badge>;
  if (state === "upcoming") return <Badge variant="secondary">{t("Upcoming")}</Badge>;
  if (state === "unannounced") return <Badge variant="outline">{t("Date not announced")}</Badge>;
  return null;
}

export function EpisodeBadges({ e }: { e: ScheduleEpisode }) {
  return (
    <>
      {e.premiere && e.season > 0 ? (
        <span className="catalog-ep-tag">
          {e.season === 1 ? t("Series premiere") : t("Season premiere")}
        </span>
      ) : null}
      {e.finale ? <span className="catalog-ep-tag">{t("Season finale")}</span> : null}
    </>
  );
}

/**
 * The first thing the Overview shows for a series: when the next episode
 * airs, or that the show has ended. Opening it jumps to the episode.
 */
export function NextEpisodeCard({
  status,
  next,
  last,
  loading,
  onOpen,
}: {
  status?: string;
  next: ScheduleEpisode | null;
  last: ScheduleEpisode | null;
  loading: boolean;
  onOpen: (e: ScheduleEpisode) => void;
}) {
  const today = localToday();
  if (loading)
    return (
      <div className="catalog-next is-quiet">
        <Spinner size={16} label={t("Loading the episode schedule")} />
      </div>
    );
  const s = (status ?? "").toLowerCase();
  const ended = s === "ended" || s === "canceled" || s === "cancelled";
  if (!next) {
    return (
      <div className="catalog-next is-quiet">
        <Tv size={18} aria-hidden />
        <div className="min-w-0 flex-1">
          <p className="catalog-next-eyebrow">
            {ended
              ? s === "ended"
                ? t("Series ended")
                : t("Series cancelled")
              : t("No new episodes announced")}
          </p>
          {last ? (
            <button type="button" className="catalog-next-title" onClick={() => onOpen(last)}>
              {t("Last aired {code} · {title}, {date}", {
                code: shortCode(last),
                title: last.title,
                date: airLabel(last.airDate, today),
              })}
            </button>
          ) : null}
        </div>
      </div>
    );
  }
  const state = episodeState(next, today);
  const days = next.airDate ? daysBetween(today, next.airDate) : null;
  return (
    <button type="button" className="catalog-next" onClick={() => onOpen(next)}>
      <Artwork
        src={next.still}
        alt=""
        icon={<CalendarClock size={18} />}
        className="aspect-video w-32 shrink-0 rounded-md"
      />
      <div className="min-w-0 flex-1 text-left">
        <p className="catalog-next-eyebrow">
          {state === "today" ? t("New episode today") : t("Next episode")}
        </p>
        <p className="catalog-next-title">
          {shortCode(next)} · {next.title}
        </p>
        <p className="catalog-next-when">
          {next.airDate ? longDate(next.airDate) : t("Air date not announced yet")}
          <EpisodeBadges e={next} />
        </p>
      </div>
      {days != null && days > 0 ? (
        <span className="catalog-next-count" aria-label={relativeDay(next.airDate!, today)}>
          <strong>{days}</strong>
          <span>{days === 1 ? t("day") : t("days")}</span>
        </span>
      ) : null}
    </button>
  );
}

function defaultSeason(s: ShowSchedule | undefined | null): number | null {
  if (!s) return null;
  if (s.next) return s.next.season;
  if (s.last) return s.last.season;
  const seasons = (s.seasons ?? []).filter((x) => x.season > 0);
  return seasons.length ? seasons[seasons.length - 1]!.season : (s.seasons?.[0]?.season ?? null);
}

/** Season picker and a compact episode list; rows expand for synopsis and sources. */
export function TitleEpisodes({
  title,
  schedule,
  focus,
  onCheck,
}: {
  title: TitleDetail;
  schedule: AsyncState<ShowSchedule | null>;
  focus: EpisodeFocus | null;
  onCheck: (e: ScheduleEpisode) => void;
}) {
  const [picked, setPicked] = useState<number | null>(null);
  const season = picked ?? focus?.season ?? defaultSeason(schedule.data);
  // Newest first, the way people browse a long-running show; specials last.
  const seasons = [...(schedule.data?.seasons ?? [])].sort((a, b) =>
    a.season === 0 ? 1 : b.season === 0 ? -1 : b.season - a.season,
  );
  const chips = useRef<HTMLDivElement>(null);
  useEffect(() => {
    chips.current
      ?.querySelector<HTMLElement>('[aria-selected="true"]')
      ?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [season, schedule.data]);
  const episodes = useAsync(
    (signal) =>
      season == null
        ? Promise.resolve([] as ScheduleEpisode[])
        : titleScheduleApi.season(title.key, season, signal),
    [title.key, season],
  );
  const [open, setOpen] = useState<string | null>(focus?.key ?? null);
  const [pinningSeason, setPinningSeason] = useState(false);
  const focused = useRef<HTMLLIElement>(null);
  useEffect(() => {
    if (focus?.key && episodes.data?.length)
      focused.current?.scrollIntoView({ block: "center", behavior: "smooth" });
  }, [focus?.key, episodes.data]);
  const today = localToday();

  if (schedule.error)
    return (
      <ErrorPanel className="m-5" error={schedule.error} onRetry={() => void schedule.reload()} />
    );
  if (!schedule.data)
    return (
      <div className="grid min-h-64 place-items-center">
        <Spinner label={t("Loading seasons")} />
      </div>
    );
  if (!seasons.length)
    return <p className="catalog-empty">{t("TMDB lists no seasons for this show yet.")}</p>;
  const current = seasons.find((s) => s.season === season);

  return (
    <div className="catalog-tab">
      <div ref={chips} className="catalog-seasons" role="tablist" aria-label={t("Seasons")}>
        {seasons.map((s) => {
          const upcoming = !s.unlisted && s.episodes > s.aired;
          return (
            <button
              key={s.season}
              type="button"
              role="tab"
              aria-selected={s.season === season}
              className={cn("catalog-season", s.season === season && "is-active")}
              onClick={() => {
                setPicked(s.season);
                setOpen(null);
              }}
            >
              <span>
                {s.season === 0 ? t("Specials") : t("Season {number}", { number: s.season })}
              </span>
              <small>
                {s.episodes
                  ? upcoming && s.aired
                    ? t("{aired} of {count} aired", { aired: s.aired, count: s.episodes })
                    : upcoming
                      ? t("Upcoming")
                      : t("{count} episodes", { count: s.episodes })
                  : s.airDate
                    ? shortDate(s.airDate, today)
                    : t("Announced")}
              </small>
            </button>
          );
        })}
      </div>
      {current ? (
        <div className="catalog-season-head flex-wrap">
          <Artwork
            src={current.poster}
            alt=""
            className="aspect-[2/3] w-14 shrink-0 rounded-md"
            icon={<Tv size={16} />}
          />
          <div className="min-w-0 flex-1">
            <h3>{current.title}</h3>
            <p>
              {[
                current.airDate
                  ? t("Premiered {date}", { date: longDate(current.airDate) })
                  : t("Premiere date not announced"),
                current.episodes ? t("{count} episodes", { count: current.episodes }) : null,
              ]
                .filter(Boolean)
                .join(" · ")}
            </p>
          </div>
          {season != null &&
          (current.aired > 0 ||
            (episodes.data ?? []).some((e) => {
              const st = episodeState(e, today);
              return st === "aired" || st === "today";
            })) ? (
            <Button size="sm" variant="outline" onClick={() => setPinningSeason(true)}>
              <Pin size={14} aria-hidden />
              {t("Pin a release for every episode")}
            </Button>
          ) : null}
        </div>
      ) : null}
      {episodes.error ? (
        <ErrorPanel error={episodes.error} onRetry={() => void episodes.reload()} />
      ) : !episodes.data ? (
        <div className="grid min-h-40 place-items-center">
          <Spinner label={t("Loading episodes")} />
        </div>
      ) : !episodes.data.length ? (
        <p className="catalog-empty">{t("TMDB has not listed this season's episodes yet.")}</p>
      ) : (
        <ol className="catalog-episodes">
          {episodes.data.map((e) => {
            const state = episodeState(e, today);
            const expanded = open === e.key;
            return (
              <li
                key={e.key}
                ref={focus?.key === e.key ? focused : undefined}
                className={cn("catalog-episode", `is-${state}`, expanded && "is-open")}
              >
                <button
                  type="button"
                  className="catalog-episode-row"
                  aria-expanded={expanded}
                  onClick={() => setOpen(expanded ? null : e.key)}
                >
                  <span className="catalog-episode-num">{e.episode}</span>
                  <span className="min-w-0 flex-1">
                    <span className="catalog-episode-title">{e.title}</span>
                    <span className="catalog-episode-meta">
                      {airLabel(e.airDate, today)}
                      {e.runtimeMinutes
                        ? ` · ${t("{minutes} min", { minutes: e.runtimeMinutes })}`
                        : ""}
                      <EpisodeBadges e={e} />
                    </span>
                  </span>
                  <StateBadge state={state} />
                  <ChevronDown size={15} className="catalog-episode-chevron" aria-hidden />
                </button>
                {expanded ? (
                  <div className="catalog-episode-detail">
                    {e.still ? (
                      <Artwork
                        src={e.still}
                        alt=""
                        className="catalog-episode-still aspect-video w-full rounded-md"
                      />
                    ) : null}
                    <p>
                      {e.overview ||
                        (state === "aired"
                          ? t("TMDB has no synopsis for this episode.")
                          : t("No synopsis yet. TMDB usually adds one closer to the air date."))}
                    </p>
                    <div className="flex flex-wrap items-center gap-3 text-xs text-fg-muted">
                      {e.airDate ? (
                        <span className="inline-flex items-center gap-1">
                          <CalendarClock size={13} aria-hidden />
                          {longDate(e.airDate)}
                        </span>
                      ) : null}
                      {e.runtimeMinutes ? (
                        <span className="inline-flex items-center gap-1">
                          <Clock size={13} aria-hidden />
                          {t("{minutes} min", { minutes: e.runtimeMinutes })}
                        </span>
                      ) : null}
                      {e.rating ? (
                        <span className="inline-flex items-center gap-1">
                          <Star size={13} aria-hidden />
                          {fixed(e.rating, 1)}
                        </span>
                      ) : null}
                    </div>
                    {state === "aired" || state === "today" ? (
                      <Button size="sm" variant="outline" onClick={() => onCheck(e)}>
                        <Search size={14} aria-hidden />
                        {t("Check sources")}
                      </Button>
                    ) : (
                      <p className="text-xs text-fg-subtle">
                        {t("Ether can play this episode once it airs.")}
                      </p>
                    )}
                  </div>
                ) : null}
              </li>
            );
          })}
        </ol>
      )}
      <SeasonPinDrawer
        open={pinningSeason && season != null}
        target={season != null ? { show: title.key, season } : null}
        label={current ? `${title.title} · ${current.title}` : title.title}
        onClose={() => setPinningSeason(false)}
      />
    </div>
  );
}
