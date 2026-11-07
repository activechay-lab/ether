/**
 * "Who gets what" for the mock backend: the known apps and a small version of
 * the server's quality decision (rules, Away profile, device ceiling, bitrate
 * caps). Development and tests only; the real answer comes from the server's
 * playback code path.
 */
import type {
  EtherConfig,
  QualityAnswer,
  QualityClient,
  QualityLayer,
  QualityQuestion,
} from "../lib/types";

type Reply = { status: number; body: unknown };

const ok = (body: unknown, status = 200): Reply => ({ status, body });
const fail = (status: number, code: string, message: string): Reply => ({
  status,
  body: { error: { code, message } },
});

type Device = { maxHeight: number; hdr: boolean; dolbyVision: boolean };

const CLIENTS: (QualityClient & Device)[] = [
  {
    id: "plex-apple-tv",
    name: "Plex for Apple TV",
    product: "Plex for Apple TV",
    platform: "tvOS",
    device: "Apple TV",
    maxHeight: 2160,
    hdr: true,
    dolbyVision: true,
  },
  {
    id: "plex-ios",
    name: "Plex for iOS",
    product: "Plex for iOS",
    platform: "iOS",
    device: "iPhone",
    maxHeight: 2160,
    hdr: true,
    dolbyVision: true,
  },
  {
    id: "plex-android",
    name: "Plex for Android",
    product: "Plex for Android (Mobile)",
    platform: "Android",
    device: "Pixel 8",
    maxHeight: 1080,
    hdr: false,
    dolbyVision: false,
  },
  {
    id: "plex-roku",
    name: "Plex for Roku",
    product: "Plex for Roku",
    platform: "Roku",
    maxHeight: 2160,
    hdr: true,
    dolbyVision: false,
  },
  {
    id: "plex-web-chrome",
    name: "Plex Web (Chrome)",
    product: "Plex Web",
    platform: "Chrome",
    maxHeight: 2160,
    hdr: false,
    dolbyVision: false,
  },
];

const contains = (have: string, want?: string) =>
  !want || have.toLowerCase().includes(want.toLowerCase());

export function createQualityMock() {
  function explain(
    q: QualityQuestion,
    config: EtherConfig,
    policy: Record<string, unknown> | undefined,
  ): Reply {
    const client = CLIENTS.find((c) => c.id === q.clientId);
    if (!client) return fail(400, "unknown_client", "no such app");
    const quality = config.quality;
    const profiles = quality.profiles ?? [];
    const media = q.mediaType === "movie" || q.mediaType === "tv" ? (q.mediaType as string) : "";
    const applies = (scope: string | undefined) => !media || !scope || scope === media;
    // Names are unique per media: the title's media picks the profile.
    const pick = (name: string | undefined) =>
      name ? profiles.find((p) => p.name === name && applies(p.mediaType)) : undefined;
    const rules = quality.rules ?? [];
    const ruleIndex = rules.findIndex(
      (r) =>
        (r.protocol || r.product || r.platform || r.userId) &&
        (!r.protocol || r.protocol === "plex") &&
        contains(client.product, r.product) &&
        contains(client.platform, r.platform) &&
        (!r.userId || r.userId === q.userId) &&
        applies(r.mediaType) &&
        pick(r.profile) !== undefined,
    );
    const rule = ruleIndex >= 0 ? rules[ruleIndex] : undefined;
    let source: NonNullable<QualityLayer["source"]> = "first";
    let profile = profiles.find((p) => applies(p.mediaType));
    const scoped = media
      ? media === "movie"
        ? quality.defaultMovie
        : quality.defaultTv
      : undefined;
    const scopedRemote = media
      ? media === "movie"
        ? quality.remoteMovie
        : quality.remoteTv
      : undefined;
    if (rule) {
      const found = pick(rule.profile);
      if (found) {
        source = "rule";
        profile = found;
      }
    } else if (q.remote) {
      const found = pick(scopedRemote || quality.remote);
      if (found) {
        source = "remote";
        profile = found;
      }
    } else {
      const found = pick(scoped || quality.default);
      if (found) {
        source = "default";
        profile = found;
      }
    }
    const name = profile?.name ?? "default";
    const configured = profile?.maxHeight ?? 1080;
    const ceiling = rule?.maxHeight || client.maxHeight;
    const maxHeight = configured && (!ceiling || configured <= ceiling) ? configured : ceiling;

    const layers: QualityLayer[] = [
      {
        kind: "profile",
        reason: "",
        profile: name,
        source,
        rule: rule ? ruleIndex + 1 : undefined,
        ruleUserId: rule?.userId,
        beatsRemote: Boolean(rule && q.remote && quality.remote && quality.remote !== name),
        maxHeight: configured,
        hdr: Boolean(profile?.allowHdr),
        dolbyVision: Boolean(profile?.allowDolbyVision),
        decisive: Boolean(maxHeight && configured === maxHeight),
      },
      {
        kind: "device",
        reason: "",
        maxHeight: client.maxHeight,
        hdr: client.hdr,
        dolbyVision: client.dolbyVision,
        decisive: Boolean(maxHeight && configured !== maxHeight && !rule?.maxHeight),
      },
    ];
    if (rule?.maxHeight)
      layers.push({
        kind: "rule-height",
        reason: "",
        rule: ruleIndex + 1,
        ruleUserId: rule.userId,
        maxHeight: rule.maxHeight,
        decisive: configured !== maxHeight,
      });
    const remoteCap = q.remote ? config.transcode.remoteMaxBitrateKbps : 0;
    const viewerCap = Number(policy?.maxBitrateKbps) || 0;
    const bitrate = [remoteCap, viewerCap].filter((n) => n > 0).sort((a, b) => a - b)[0] ?? 0;
    if (q.remote)
      layers.push({
        kind: "remote-cap",
        reason: "",
        bitrateKbps: remoteCap || undefined,
        decisive: bitrate > 0 && bitrate === remoteCap,
      });
    if (viewerCap)
      layers.push({
        kind: "viewer-cap",
        reason: "",
        bitrateKbps: viewerCap,
        decisive: bitrate === viewerCap && bitrate !== remoteCap,
      });
    const blocked = q.remote && Boolean(policy?.remoteDisabled);
    if (blocked) layers.push({ kind: "remote-blocked", reason: "", decisive: true });
    const answer: QualityAnswer = {
      profile: name,
      ...(media ? { mediaType: media } : {}),
      maxHeight,
      hdr: Boolean(profile?.allowHdr) && client.hdr,
      dolbyVision: Boolean(profile?.allowDolbyVision) && client.dolbyVision,
      bitrateKbps: bitrate,
      blocked,
      layers,
    };
    return ok(answer);
  }

  function handle(
    method: string,
    seg: string[],
    body: unknown,
    config: EtherConfig,
    policy: (id: number) => Record<string, unknown> | undefined,
  ): Reply | null {
    if (seg[0] !== "quality") return null;
    const route = `${method} /${seg.join("/")}`;
    if (route === "GET /quality/clients")
      return ok({
        clients: CLIENTS.map(({ id, name, product, platform, device }) => ({
          id,
          name,
          product,
          platform,
          device,
        })),
      });
    if (route === "POST /quality/explain") {
      const q = (body ?? {}) as QualityQuestion;
      if (!q.userId) return fail(404, "unknown_viewer", "no such viewer");
      return explain(q, config, policy(q.userId));
    }
    return null;
  }

  return { handle };
}
