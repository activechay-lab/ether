import { t, tn } from "../../../i18n";
import type { HealthLevel } from "../../../lib/health";
import { formatNumber, relativeTime } from "../../../lib/format";
import { pluginHealthLevel, type PluginDetail } from "../types";

/**
 * One reading of a plugin's health for every place the drawer shows it:
 * the header word, the Responding tile and the problem card. They all come
 * from here, so they can never disagree. Plain words only; the raw error
 * lives in Activity.
 */
export interface HealthSummary {
  level: HealthLevel;
  /** One or two words for the header and the tile. */
  word: string;
  /** The tile's second line. */
  tile: string;
  /** Present only when the owner should look. */
  card?: { title: string; line: string; retry: boolean };
}

/**
 * The level and the word for a plugin's health, from its status and the
 * server's plain reason only, so a card in the Installed list and the
 * plugin's drawer always say the same thing.
 */
export function healthHeadline(p: {
  health: string;
  healthReason?: string;
  healthSidecar?: string;
  enabled: boolean;
}): {
  level: HealthLevel;
  word: string;
  slow: boolean;
} {
  const level = pluginHealthLevel(p.health, p.enabled);
  const slow = level === "warn" && /slow/i.test(p.healthReason ?? "");
  const waiting = level === "warn" && p.healthSidecar === "waiting";
  const word =
    level === "off"
      ? t("Off")
      : level === "unknown"
        ? t("Unknown")
        : level === "ok"
          ? t("Healthy")
          : waiting
            ? t("Waiting for its sidecar")
            : slow
              ? t("Slow")
              : level === "warn"
                ? t("Needs attention")
                : t("Not answering");
  return { level, word, slow };
}

export function healthSummary(detail: PluginDetail): HealthSummary {
  const h = detail.healthDetail;
  const im = detail.impact;
  const { level, word, slow } = healthHeadline({
    health: h.status,
    healthReason: h.reason,
    healthSidecar: h.sidecar,
    enabled: detail.enabled,
  });
  const calls = im.calls;
  const seconds = Math.round(im.p50Ms / 1000);
  const failures = t("{errors} of {calls} recent calls failed", {
    errors: formatNumber(im.errors, 0),
    calls: formatNumber(calls, 0),
  });
  if (level === "off") {
    return { level, word, tile: t("Turned off") };
  }
  if (level === "ok" || level === "unknown") {
    return {
      level,
      word,
      tile: calls ? tn(calls, "{count} recent call", "{count} recent calls") : t("Not used yet"),
    };
  }
  if (level === "warn" && h.sidecar === "waiting") {
    return {
      level,
      word,
      tile: t("Not connected"),
      card: {
        title: t("{name} needs its sidecar running", { name: detail.name }),
        line: t(
          "This plugin runs as its own program on another computer. Choose Connect a sidecar below to get a pairing code, then start the program with it. Until it connects, nothing the plugin provides works.",
        ),
        retry: true,
      },
    };
  }
  const skipped = h.rankingSkipped ?? 0;
  if (level === "warn" && skipped > 0) {
    return {
      level,
      word,
      tile: tn(skipped, "Ranking left out once", "Ranking left out {count} times"),
      card: {
        title: t("Releases are being ranked without {name}", { name: detail.name }),
        line: t(
          "Its ranking step ran past its time limit, so it was left out. It counts again as soon as it finishes in time.",
        ),
        retry: false,
      },
    };
  }
  if (slow) {
    return {
      level,
      word,
      tile:
        seconds >= 1 ? t("About {seconds} s per answer", { seconds }) : t("Some answers are slow"),
      card: {
        title:
          seconds >= 1
            ? t("Works, but slow: about {seconds} s per answer", { seconds })
            : t("Works, but some answers are slow"),
        line: t("Anything that uses it waits for it."),
        retry: false,
      },
    };
  }
  if (level === "warn") {
    return {
      level,
      word,
      tile: im.errors ? failures : t("Needs attention"),
      card: {
        title: t("{name} needs attention", { name: detail.name }),
        line: t("Activity shows what happened."),
        retry: true,
      },
    };
  }
  const lastOk = h.lastOk && Date.parse(h.lastOk) > Date.UTC(2000, 0, 1) ? h.lastOk : "";
  return {
    level,
    word,
    tile: im.errors ? failures : word,
    card: {
      title: t("{name} isn't answering", { name: detail.name }),
      line: lastOk
        ? t("It last worked {when}.", { when: relativeTime(lastOk) })
        : t("It hasn't answered yet."),
      retry: true,
    },
  };
}
