import { localized, t, tn } from "../../i18n";
import { Meter } from "../../components/charts";
import { formatNumber, formatPercent } from "../../lib/format";

/** GET /indexer-usage: real API requests per indexer, as the indexer counts them. */
type UsageCount = { purpose: string; hits: number; grabs: number };
type UsageWindow = { hits: number; grabs: number; purposes: UsageCount[] | null };
type UsageDay = { day: string; hits: number; grabs: number };
type IndexerUsage = {
  id: string;
  name: string;
  enabled: boolean;
  torrent: boolean;
  dailyLimit: number;
  dailyGrabs: number;
  retryAt?: number;
  today: UsageWindow;
  week: UsageWindow;
  month: UsageWindow;
  days: UsageDay[] | null;
};
export type UsageReport = {
  today: string;
  weekStart: string;
  monthStart: string;
  indexers: IndexerUsage[] | null;
};

export type UsagePeriod = "today" | "week" | "month";

export const USAGE_PERIODS = localized(
  () =>
    [
      { value: "today", label: t("Today") },
      { value: "week", label: t("Last 7 days") },
      { value: "month", label: t("This month") },
    ] as const,
);

/** What each purpose means, in the owner's words. Unknown purposes read as Other. */
const PURPOSES = localized((): Record<string, { label: string; detail: string }> => ({
  playback: {
    label: t("Playback searches"),
    detail: t("Someone pressed Play, or playback switched to another release."),
  },
  warmup: {
    label: t("Warm-ups and prefetch"),
    detail: t(
      "Getting titles ready before Play: opened details, next episodes, watchlists and the managed cache.",
    ),
  },
  feed: {
    label: t("Release-feed polls"),
    detail: t("Reading the indexer's newest releases so new movies and episodes show up."),
  },
  availability: {
    label: t("Availability checks"),
    detail: t("Background checks that a title has a playable release."),
  },
  rescue: {
    label: t("Rescue searches"),
    detail: t("Wider searches when the first search found nothing playable."),
  },
  caps: {
    label: t("Capability checks"),
    detail: t("Asking the indexer which searches it supports. Refreshed once a day."),
  },
  test: {
    label: t("Manual tests"),
    detail: t("The Test button on an indexer. These never use the daily limit."),
  },
  console: {
    label: t("Release searches in the console"),
    detail: t("Searches and checks you ran from Releases."),
  },
  other: {
    label: t("Other requests"),
    detail: t("Requests from plugins and other tools."),
  },
  unattributed: {
    label: t("Before this breakdown"),
    detail: t(
      "Counted before Ether recorded why each request was made, or by a server that has not been updated yet.",
    ),
  },
}));

function purposeInfo(purpose: string) {
  return PURPOSES[purpose] ?? PURPOSES.other!;
}

function periodLabel(period: UsagePeriod) {
  return USAGE_PERIODS.find((p) => p.value === period)?.label ?? "";
}

/** One indexer's daily-limit meters, window totals and purpose breakdown. */
export function IndexerUsageDetails({
  usage,
  period,
}: {
  usage: IndexerUsage;
  period: UsagePeriod;
}) {
  const window = usage[period];
  const purposes = (window.purposes ?? []).filter((p) => p.hits > 0 || p.grabs > 0);
  const hitsTotal = window.hits || 1;
  return (
    <div className="space-y-3">
      <div className="grid max-w-xl gap-3 sm:grid-cols-2">
        <DailyMeter used={usage.today.hits} limit={usage.dailyLimit} kind="hits" />
        <DailyMeter used={usage.today.grabs} limit={usage.dailyGrabs} kind="grabs" />
      </div>
      <dl className="grid grid-cols-3 gap-x-6 gap-y-1 text-xs sm:max-w-xl">
        {USAGE_PERIODS.map((p) => (
          <div key={p.value} className="min-w-0">
            <dt className="text-fg-subtle">{p.label}</dt>
            <dd className="tabular-nums text-fg">
              {tn(usage[p.value].hits, "{count} API hit", "{count} API hits")}
              <span className="block text-fg-muted">
                {tn(usage[p.value].grabs, "{count} download", "{count} downloads")}
              </span>
            </dd>
          </div>
        ))}
      </dl>
      <div className="space-y-1.5">
        <p className="text-xs font-medium text-fg">
          {t("What used API hits · {period}", { period: periodLabel(period) })}
        </p>
        {purposes.length ? (
          <ul className="max-w-xl space-y-1.5" aria-label={t("API hits by purpose")}>
            {purposes.map((p) => {
              const info = purposeInfo(p.purpose);
              const share = p.hits / hitsTotal;
              return (
                <li key={p.purpose} className="text-xs" title={info.detail}>
                  <div className="flex items-baseline justify-between gap-3">
                    <span className="min-w-0 truncate text-fg">{info.label}</span>
                    <span className="shrink-0 tabular-nums text-fg-muted">
                      {[
                        tn(p.hits, "{count} hit", "{count} hits"),
                        window.hits ? formatPercent(share, 0) : "",
                        p.grabs ? tn(p.grabs, "{count} download", "{count} downloads") : "",
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </span>
                  </div>
                  <div className="mt-1 h-1 overflow-hidden rounded-full bg-bg-subtle">
                    <div
                      className="h-full rounded-full bg-primary/70"
                      style={{ width: `${Math.max(p.hits ? 2 : 0, share * 100)}%` }}
                    />
                  </div>
                  <p className="mt-0.5 text-fg-subtle">{info.detail}</p>
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="text-xs text-fg-muted">
            {t("No API hits · {period}", { period: periodLabel(period) })}
          </p>
        )}
      </div>
    </div>
  );
}

function DailyMeter({
  used,
  limit,
  kind,
}: {
  used: number;
  limit: number;
  kind: "hits" | "grabs";
}) {
  if (limit <= 0)
    return (
      <p className="text-xs text-fg-muted">
        {kind === "hits"
          ? tn(
              used,
              "{count} API hit today · no daily limit set",
              "{count} API hits today · no daily limit set",
            )
          : tn(
              used,
              "{count} download today · no daily limit set",
              "{count} downloads today · no daily limit set",
            )}
      </p>
    );
  const ratio = used / limit;
  return (
    <Meter
      value={ratio}
      tone={ratio >= 1 ? "danger" : ratio > 0.9 ? "warning" : "primary"}
      label={kind === "hits" ? t("API hits today") : t("Downloads today")}
      valueLabel={t("{used} of {limit}", {
        used: formatNumber(used, 0),
        limit: formatNumber(limit, 0),
      })}
    />
  );
}
