import { getLocale, localized, t, tn } from "../../i18n";
import { ApiError } from "../../lib/api";
import { RATING_LEVELS, canonicalRating } from "../../lib/contentRatings";
import type {
  AudienceMode,
  DeviceKind,
  FrequencyMode,
  NetworkMode,
  Preroll,
  PrerollInput,
  PrerollOrder,
  VideoProblem,
} from "./prerolls-api";

export const DEVICES = localized<{ value: DeviceKind; label: string }[]>(() => [
  { value: "tv", label: t("TV") },
  { value: "mobile", label: t("Phone & tablet") },
  { value: "desktop", label: t("Desktop app") },
  { value: "web", label: t("Web") },
]);

export const KIND_OPTIONS = localized<{ value: "movie" | "episode" | "both"; label: string }[]>(
  () => [
    { value: "movie", label: t("Movies") },
    { value: "episode", label: t("Episodes") },
    { value: "both", label: t("Both") },
  ],
);

export const AUDIENCE_OPTIONS = localized<{ value: AudienceMode; label: string }[]>(() => [
  { value: "everyone", label: t("Everyone") },
  { value: "only", label: t("Only these viewers") },
  { value: "except", label: t("Everyone except") },
]);

export const NETWORK_OPTIONS = localized<{ value: NetworkMode; label: string }[]>(() => [
  { value: "any", label: t("Home and away") },
  { value: "home", label: t("At home") },
  { value: "away", label: t("Away from home") },
]);

export const FREQUENCY_OPTIONS = localized<{ value: FrequencyMode; label: string; hint: string }[]>(
  () => [
    {
      value: "always",
      label: t("Every time"),
      hint: t("Plays before every matching title."),
    },
    {
      value: "daily",
      label: t("Once a day"),
      hint: t("Each viewer sees it at most once a day."),
    },
    {
      value: "hours",
      label: t("Every few hours"),
      hint: t("Each viewer sees it at most once in the hours you choose."),
    },
    {
      value: "title",
      label: t("Once per title"),
      hint: t("Each viewer sees it the first time they play a title, not again for that title."),
    },
  ],
);

export const ORDER_OPTIONS = localized<{ value: PrerollOrder; label: string }[]>(() => [
  { value: "before_trailers", label: t("Before trailers") },
  { value: "after_trailers", label: t("After trailers") },
]);

/** Month names for the date window, January first. */
export const MONTH_NAMES = localized<readonly string[]>(() => {
  const fmt = new Intl.DateTimeFormat(getLocale(), { month: "long", timeZone: "UTC" });
  return Array.from({ length: 12 }, (_, i) => fmt.format(Date.UTC(2000, i, 15)));
});

/** "Oct 1" for "10-01". */
function formatMonthDay(md: string): string {
  const [m, d] = md.split("-").map(Number);
  if (!m || !d) return md;
  return new Intl.DateTimeFormat(getLocale(), {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  }).format(Date.UTC(2000, m - 1, d));
}

function list(items: string[], type: "conjunction" | "disjunction"): string {
  return new Intl.ListFormat(getLocale(), { style: "long", type }).format(items);
}

/** "10 seconds", "1 min 35 s". */
export function clipLength(ms: number): string {
  const total = Math.round(ms / 1000);
  if (total < 60) return tn(total, "{count} second", "{count} seconds");
  const m = Math.floor(total / 60);
  const s = total % 60;
  return s
    ? t("{minutes} min {seconds} s", { minutes: m, seconds: s })
    : tn(m, "{count} minute", "{count} minutes");
}

/** What a pre-roll plays before, as " · "-joined full phrases. */
export function describePlaysBefore(
  p: PrerollInput,
  collectionName: (id: string) => string = (id) => id,
): string {
  const r = p.rules;
  const movies = r.kinds.includes("movie");
  const episodes = r.kinds.includes("episode");
  const genres = r.genres.length ? list(r.genres, "disjunction") : "";
  const parts: string[] = [];
  if (!movies && !episodes) parts.push(t("Nothing yet"));
  else if (genres)
    parts.push(
      movies && episodes
        ? t("{genres} movies and episodes", { genres })
        : movies
          ? t("{genres} movies", { genres })
          : t("{genres} episodes", { genres }),
    );
  else
    parts.push(
      movies && episodes ? t("Movies and episodes") : movies ? t("Movies") : t("Episodes"),
    );
  if (episodes && r.firstEpisodeOnly) parts.push(t("First episode of a session"));
  if (r.excludeGenres.length)
    parts.push(t("Not {genres}", { genres: list(r.excludeGenres, "disjunction") }));
  if (r.collections.length)
    parts.push(
      t("In {collections}", {
        collections: list(r.collections.map(collectionName), "disjunction"),
      }),
    );
  if (r.maxContentRating)
    parts.push(t("Rated {rating} or lower", { rating: canonicalRating(r.maxContentRating) }));
  if (r.dateFrom)
    parts.push(
      t("{start} – {end}", {
        start: formatMonthDay(r.dateFrom),
        end: formatMonthDay(r.dateTo || r.dateFrom),
      }),
    );
  return parts.join(" · ");
}

