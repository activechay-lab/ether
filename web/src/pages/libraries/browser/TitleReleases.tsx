import { Badge, Spinner, cn } from "@braedonsaunders/appkit-ui";
import { Clapperboard, Disc3, MonitorPlay, Sparkles, Ticket, Tv } from "lucide-react";
import { Fragment, useState } from "react";
import { ErrorPanel } from "../../../components/feedback";
import { getLocale, t } from "../../../i18n";
import type { AsyncState } from "../../../lib/hooks";
import { daysBetween, localToday, relativeDay } from "../../calendar/model";
import {
  RELEASE_KIND_LABEL,
  countryName,
  longDate,
  shortDate,
  type MovieReleases,
  type ReleaseDateEntry,
  type ReleaseKind,
} from "./titleSchedule";

const ICON: Record<ReleaseKind, typeof Ticket> = {
  premiere: Sparkles,
  limited: Clapperboard,
  theatrical: Ticket,
  digital: MonitorPlay,
  physical: Disc3,
  tv: Tv,
};

/** The milestones the owner cares about, earliest in their region, else anywhere. */
const MILESTONES: { kinds: ReleaseKind[]; label: () => string }[] = [
  { kinds: ["theatrical", "limited"], label: () => t("In theatres") },
  { kinds: ["digital"], label: () => t("Digital") },
  { kinds: ["physical"], label: () => t("Disc") },
];

/** The earliest release of these kinds in the region, else anywhere (dates arrive sorted). */
export function milestone(dates: ReleaseDateEntry[], kinds: ReleaseKind[], region: string) {
  const pool = dates.filter((d) => kinds.includes(d.kind));
  return pool.find((d) => d.country === region) ?? pool[0];
}

/**
 * A movie's release timeline: the theatrical, digital and disc milestones up
 * top, then every dated release, in the owner's region or worldwide.
 */
export function TitleReleases({ releases: state }: { releases: AsyncState<MovieReleases | null> }) {
  const [everywhere, setEverywhere] = useState(false);
  const today = localToday();
  const locale = getLocale();

  if (state.error)
    return <ErrorPanel className="m-5" error={state.error} onRetry={() => void state.reload()} />;
  if (!state.data)
    return (
      <div className="grid min-h-64 place-items-center">
        <Spinner label={t("Loading release dates")} />
      </div>
    );
  const region = (state.data.region || "US").toUpperCase();
  const dates = state.data.dates ?? [];
  if (!dates.length)
    return <p className="catalog-empty">{t("TMDB has no release dates for this movie yet.")}</p>;
  const local = dates.filter((d) => d.country === region);
  const shown = everywhere || !local.length ? dates : local;
  const regionName = countryName(region, locale);
  let todayMarked = false;

  return (
    <div className="catalog-tab">
      <div className="catalog-milestones">
        {MILESTONES.map((m) => {
          const d = milestone(dates, m.kinds, region);
          const past = d ? d.date <= today : false;
          return (
            <div key={m.kinds[0]} className={cn("catalog-milestone", past && "is-past")}>
              <span>{m.label()}</span>
              <strong>{d ? shortDate(d.date, today) : t("Not announced")}</strong>
              <small>
                {d
                  ? [
                      d.country !== region ? countryName(d.country, locale) : null,
                      Math.abs(daysBetween(today, d.date)) <= 30
                        ? relativeDay(d.date, today)
                        : null,
                    ]
                      .filter(Boolean)
                      .join(" · ") || regionName
                  : t("No date in any country yet")}
              </small>
            </div>
          );
        })}
      </div>
      <p className="catalog-hint">
        {t(
          "New movies stay hidden from viewers until a digital or disc release exists and a playable source is found.",
        )}
      </p>

      <div className="catalog-toggle" role="radiogroup" aria-label={t("Release dates to show")}>
        <button
          type="button"
          role="radio"
          aria-checked={!everywhere}
          disabled={!local.length}
          onClick={() => setEverywhere(false)}
        >
          {local.length ? regionName : t("{country} (none)", { country: regionName })}
        </button>
        <button
          type="button"
          role="radio"
          aria-checked={everywhere || !local.length}
          onClick={() => setEverywhere(true)}
        >
          {t("All countries ({count})", { count: new Set(dates.map((d) => d.country)).size })}
        </button>
      </div>

      <ol className="catalog-timeline">
        {shown.map((d, i) => {
          const Icon = ICON[d.kind];
          const markToday = !todayMarked && d.date > today;
          if (markToday) todayMarked = true;
          return (
            <Fragment key={`${d.country}-${d.kind}-${d.date}-${i}`}>
              {markToday ? (
                <li className="catalog-timeline-today" aria-label={t("Today")}>
                  {t("Today")}
                </li>
              ) : null}
              <li className={cn(d.date <= today ? "is-past" : "is-future")}>
                <span className="catalog-timeline-dot">
                  <Icon size={13} aria-hidden />
                </span>
                <div className="min-w-0 flex-1">
                  <p className="catalog-timeline-title">
                    {RELEASE_KIND_LABEL[d.kind]}
                    {everywhere || !local.length ? (
                      <span className="text-fg-muted"> · {countryName(d.country, locale)}</span>
                    ) : null}
                  </p>
                  <p className="catalog-timeline-meta">
                    {longDate(d.date)}
                    {d.note ? ` · ${d.note}` : ""}
                  </p>
                </div>
                {d.certification ? <Badge variant="outline">{d.certification}</Badge> : null}
              </li>
            </Fragment>
          );
        })}
      </ol>
    </div>
  );
}
