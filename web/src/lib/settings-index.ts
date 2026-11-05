/**
 * Every individual owner setting ⌘K can jump to. Schema-driven forms are
 * listed from their field groups; hand-built sections are listed by the
 * stable row key they render. `more` names the "More options" disclosure
 * (its data-more key) that hides the row, so the jump can open it.
 */

import { t } from "../i18n";
import type { FieldDef, FieldGroup } from "../pages/settings/SchemaForm";
import * as S from "../pages/settings/schema";
import { UpcomingEpisodeSettings } from "../pages/catalog/HomeReleaseSettings";

export interface SettingHit {
  /** Stable row id, independent of display language. */
  key: string;
  title: string;
  /** Section or page the setting sits in, shown as the search subtitle. */
  where: string;
  href: string;
  /** Stable id of the "More options" disclosure hiding the row. */
  more?: string;
  keywords?: string;
}

function fieldHits(
  defs: FieldDef[],
  href: string,
  where: string,
  more: string | undefined,
): SettingHit[] {
  return defs.flatMap((f) => {
    if (f.kind === "hidden") return [];
    if (f.kind === "group") return fieldHits(f.fields, href, where, more);
    return [
      {
        key: f.key,
        title: f.label,
        where,
        href,
        more,
        keywords: typeof f.help === "string" ? f.help : undefined,
      },
    ];
  });
}

function groupHits(
  groups: readonly FieldGroup[],
  href: string,
  page: string,
  moreKey?: (g: FieldGroup) => string | undefined,
): SettingHit[] {
  return groups.flatMap((g) => {
    const where = g.title ? `${page} › ${g.title}` : page;
    return [
      ...fieldHits(g.fields, href, where, undefined),
      ...fieldHits(g.more ?? [], href, where, moreKey ? moreKey(g) : g.id),
    ];
  });
}

function rows(
  href: string,
  where: string,
  titles: [string, string][],
  more?: string,
): SettingHit[] {
  return titles.map(([key, title]) => ({ key, title, where, href, more }));
}

