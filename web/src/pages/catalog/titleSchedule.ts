import { getLocale, localized, t } from "../../i18n";
import { apiFetch } from "../../lib/api";

/** One episode in a show's schedule; airDate is YYYY-MM-DD, empty when not announced. */
export interface ScheduleEpisode {
  key: string;
  season: number;
  episode: number;
  title: string;
  overview?: string;
  airDate?: string;
  runtimeMinutes?: number;
  still?: string;
  rating?: number;
  aired: boolean;
  premiere?: boolean;
  finale?: boolean;
}

interface ScheduleSeason {
  season: number;
  title: string;
  overview?: string;
  airDate?: string;
  poster?: string;
  episodes: number;
  aired: number;
  /** episodes is TMDB's count; the season's episodes load when it opens. */
  unlisted?: boolean;
}

export interface ShowSchedule {
  seasons: ScheduleSeason[] | null;
  next: ScheduleEpisode | null;
  last: ScheduleEpisode | null;
  /** Episodes from about six weeks ago onward, plus announced undated ones. */
  episodes: ScheduleEpisode[] | null;
}

export type ReleaseKind = "premiere" | "limited" | "theatrical" | "digital" | "physical" | "tv";

export interface ReleaseDateEntry {
  country: string;
  kind: ReleaseKind;
  date: string;
  note?: string;
  certification?: string;
}

export interface MovieReleases {
  region: string;
  dates: ReleaseDateEntry[] | null;
}

const enc = encodeURIComponent;

export const titleScheduleApi = {
  show: (key: string, signal?: AbortSignal) =>
    apiFetch<ShowSchedule>(`/catalog/title/${enc(key)}/schedule`, { signal }),
  season: async (key: string, season: number, signal?: AbortSignal) =>
    (
      await apiFetch<{ season: number; episodes: ScheduleEpisode[] | null }>(
        `/catalog/title/${enc(key)}/season/${season}`,
        { signal },
      )
    ).episodes ?? [],
  releases: (key: string, signal?: AbortSignal) =>
    apiFetch<MovieReleases>(`/catalog/title/${enc(key)}/releases`, { signal }),
};

export const RELEASE_KIND_LABEL = localized<Record<ReleaseKind, string>>(() => ({
  premiere: t("Premiere"),
  limited: t("Limited theatrical"),
  theatrical: t("Theatrical"),
  digital: t("Digital"),
  physical: t("Physical"),
  tv: t("TV broadcast"),
}));

/** "S2 · E5" style short code used in compact rows. */
export function shortCode(e: Pick<ScheduleEpisode, "season" | "episode">): string {
  return e.season === 0
    ? t("Special {number}", { number: e.episode })
    : t("S{season} · E{episode}", { season: e.season, episode: e.episode });
}

/** Country name for an ISO 3166-1 code, falling back to the code. */
export function countryName(code: string, locale: string): string {
  try {
    return new Intl.DisplayNames([locale], { type: "region" }).of(code) ?? code;
  } catch {
    return code;
  }
}

function fmtDay(s: string, opts: Intl.DateTimeFormatOptions): string {
  const [y, m, d] = s.split("-").map(Number);
  return new Intl.DateTimeFormat(getLocale(), { ...opts, timeZone: "UTC" }).format(
    new Date(Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1)),
  );
}

/** "Fri, Mar 7", with the year when it is not this year. */
export function shortDate(s: string, today: string): string {
  return s.slice(0, 4) === today.slice(0, 4)
    ? fmtDay(s, { weekday: "short", month: "short", day: "numeric" })
    : fmtDay(s, { month: "short", day: "numeric", year: "numeric" });
}

/** "Friday, March 7, 2025". */
export function longDate(s: string): string {
  return fmtDay(s, { weekday: "long", month: "long", day: "numeric", year: "numeric" });
}

/** "In 6 days" for a label that starts a line ("in 6 days" from Intl). */
export function capitalized(s: string): string {
  return s ? s.charAt(0).toLocaleUpperCase(getLocale()) + s.slice(1) : s;
}
