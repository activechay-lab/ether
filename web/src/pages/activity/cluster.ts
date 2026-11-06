import { t } from "../../i18n";
import { normalizeSessions, type NowPlaying } from "../../lib/health";
import type { ClusterConsole, ClusterStatus } from "../../lib/types";

/** Join durable assignments to the existing cluster-wide observations by exact ID. */
export function clusterActivity(
  streams: NowPlaying[],
  status: ClusterStatus,
  console: ClusterConsole,
): NowPlaying[] {
  const name = (id?: string) =>
    !id ? t("Not reported") : status.nodes?.find((n) => n.id === id)?.name || t("Previous node");
  const result = streams.map((s) => ({ ...s }));
  for (const a of console.assignments) {
    let stream = result.find((s) => s.id === a.sessionId || (a.playId && s.id === a.playId));
    if (!stream) {
      const p = a.playback;
      stream = normalizeSessions([
        {
          ID: a.sessionId,
          Key: p?.titleKey,
          TitleName: p?.title,
          Username: p?.viewer,
          Client: { deviceName: p?.device, product: p?.app, protocol: p?.protocol },
          Decision: p?.decision,
          decisionVideoCopy: p?.videoCopy,
          decisionAudioCopy: p?.audioCopy,
          outputVideo: p?.video,
          bitrateKbps: p?.bitrateKbps,
        },
      ])[0]!;
      result.push(stream);
    }
    const events = console.events.filter((e) => e.sessionId === a.sessionId);
    const encoder = !a.encoder
      ? t("Not reported")
      : a.encoder === "cpu"
        ? t("CPU")
        : t("GPU · {encoder}", { encoder: a.encoder });
    stream.cluster = {
      source: name(a.sourceNodeId),
      encoder: t("{node} · {encoder}", { node: name(a.encoderNodeId), encoder }),
      delivery: a.deliveryNodeId ? name(a.deliveryNodeId) : t("Any delivery node"),
      recovery: events.length
        ? `/cluster?tab=recovery&session=${encodeURIComponent(a.sessionId)}`
        : undefined,
      recovering: events.some((e) => e.status === "recovering"),
    };
  }
  for (const s of result) {
    if (!s.cluster)
      s.cluster = {
        source: name(s.node || status.self.id),
        encoder:
          s.decision === "directplay"
            ? t("Not needed")
            : s.processor === "gpu"
              ? t("GPU · {encoder}", { encoder: name(s.node || status.self.id) })
              : t("{node} · {encoder}", {
                  node: name(s.node || status.self.id),
                  encoder: s.processor === "cpu" ? t("CPU") : t("Not reported"),
                }),
        delivery: t("Any delivery node"),
        recovering: false,
      };
  }
  return result;
}