/** Who sees a pre-roll, as " · "-joined full phrases. */
export function describeWho(p: PrerollInput, viewerName: (id: number) => string): string {
  const r = p.rules;
  const names = r.audience.viewers.map(viewerName);
  const parts: string[] = [];
  const many = names.length > 3;
  if (r.audience.mode === "everyone" || (r.audience.mode === "except" && !names.length))
    parts.push(t("Everyone"));
  else if (!names.length) parts.push(t("No viewers chosen"));
  else if (r.audience.mode === "only")
    parts.push(
      many
        ? tn(names.length, "Only {count} viewer", "Only {count} viewers")
        : t("Only {viewers}", { viewers: list(names, "conjunction") }),
    );
  else
    parts.push(
      many
        ? tn(names.length, "Everyone except {count} viewer", "Everyone except {count} viewers")
        : t("Everyone except {viewers}", { viewers: list(names, "conjunction") }),
    );
  if (r.network === "home") parts.push(t("At home only"));
  if (r.network === "away") parts.push(t("Away from home only"));
  if (r.devices.length && r.devices.length < DEVICES.length)
    parts.push(
      t("On {devices}", {
        devices: list(
          r.devices.map((d) => DEVICES.find((x) => x.value === d)?.label ?? d),
          "disjunction",
        ),
      }),
    );
  return parts.join(" · ");
}

export function describeFrequency(f: PrerollInput["frequency"]): string {
  switch (f.mode) {
    case "always":
      return t("Every time");
    case "daily":
      return t("Once a day per viewer");
    case "hours":
      return tn(f.hours, "Once an hour per viewer", "Once every {count} hours per viewer");
    case "title":
      return t("Once per title per viewer");
  }
}

export function ratingOptions() {
  return [
    { value: "", label: t("Any rating") },
    ...RATING_LEVELS.map((l) => ({
      value: l.value,
      label: t("Up to {labels} ({name})", { labels: l.labels, name: l.name }),
    })),
  ];
}

export function problemSentence(problem: VideoProblem): string {
  switch (problem) {
    case "too_long":
      return t("This video is longer than 3 minutes. Choose a shorter clip.");
    case "convert_failed":
      return t("Ether couldn't convert this video. Try another file, or export it again as MP4.");
    default:
      return t("Ether couldn't read this video. Check that it's a working video file.");
  }
}

/** A plain sentence for each simulator reason, passed or failed. */
export function reasonSentence(code: string, ok: boolean): string {
  switch (code) {
    case "disabled":
      return ok ? t("It's turned on.") : t("It's turned off.");
    case "not_ready":
      return ok ? t("Its video is ready.") : t("Its video isn't ready yet.");
    case "unsupported_app":
      return ok ? t("This app can show pre-rolls.") : t("This app can't show pre-rolls.");
    case "resumed":
      return ok
        ? t("The title starts from the beginning.")
        : t("Pre-rolls don't play when a title is resumed.");
    case "kind":
      return ok
        ? t("It plays before this kind of title.")
        : t("It doesn't play before this kind of title.");
    case "first_episode":
      return ok
        ? t("This is the first episode of the session.")
        : t("It only plays before the first episode of a session.");
    case "binge_gap":
      return ok
        ? t("This starts a new viewing session.")
        : t("The viewer is still in the middle of a viewing session.");
    case "genre":
      return ok ? t("The title has one of its genres.") : t("The title has none of its genres.");
    case "excluded_genre":
      return ok
        ? t("The title has none of its excluded genres.")
        : t("The title has a genre it excludes.");
    case "collection":
      return ok
        ? t("The title is in one of its collections.")
        : t("The title isn't in any of its collections.");
    case "rating":
      return ok
        ? t("The title is within its rating limit.")
        : t("The title is rated above its limit.");
    case "date_window":
      return ok ? t("The date is within its dates.") : t("The date is outside its dates.");
    case "audience":
      return ok ? t("It plays for this viewer.") : t("It doesn't play for this viewer.");
    case "network":
      return ok
        ? t("It plays for this location (at home or away from home).")
        : t("It doesn't play for this location (at home or away from home).");
    case "device":
      return ok
        ? t("It plays on this kind of device.")
        : t("It doesn't play on this kind of device.");
    case "frequency":
      return ok ? t("The viewer is due to see it.") : t("The viewer saw it too recently.");
    case "lower_priority":
      return t("Another matching pre-roll has a higher priority.");
    case "shuffled_out":
      return t("Shuffle picked another matching pre-roll.");
    default:
      return ok
        ? t("Ether checked a condition this console doesn't know yet, and it passed.")
        : t("Ether checked a condition this console doesn't know yet, and it failed.");
  }
}

export type PlainError = { message: string; detail?: string };

/**
 * A plain-language error for a failed request: `what` says what failed,
 * and the server's own words ride along as the detail when they add
 * something (a validation message, for instance).
 */
export function plainError(e: unknown, what: string): PlainError {
  if (!(e instanceof ApiError)) return { message: what };
  if (e.status === 0)
    return {
      message: t("Ether can't be reached. Check that the server is running, then try again."),
    };
  if (e.status === 413) return { message: t("That file is too large to upload.") };
  if (e.status === 401 || e.status === 403)
    return { message: t("Your session has ended. Sign in again, then try again.") };
  if (e.status === 404)
    return {
      message: t("That pre-roll no longer exists. Reload the page to see the current list."),
    };
  const detail = e.fields?.length ? e.fields.map((f) => f.message).join(" ") : e.message;
  return { message: what, detail: detail?.trim() || undefined };
}

/** The video state a row and the drawer both read. */
export function videoState(p: Pick<Preroll, "video">): "none" | "processing" | "ready" | "failed" {
  return p.video.state === "" ? "none" : p.video.state;
}