export function settingsIndex(): SettingHit[] {
  const general = t("General");
  const quality = t("Quality");
  const playback = t("Playback");
  const catalog = t("Catalog settings");
  const plex = "Plex";
  const away = t("Away from home");
  return [
    {
      key: "poster-source",
      title: t("Poster source"),
      where: t("Artwork"),
      href: "/metadata/artwork",
      keywords: "TMDB RPDB Custom PostersPlus XRDB BetterPosters",
    },
    ...["PostersPlus", "XRDB", "BetterPosters", "RPDB"].map((title) => ({
      key: title.toLowerCase(),
      title,
      where: t("Integrations"),
      href: "/services/overview",
      keywords: "poster API key",
    })),
    // General
    ...groupHits(S.SERVER, "/server", general),
    ...rows("/server", general, [
      [
        "send-indexers-debrid-and-usenet-through-a-proxy",
        t("Send indexers, debrid and Usenet through a proxy"),
      ],
      ["proxy-address", t("Proxy address")],
      ["how-apps-connect", t("How apps connect")],
      ["port", t("Port")],
      ["local-url", t("Local URL")],
      ["remote-url", t("Remote URL")],
    ]),
    ...rows(
      "/server",
      general,
      [
        ["username", t("Username")],
        ["password", t("Password")],
        ["names-that-stay-on-this-network", t("Names that stay on this network")],
      ],
      "how-ether-reaches-the-internet",
    ),
    // Apps
    ...groupHits(S.AWAY, "/apps/overview", t("Apps")).map((h) => ({
      ...h,
      where: `${t("Apps")} › ${away}`,
    })),
    ...groupHits(
      S.PLEX.filter((g) => g.id === "compatibility"),
      "/apps/plex/account",
      plex,
    ),
    ...rows("/apps/plex/account", plex, [
      ["use-plex-s-metadata-for-plex-apps", t("Use Plex's metadata for Plex apps")],
    ]),
    ...groupHits(
      S.PLEX.filter((g) => g.id !== "compatibility"),
      "/apps/plex/remote",
      plex,
    ),
    ...groupHits(S.DOWNLOADS, "/apps/plex/downloads", plex),
    // Catalog
    ...groupHits(S.CATALOG, "/catalog/settings", catalog),
    ...groupHits([UpcomingEpisodeSettings], "/catalog/settings", catalog),
    ...rows("/catalog/settings", catalog, [
      ["hide-obscure-titles", t("Hide obscure titles")],
      ["watchlist-monitoring", t("Watchlist monitoring")],
      ["plex-watchlists", t("Plex watchlists")],
      ["trakt-watchlists", t("Trakt watchlists")],
      ["tell-me-too", t("Tell me too")],
      ["check-every", t("Check every")],
    ]),
    ...rows("/catalog/settings", `${catalog} › ${t("New releases")}`, [
      ["when-they-can-play", t("When they can play")],
      ["on-digital-release", t("On digital release")],
      ["right-away", t("Right away")],
    ]),
    // Sources
    ...groupHits(S.DEBRID, "/sources/debrid", t("Debrid"), () => "debrid"),
    ...rows(
      "/sources/indexers",
      t("Indexers"),
      [["daily-budget-for-new-release-checks", t("Daily budget for new-release checks")]],
      "daily-budgets",
    ),
    ...rows(
      "/sources/indexers",
      t("Site definitions"),
      [
        ["definitions-source", t("Definitions source")],
        ["flaresolverr", "FlareSolverr"],
      ],
      "site-definitions",
    ),
    // Quality
    ...rows("/quality/profiles", `${quality} › ${away}`, [
      ["away-profile", t("Away profile")],
      ["bitrate-limit", t("Bitrate limit")],
    ]),
    ...rows("/quality/profiles", quality, [["source-order", t("Source order")]]),
    ...rows("/quality/check", quality, [["check-quality", t("Check quality")]]),
    ...groupHits(S.PLAY_VERSIONS, "/quality/profiles", quality),
    ...rows("/quality/searching", `${quality} › ${t("Start speed")}`, [
      ["start-before-the-health-check-finishes", t("Start before the health check finishes")],
    ]),
    ...rows(
      "/quality/searching",
      `${quality} › ${t("Start speed")}`,
      [["concurrent-checks", t("Concurrent checks")]],
      "start-speed",
    ),
    ...groupHits(S.RELEASE_SEARCH, "/quality/searching", quality),
    ...groupHits(S.RELEASE_BLOCKS, "/quality/blocklist", quality),
    // Playback
    ...rows("/playback/subtitles", `${playback} › ${t("Languages")}`, [
      ["audio-languages", t("Audio languages")],
      ["prefer-the-original-language", t("Prefer the original language")],
      ["subtitle-languages", t("Subtitle languages")],
      ["show-subtitles", t("Show subtitles")],
      ["subtitle-style", t("Subtitle style")],
    ]),
    ...rows(
      "/playback/subtitles",
      `${playback} › ${t("Subtitle appearance")}`,
      [
        ["size", t("Size")],
        ["text-colour", t("Text colour")],
        ["background", t("Background")],
        ["position", t("Position")],
      ],
      "subtitle-appearance",
    ),
    ...rows(
      "/playback/subtitles",
      `${playback} › ${t("Audio processing")}`,
      [
        ["normalize-loudness", t("Normalize loudness")],
        ["night-mode-compression", t("Night mode compression")],
        ["boost-centre-channel-dialogue", t("Boost centre-channel dialogue")],
      ],
      "audio-processing",
    ),
    ...groupHits(S.STREAM_LIMITS, "/playback/transcoding", playback),
    ...groupHits(S.TRANSCODE, "/playback/transcoding", playback),
    ...groupHits(S.EXTRAS, "/playback/prerolls", playback),
    ...rows("/playback/extras", `${playback} › ${t("Skip buttons")}`, [
      ["skip-intro", t("Skip intro")],
      ["skip-credits", t("Skip credits")],
    ]),
    ...groupHits(S.UP_NEXT, "/playback/extras", playback),
    // Home
    ...groupHits(S.PERSONAL_TOUCHES, "/home", t("Home")),
    // Notifications
    ...rows("/notifications/report", t("Weekly report"), [
      ["email-the-weekly-report", t("Email the weekly report")],
      ["day", t("Day")],
      ["time", t("Time")],
      ["report-viewer", t("Report viewer")],
    ]),
    // Maintenance
    ...groupHits(S.UPDATES, "/maintenance/updates", t("Updates")),
    ...rows("/maintenance/health", t("Health"), [
      ["send-uptime-heartbeat", t("Send uptime heartbeat")],
      ["secret-heartbeat-url", t("Secret heartbeat URL")],
    ]),
    ...groupHits(S.SPIKE, "/maintenance/health", t("Health")),
    ...groupHits(S.LOG_LEVEL, "/maintenance/logs", t("Logs")),
  ];
}
