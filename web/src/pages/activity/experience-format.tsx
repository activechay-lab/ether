import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { getLocale, t, tn, tx } from "../../i18n";
import { formatBitrate, fixed, formatPercent } from "../../lib/format";
import type { ExperienceCause, ExperienceMetrics, ReasonKind } from "./types";

/** Targets the owner approved: start ≤5 s good, ≤10 s fair; buffering <1/h good. */
export const START_GOOD_MS = 5_000;
const START_FAIR_MS = 10_000;
export const BUFFER_GOOD_PER_HOUR = 1;
const BUFFER_FAIR_PER_HOUR = 3;

export type Tone = "success" | "warning" | "danger";

export const DASH = "—";

/** "2.4 s" in the active language; "—" for an unknown value. */
export function formatSeconds(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms)) return DASH;
  const s = ms / 1000;
  return new Intl.NumberFormat(getLocale(), {
    style: "unit",
    unit: "second",
    unitDisplay: "narrow",
    maximumFractionDigits: s < 10 ? 1 : 0,
  }).format(s);
}

/** Buffering events per hour of playback, "—" when nothing was measured. */
export function formatPerHour(v: number | null | undefined): string {
  if (v == null || !Number.isFinite(v)) return DASH;
  return t("{rate} per hour", { rate: fixed(v, v < 10 ? 1 : 0) });
}

export function formatMbps(kbps: number | null | undefined): string {
  return formatBitrate((kbps ?? 0) * 1000);
}

/** A 0–100 value from the API as a localised percentage. */
export function pct(v: number | null | undefined): string {
  if (v == null || !Number.isFinite(v)) return DASH;
  return formatPercent(v / 100);
}

export function startTone(ms: number | null | undefined): Tone | undefined {
  if (ms == null) return undefined;
  return ms <= START_GOOD_MS ? "success" : ms <= START_FAIR_MS ? "warning" : "danger";
}

export function bufferTone(perHour: number | null | undefined): Tone | undefined {
  if (perHour == null) return undefined;
  return perHour < BUFFER_GOOD_PER_HOUR
    ? "success"
    : perHour < BUFFER_FAIR_PER_HOUR
      ? "warning"
      : "danger";
}

/** No failed starts is good; up to one in twenty is fair. */
export function failedTone(failed: number, plays: number): Tone | undefined {
  if (!plays) return undefined;
  if (!failed) return "success";
  return failed / plays <= 0.05 ? "warning" : "danger";
}

export const TONE_TEXT: Record<Tone, string> = {
  success: "text-success",
  warning: "text-warning",
  danger: "text-danger",
};

export const TONE_DOT: Record<Tone, string> = {
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
};

export function directShare(m: ExperienceMetrics): number | null {
  const total = m.decisions.directplay + m.decisions.copy + m.decisions.transcode;
  return total ? (m.decisions.directplay / total) * 100 : null;
}

/**
 * Worst-first ordering for the viewer table: failed starts weigh most, then
 * start time and buffering against their targets. Viewers without measured
 * plays sort last.
 */
export function badness(m: ExperienceMetrics): number {
  if (!m.plays) return -1;
  let score = m.measuredPlays ? (m.failedStarts / m.measuredPlays) * 10 : 0;
  if (m.startMedianMs != null) score += m.startMedianMs / START_GOOD_MS;
  if (m.bufferEventsPerHour != null) score += m.bufferEventsPerHour / BUFFER_GOOD_PER_HOUR;
  return score;
}

/** Plain words for why a play was converted, e.g. "the audio format (DTS)". */
export function reasonPhrase(kind: ReasonKind | "", detail: string): string {
  const d = detail.trim().toUpperCase();
  switch (kind) {
    case "audio_codec":
      return d ? t("the audio format ({format})", { format: d }) : t("the audio format");
    case "audio_limit":
      return t("the app's audio channel or bitrate limits");
    case "audio_processing":
      return t("audio processing such as night mode or dialogue boost");
    case "video_codec":
      return d ? t("the video format ({format})", { format: d }) : t("the video format");
    case "video_profile":
      return t("the video profile or level");
    case "resolution":
      return t("a resolution limit");
    case "bitrate":
      return t("a bitrate limit");
    case "subtitles":
      return d
        ? t("subtitles that must be burned in ({format})", { format: d })
        : t("subtitles that must be burned in");
    case "container":
      return d ? t("the file type ({format})", { format: d }) : t("the file type");
    case "hdr":
      return t("missing HDR or Dolby Vision support");
    case "client_setting":
      return t("the app's own quality settings");
    case "other":
      return t("other reasons");
    default:
      return "";
  }
}

/** "Direct play", "Direct stream" or "Transcode"; "" when unknown. */
export function decisionLabel(decision: string): string {
  switch (decision) {
    case "directplay":
      return t("Direct play");
    case "copy":
      return t("Direct stream");
    case "transcode":
      return t("Transcode");
    default:
      return "";
  }
}

/** The failed-start reason as recorded, or plain words when the viewer left. */
export function failedReason(reason: string): string {
  return reason || t("Left before the picture appeared");
}

/** Viewing time in hours. */
export function formatHours(ms: number): string {
  return t("{hours} h", { hours: fixed(ms / 3600000, 1) });
}

/** One plain-language sentence (with any useful links) for the likely cause. */
export function causeSentence(cause: ExperienceCause, viewerId: number): ReactNode {
  const link = (to: string, label: string) => (
    <Link to={to} className="font-medium text-primary hover:underline">
      {label}
    </Link>
  );
  switch (cause.kind) {
    case "none":
      return t("Nothing stands out: starts are quick and playback is smooth.");
    case "not_enough_data":
      return t("There aren't enough measured plays yet to point at a cause.");
    case "failed_starts":
      return cause.reason
        ? tn(
            cause.count,
            "{count} play never reached a picture. The most common reason: {reason}",
            "{count} plays never reached a picture. The most common reason: {reason}",
            { reason: cause.reason },
          )
        : tn(
            cause.count,
            "{count} play never reached a picture; the viewer left while it was starting.",
            "{count} plays never reached a picture; the viewer left while they were starting.",
          );
    case "away_bitrate":
      return tx(
        "Most viewing is away from home ({share}) at a high bitrate ({rate}). Set an {away} or a Mbps cap in their {limits}.",
        {
          share: pct(cause.remotePct),
          rate: formatMbps(cause.sourceKbps),
          away: link("/quality/profiles", t("Away profile")),
          limits: link(`/viewers/${viewerId}/limits`, t("limits")),
        },
      );
    case "transcode_device":
      return t("{share} of plays on {device} are converted because of {reason}.", {
        share: pct(cause.share),
        device: cause.device || t("one device"),
        reason: reasonPhrase(cause.reasonKind, cause.reasonDetail),
      });
    case "source_switches":
      return tx(
        "Playback switched sources {switches} times in {plays} plays, so the sources themselves are unreliable. Check {sources}.",
        {
          switches: String(cause.switches),
          plays: String(cause.plays),
          sources: link("/sources", t("Sources")),
        },
      );
    case "slow_starts":
      return t(
        "Starts are slow (median {time}) without one clear cause. Faster sources or a warm cache help most.",
        { time: formatSeconds(cause.medianMs) },
      );
    case "buffering":
      return tx(
        "Playback stalls {rate} with no other clear cause. Their connection may be too slow; try a Mbps cap in their {limits}.",
        {
          rate: formatPerHour(cause.perHour),
          limits: link(`/viewers/${viewerId}/limits`, t("limits")),
        },
      );
  }
}
