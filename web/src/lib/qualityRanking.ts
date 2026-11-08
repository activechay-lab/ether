// The Radarr/Sonarr-standard ranking vocabulary (docs/quality-ranking.md),
// mirrored from internal/core/formats.go: a profile's quality order and
// the formats it scores.
import { localized, t } from "../i18n";
import type { CustomFormatDefinition, QualityItem, QualityProfile, ReleaseRule } from "./types";

export const BUILTIN_PREFIX = "ether:";
export const RULE_PREFIX = "rule:";

export type BuiltinKind = "resolution" | "release" | "streaming";

/**
 * The one quality group of a profile converted from an order that ranked
 * something above resolution (config.ConvertedOrderGroup); shown translated.
 */
export const CONVERTED_ORDER_GROUP = "All resolutions (from your old order)";

export interface BuiltinFormat {
  id: string;
  kind: BuiltinKind;
  label: string;
  /** One short line of help. */
  help: string;
}

const HEIGHTS = [2160, 1080, 720, 576, 480, 360];
const SOURCES = ["remux", "bluray", "webdl", "webrip", "hdtv", "dvd"];
const CODECS: [string, string][] = [
  ["hevc", "HEVC"],
  ["av1", "AV1"],
  ["h264", "H.264"],
  ["vp9", "VP9"],
  ["vc1", "VC-1"],
  ["mpeg2", "MPEG-2"],
  ["xvid", "XviD"],
];
const AUDIO: [string, string][] = [
  ["truehd", "TrueHD"],
  ["dtshd", "DTS-HD"],
  ["eac3", "E-AC-3"],
  ["dts", "DTS"],
  ["ac3", "AC-3"],
  ["aac", "AAC"],
  ["flac", "FLAC"],
  ["opus", "Opus"],
  ["mp3", "MP3"],
];
const SOURCE_NAMES: Record<string, string> = {
  remux: "Remux",
  bluray: "Blu-ray",
  webdl: "WEB-DL",
  webrip: "WEBRip",
  hdtv: "HDTV",
  dvd: "DVD",
};

/** Every built-in format, in the order the console lists them. */
export const BUILTIN_FORMATS: BuiltinFormat[] = localized(() => [
  ...HEIGHTS.map((h) => ({
    id: `${BUILTIN_PREFIX}res-${h}`,
    kind: "resolution" as const,
    label: h === 2160 ? "4K" : `${h}p`,
    help: t("The release's resolution, one step lower when its bitrate is too low for it."),
  })),
  ...SOURCES.map((s) => ({
    id: `${BUILTIN_PREFIX}${s}`,
    kind: "release" as const,
    label: SOURCE_NAMES[s] ?? s,
    help: t("The release type."),
  })),
  {
    id: `${BUILTIN_PREFIX}dolby-vision`,
    kind: "release",
    label: "Dolby Vision",
    help: t("Dolby Vision the device and profile can show."),
  },
  {
    id: `${BUILTIN_PREFIX}hdr`,
    kind: "release",
    label: "HDR",
    help: t("HDR10, Dolby Vision or a 4K remux the device and profile can show."),
  },
  {
    id: `${BUILTIN_PREFIX}hdr10`,
    kind: "release",
    label: "HDR10",
    help: t("An HDR10 layer the device and profile can show."),
  },
  ...CODECS.map(([c, name]) => ({
    id: `${BUILTIN_PREFIX}codec-${c}`,
    kind: "release" as const,
    label: name,
    help: t("The video codec, when the device decodes it."),
  })),
  ...AUDIO.map(([a, name]) => ({
    id: `${BUILTIN_PREFIX}audio-${a}`,
    kind: "release" as const,
    label: name,
    help: t("The audio format."),
  })),
  { id: `${BUILTIN_PREFIX}atmos`, kind: "release", label: "Atmos", help: t("Dolby Atmos.") },
  {
    id: `${BUILTIN_PREFIX}repack-proper`,
    kind: "release",
    label: "Repack/Proper",
    help: t("A fixed re-release of the same quality."),
  },
  {
    id: `${BUILTIN_PREFIX}low-bitrate`,
    kind: "release",
    label: t("Low bitrate"),
    help: t("Too little bitrate for the resolution it claims."),
  },
  {
    id: `${BUILTIN_PREFIX}preferred-language`,
    kind: "release",
    label: t("Preferred language"),
    help: t("Audio in one of the profile's languages."),
  },
  {
    id: `${BUILTIN_PREFIX}preferred-language-region`,
    kind: "release",
    label: t("Preferred language, other region"),
    help: t("Another regional variant of one of the profile's languages."),
  },
  {
    id: `${BUILTIN_PREFIX}other-language`,
    kind: "release",
    label: t("Not a preferred language"),
    help: t("Audio only in languages the profile doesn't list."),
  },
  {
    id: `${BUILTIN_PREFIX}preferred-subtitles`,
    kind: "release",
    label: t("Preferred subtitles"),
    help: t("Subtitles in one of the profile's languages."),
  },
  {
    id: `${BUILTIN_PREFIX}viewer-language`,
    kind: "streaming",
    label: t("Viewer's language"),
    help: t("A foreign film or show released in the viewer's audio language or MULTi."),
  },
  {
    id: `${BUILTIN_PREFIX}series-year-differs`,
    kind: "streaming",
    label: t("Series year differs"),
    help: t("Its series year differs from the show's but names no other show."),
  },
  {
    id: `${BUILTIN_PREFIX}video-transcode`,
    kind: "streaming",
    label: t("Video transcode"),
    help: t("The device can't decode the video, so Ether converts it."),
  },
  {
    id: `${BUILTIN_PREFIX}audio-direct`,
    kind: "streaming",
    label: t("Audio plays directly"),
    help: t("The device plays the audio as it is."),
  },
  {
    id: `${BUILTIN_PREFIX}audio-transcode`,
    kind: "streaming",
    label: t("Audio transcode"),
    help: t("The device can't play the audio, so Ether converts it."),
  },
  {
    id: `${BUILTIN_PREFIX}atmos-passthrough`,
    kind: "streaming",
    label: t("Atmos passthrough"),
    help: t("Atmos in an audio format the device plays."),
  },
  {
    id: `${BUILTIN_PREFIX}tone-mapping`,
    kind: "streaming",
    label: t("Tone mapping"),
    help: t("HDR on a device or profile without HDR, converted to SDR."),
  },
  {
    id: `${BUILTIN_PREFIX}past-retention`,
    kind: "streaming",
    label: t("Past retention"),
    help: t("A Usenet post older than your provider keeps."),
  },
]);

