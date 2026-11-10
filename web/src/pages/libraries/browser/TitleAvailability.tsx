import { Badge, Button, Spinner, cn } from "@braedonsaunders/appkit-ui";
import {
  ArrowUpRight,
  CircleCheck,
  CircleDashed,
  Clock,
  RefreshCw,
  ShieldAlert,
} from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { ErrorPanel } from "../../../components/feedback";
import { t, tn } from "../../../i18n";
import type { TitleDetail } from "../../../lib/contract";
import { formatBytes } from "../../../lib/format";
import { useAsync } from "../../../lib/hooks";
import {
  qualityLabel,
  rangeLabel,
  releaseConsole,
  type ConsoleRelease,
  type ConsoleSearch,
} from "../../../lib/releases";
import { localToday } from "../../calendar/model";
import { searchLink } from "../../releases/Pinned";
import { useReleaseEvidence } from "./TitleInspector";
import { airLabel, episodeState } from "./TitleEpisodes";
import { shortCode, type ScheduleEpisode, type ShowSchedule } from "./titleSchedule";

/**
 * What Ether would play right now: the release it picks first, the
 * fallbacks, and plain reasons when nothing qualifies. Released and
 * playable are different things, so unreleased titles never search.
 */
export function TitleAvailability({
  title,
  schedule,
  episodeKey,
  onEpisodeKey,
}: {
  title: TitleDetail;
  schedule: ShowSchedule | null;
  episodeKey: string | null;
  onEpisodeKey: (key: string) => void;
}) {
  const today = localToday();
  const isShow = title.type === "show";
  // Episodes Ether can search: aired ones in the schedule window, newest first.
  const aired = isShow
    ? (schedule?.episodes ?? [])
        .filter((e) => e.airDate && e.airDate <= today)
        .reverse()
        .concat(
          schedule?.last && !(schedule.episodes ?? []).some((e) => e.key === schedule.last!.key)
            ? [schedule.last]
            : [],
        )
    : [];
  const picked: ScheduleEpisode | undefined = isShow
    ? (aired.find((e) => e.key === episodeKey) ?? (episodeKey ? undefined : aired[0]))
    : undefined;
  const searchKey = isShow ? (episodeKey ?? picked?.key ?? null) : title.key;
  const searchName = isShow
    ? picked
      ? `${title.title} ${shortCode(picked)}`
      : title.title
    : title.title;
  const evidence = useReleaseEvidence(title.key, title.type);
  const movieUnreleased =
    !isShow && (!title.releaseDate || title.releaseDate > today) && !title.homeReleaseDate;

  return (
    <div className="catalog-tab">
      {!isShow ? <MovieVerdict code={evidence.data?.code} title={title} /> : null}
      {isShow ? (
        <div className="catalog-avail-pick">
          <label htmlFor="catalog-avail-episode">{t("Episode")}</label>
          {aired.length ? (
            <select
              id="catalog-avail-episode"
              value={searchKey ?? ""}
              onChange={(e) => onEpisodeKey(e.target.value)}
            >
              {episodeKey && !picked ? (
                <option value={episodeKey}>{t("Selected episode")}</option>
              ) : null}
              {aired.map((e) => (
                <option key={e.key} value={e.key}>
                  {shortCode(e)} · {e.title} · {airLabel(e.airDate, today)}
                </option>
              ))}
            </select>
          ) : (
            <p className="text-sm text-fg-muted">
              {episodeKey
                ? t("Checking the episode you picked.")
                : t("No recently aired episode to check. Pick one from Episodes.")}
            </p>
          )}
        </div>
      ) : null}
      {movieUnreleased ? (
        <Notice icon={<Clock size={16} />}>
          {t(
            "This movie is not out yet, so there is nothing to play. Ether starts looking once it is released.",
          )}
        </Notice>
      ) : searchKey ? (
        <Sources
          key={searchKey}
          searchKey={searchKey}
          name={searchName}
          episode={picked}
          today={today}
        />
      ) : null}
    </div>
  );
}

function Notice({
  icon,
  children,
  tone,
}: {
  icon: React.ReactNode;
  children: React.ReactNode;
  tone?: "good" | "warn";
}) {
  return (
    <div className={cn("catalog-notice", tone && `is-${tone}`)}>
      {icon}
      <p>{children}</p>
    </div>
  );
}

function MovieVerdict({ code, title }: { code?: string; title: TitleDetail }) {
  if (!code || code === "unknown" || code === "outside") return null;
  if (code === "playable" || code === "digital")
    return (
      <Notice tone="good" icon={<CircleCheck size={16} />}>
        {code === "playable"
          ? t("Viewers see this movie, and Ether has already confirmed a playable release.")
          : t("Viewers see this movie. Ether finds a release when someone presses play.")}
      </Notice>
    );
  if (code === "theatrical")
    return (
      <Notice icon={<Clock size={16} />}>
        {t(
          "Hidden from viewers while {title} is only in theatres. It appears once a digital or disc release exists.",
          {
            title: title.title,
          },
        )}
      </Notice>
    );
  if (code === "quality")
    return (
      <Notice tone="warn" icon={<ShieldAlert size={16} />}>
        {t("Hidden from viewers: releases exist, but none meets your quality profile yet.")}
      </Notice>
    );
  return (
    <Notice icon={<CircleDashed size={16} />}>
      {t(
        "Hidden from viewers until Ether finds a playable release. It keeps checking in the background.",
      )}
    </Notice>
  );
}