export function builtinFormat(id: string): BuiltinFormat | undefined {
  return BUILTIN_FORMATS.find((f) => f.id === id);
}

export function isRuleFormat(id: string): boolean {
  return id.startsWith(RULE_PREFIX);
}

export function isPluginFormat(id: string): boolean {
  return id.startsWith("plugin:") && id.includes("/");
}

/** The key a custom format definition is scored by. */
export function definitionKey(f: CustomFormatDefinition): string {
  return f.trash_id || f.name || "";
}

/** A quality's display name: "WEB-DL 1080p", "Remux 4K", "Unknown 720p". */
export function qualityName(q: string): string {
  const [source = "", height = ""] = q.split("-");
  const res = height === "2160" ? "4K" : `${height}p`;
  const name =
    SOURCE_NAMES[source] ??
    (source === "unknown"
      ? t("Unknown")
      : source === "cam"
        ? "CAM"
        : source === "screener"
          ? t("Screener")
          : source);
  return `${name} ${res}`;
}

/** An item's display name: its own name, else its quality. */
export function itemName(item: QualityItem): string {
  if (item.name === CONVERTED_ORDER_GROUP) return t("All resolutions (from your old order)");
  return item.name || (item.qualities.length === 1 ? qualityName(item.qualities[0] ?? "") : "");
}

/** Every quality the console can list, best first. */
export function allQualities(allowCam: boolean): string[] {
  const sources = [...SOURCES, "unknown", ...(allowCam ? ["cam", "screener"] : [])];
  return HEIGHTS.flatMap((h) => sources.map((s) => `${s}-${h}`));
}

/**
 * One group per resolution from max down to min (0 for no limit), CAM and
 * screener copies included when allowCam is set (config.ResolutionGroups).
 */
export function resolutionGroups(
  maxHeight: number,
  minHeight: number,
  allowCam = false,
): QualityItem[] {
  const sources = [...SOURCES, "unknown", ...(allowCam ? ["cam", "screener"] : [])];
  return HEIGHTS.filter((h) => (maxHeight === 0 || h <= maxHeight) && h >= minHeight).map((h) => ({
    name: h === 2160 ? "2160p" : `${h}p`,
    qualities: sources.map((s) => `${s}-${h}`),
  }));
}

/**
 * A converted profile on the standard order: one group per resolution, so
 * resolution ranks first, and no resolution formats, which then only repeat
 * the order.
 */
export function withStandardOrder(p: QualityProfile): QualityProfile {
  const scores = { ...(p.customFormatScores ?? {}) };
  for (const id of Object.keys(scores))
    if (builtinFormat(id)?.kind === "resolution") delete scores[id];
  return {
    ...p,
    qualityOrder: resolutionGroups(p.maxHeight, p.minHeight, p.allowCam),
    customFormatScores: scores,
    convertedRanking: false,
  };
}

/** The smallest 1, 2 or 5 × 10ⁿ that is at least x. */
function roundUpNice(x: number): number {
  for (let p = 1; ; p *= 10) {
    for (const m of [1, 2, 5]) if (m * p >= x) return m * p;
  }
}

/**
 * The score a new Prefer rule starts with: more than all the profile's other
 * positive scores together, so it decides between releases of one quality,
 * as "Prefer" means.
 */
export function defaultPreferScore(scores: Record<string, number> | null | undefined): number {
  let sum = 0;
  for (const [id, v] of Object.entries(scores ?? {})) if (v > 0 && !isRuleFormat(id)) sum += v;
  return roundUpNice(sum + 1);
}

/**
 * The profile's scores with every Prefer rule scored (a new one starts at
 * defaultPreferScore) and no score left for a rule that is gone or no
 * longer a Prefer rule.
 */
export function withRuleScores(
  rules: ReleaseRule[],
  scores: Record<string, number> | null | undefined,
): Record<string, number> {
  const out: Record<string, number> = {};
  const prefer = new Set(rules.filter((r) => r.action === "prefer").map((r) => RULE_PREFIX + r.id));
  for (const [id, v] of Object.entries(scores ?? {})) {
    if (!isRuleFormat(id) || prefer.has(id)) out[id] = v;
  }
  for (const id of prefer) if (out[id] == null) out[id] = defaultPreferScore(out);
  return out;
}