function Sources({
  searchKey,
  name,
  episode,
  today,
}: {
  searchKey: string;
  name: string;
  episode?: ScheduleEpisode;
  today: string;
}) {
  const [fresh, setFresh] = useState(0);
  const result = useAsync<ConsoleSearch>(
    (signal) => releaseConsole.search(searchKey, { fresh: fresh > 0 }, signal),
    [searchKey, fresh],
  );
  if (episode && episodeState(episode, today) === "upcoming")
    return <Notice icon={<Clock size={16} />}>{t("This episode has not aired yet.")}</Notice>;
  if (result.error) return <ErrorPanel error={result.error} onRetry={() => void result.reload()} />;
  if (!result.data)
    return (
      <div className="catalog-searching">
        <Spinner size={18} label={t("Searching your indexers")} />
        <p>{t("Searching your indexers and ranking releases the way playback does…")}</p>
      </div>
    );
  const rows = result.data.releases ?? [];
  const ranked = rows.filter((r) => !r.rejected).sort((a, b) => (a.rank ?? 99) - (b.rank ?? 99));
  const rejected = rows.length - ranked.length;
  const winner = ranked.find((r) => r.winner) ?? ranked[0];

  return (
    <section className="catalog-sources">
      <header>
        <div>
          <h3>{t("What Ether would play")}</h3>
          <p>
            {rows.length
              ? [
                  tn(rows.length, "{count} release found", "{count} releases found"),
                  rejected
                    ? tn(
                        rejected,
                        "{count} ruled out by your rules",
                        "{count} ruled out by your rules",
                      )
                    : null,
                  result.data.cached ? t("from the last search") : null,
                ]
                  .filter(Boolean)
                  .join(" · ")
              : t("No releases found on your indexers.")}
          </p>
        </div>
        <Button size="sm" variant="ghost" onClick={() => setFresh((n) => n + 1)}>
          <RefreshCw size={14} aria-hidden />
          {t("Search again")}
        </Button>
      </header>
      {winner ? <ReleaseCard r={winner} primary /> : null}
      {ranked
        .filter((r) => r !== winner)
        .slice(0, 4)
        .map((r) => (
          <ReleaseCard key={r.release.id} r={r} />
        ))}
      {!rows.length ? (
        <Notice icon={<CircleDashed size={16} />}>
          {t(
            "Nothing turned up for this one. If you expected releases, check that your indexers and download sources are set up and reachable.",
          )}{" "}
          <Link to="/sources" className="catalog-link">
            {t("Sources")}
            <ArrowUpRight size={13} aria-hidden />
          </Link>
        </Notice>
      ) : null}
      {!ranked.length && rows.length ? (
        <Notice tone="warn" icon={<ShieldAlert size={16} />}>
          {t("Every release was ruled out. The most common reason: {reason}", {
            reason: commonReason(rows) || t("not stated"),
          })}
        </Notice>
      ) : null}
      <Link to={searchLink(searchKey, name)} className="catalog-link">
        {t("Pin, block or health-check releases")}
        <ArrowUpRight size={13} aria-hidden />
      </Link>
    </section>
  );
}

function commonReason(rows: ConsoleRelease[]): string {
  const n = new Map<string, number>();
  for (const r of rows) if (r.reason) n.set(r.reason, (n.get(r.reason) ?? 0) + 1);
  return [...n.entries()].sort((a, b) => b[1] - a[1])[0]?.[0] ?? "";
}

function ReleaseCard({ r, primary }: { r: ConsoleRelease; primary?: boolean }) {
  const range = rangeLabel(r.release.info);
  return (
    <article className={cn("catalog-release", primary && "is-primary")}>
      <div className="flex items-center gap-2">
        <Badge variant={primary ? "success" : "secondary"}>
          {primary ? t("Plays first") : t("Fallback #{rank}", { rank: r.rank ?? "?" })}
        </Badge>
        {r.pinned ? <Badge variant="outline">{t("Pinned")}</Badge> : null}
        {r.health?.healthy ? <Badge variant="outline">{t("Verified")}</Badge> : null}
      </div>
      <p className="catalog-release-quality">
        {qualityLabel(r.release.info)}
        {range ? <span> · {range}</span> : null}
      </p>
      <p className="catalog-release-name" title={r.release.name}>
        {r.release.name}
      </p>
      <p className="catalog-release-meta">
        {[formatBytes(r.release.size), r.release.indexer, r.release.info.group]
          .filter(Boolean)
          .join(" · ")}
      </p>
    </article>
  );
}
