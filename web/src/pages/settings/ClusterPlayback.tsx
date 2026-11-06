import { Badge, Button, EmptyState } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { fixed, relativeTime } from "../../lib/format";
import { useAsync, useInterval } from "../../lib/hooks";
import type { ClusterStatus, ClusterConsole } from "../../lib/types";
import { useTitle } from "../../lib/titles";
import { Artwork } from "../../components/Artwork";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { CLUSTER_POLL_MS, runtimeFresh } from "./Cluster";
type Assignment = ClusterConsole["assignments"][number];
type Event = ClusterConsole["events"][number];
const roleName = (role: string) =>
  role === "source"
    ? t("Source")
    : role === "transcode"
      ? t("Transcoding")
      : role === "delivery"
        ? t("Delivery")
        : t("Server");

export function ClusterPlayback({
  status,
  recovery,
}: {
  status: ClusterStatus;
  recovery: boolean;
}) {
  const state = useAsync((signal) => api.clusterConsole(signal), []);
  const [detail, setDetail] = useState<Assignment | Event | null>(null);
  useInterval(() => void state.reload(), CLUSTER_POLL_MS);
  if (state.error) return <ErrorPanel error={state.error} onRetry={() => void state.reload()} />;
  if (!state.data) return <LoadingRows rows={3} />;
  const data = state.data;
  const name = (id?: string) =>
    id ? status.nodes?.find((n) => n.id === id)?.name || id : t("Not reported");
  const fresh = runtimeFresh(data.sampledAt);
  return (
    <section
      className="min-h-0 flex-1 space-y-3 overflow-auto"
      aria-label={recovery ? t("Recovery history") : t("Playback")}
    >
      {!fresh && <Badge variant="warning">{t("Stale status")}</Badge>}
      {recovery ? (
        data.events.length === 0 ? (
          <EmptyState title={t("No recovery events reported")} />
        ) : (
          data.events.map((e, i) => {
            const elapsed = e.completedAt
              ? Date.parse(e.completedAt) - Date.parse(e.startedAt)
              : undefined;
            const assignment = data.assignments.find((a) => a.sessionId === e.sessionId);
            return (
              <article
                key={`${e.sessionId}-${e.generation}-${i}`}
                className="rounded-xl border border-border bg-surface p-4"
              >
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0 space-y-2">
                    {assignment?.playback?.title && (
                      <p className="font-semibold">{assignment.playback.title}</p>
                    )}
                    <p className="text-sm font-medium">{recoverySentence(e, name)}</p>
                    {e.clientStalls === 0 ? (
                      <p className="text-sm text-fg-muted">{t("Playback continued")}</p>
                    ) : e.clientStalls !== undefined ? (
                      <p className="text-sm text-fg-muted">
                        {t("Playback stalls")}: {e.clientStalls}
                      </p>
                    ) : (
                      <p className="text-xs text-fg-muted">{t("Client impact not measured")}</p>
                    )}
                    <p className="text-xs text-fg-muted">
                      {elapsed !== undefined && Number.isFinite(elapsed) && elapsed >= 0 ? (
                        <>
                          {e.status === "recovered"
                            ? t("Recovered in {value} s", { value: fixed(elapsed / 1000, 1) })
                            : t("Recovery time") + ": " + t("{value} ms", { value: elapsed })}{" "}
                          ·{" "}
                        </>
                      ) : null}
                      {relativeTime(e.completedAt ?? e.startedAt)}
                    </p>
                  </div>
                  <Button size="sm" variant="ghost" onClick={() => setDetail(e)}>
                    {t("Details")}
                  </Button>
                </div>
              </article>
            );
          })
        )
      ) : data.assignments.length === 0 ? (
        <EmptyState title={t("No active playback")} />
      ) : (
        data.assignments.map((a) => (
          <PlaybackCard
            key={a.sessionId}
            assignment={a}
            name={name}
            fresh={fresh}
            onDetails={() => setDetail(a)}
          />
        ))
      )}
      {data.truncated && !recovery && (
        <p className="text-sm text-fg-muted">{t("Showing the first 200 assignments.")}</p>
      )}
      {detail && (
        <Drawer
          open
          onClose={() => setDetail(null)}
          title={t("Playback details")}
          description={
            "encoderNodeId" in detail
              ? detail.playback?.title
              : data.assignments.find((a) => a.sessionId === detail.sessionId)?.playback?.title
          }
          footer={
            <Button variant="outline" onClick={() => setDetail(null)}>
              {t("Done")}
            </Button>
          }
        >
          <dl className="space-y-4 text-sm">
            {[
              [t("Session"), detail.sessionId],
              [t("Generation"), detail.generation],
              ...("encoderNodeId" in detail
                ? [
                    [t("Source"), name(detail.sourceNodeId)],
                    [t("Transcoding"), name(detail.encoderNodeId)],
                    [
                      t("Delivery"),
                      detail.deliveryNodeId ? name(detail.deliveryNodeId) : t("Any delivery node"),
                    ],
                    [t("Server"), name(detail.controllerNodeId)],
                    ...(detail.movedAt ? [[t("Last move"), relativeTime(detail.movedAt)]] : []),
                  ]
                : [
                    [
                      t("Status"),
                      detail.status === "recovering"
                        ? t("Recovering")
                        : detail.status === "failed"
                          ? t("Failed")
                          : t("Recovered"),
                    ],
                    ...(detail.interruptionMs !== undefined
                      ? [
                          [
                            t("Server") + " · " + t("Interruption"),
                            t("{value} ms", { value: detail.interruptionMs }),
                          ],
                        ]
                      : []),
                  ]),
            ].map(([label, value]) => (
              <div key={label}>
                <dt className="text-fg-muted">{label}</dt>
                <dd className="break-all">{value}</dd>
              </div>
            ))}
          </dl>
        </Drawer>
      )}
    </section>
  );
}
function PlaybackCard({
  assignment: a,
  name,
  fresh,
  onDetails,
}: {
  assignment: Assignment;
  name: (id?: string) => string;
  fresh: boolean;
  onDetails: () => void;
}) {
  const title = useTitle(a.playback?.titleKey);
  const p = a.playback;
  const label = title?.title || p?.title || t("Playback");
  return (
    <article className="rounded-xl border border-border bg-surface p-4">
      <div className="flex gap-3 sm:gap-4">
        <Artwork
          src={p?.poster || title?.poster}
          alt={label}
          title={label}
          className="aspect-[2/3] w-16 shrink-0 self-start overflow-hidden rounded-lg sm:w-20"
        />
        <div className="min-w-0 flex-1 space-y-2">
          <div className="flex items-start justify-between gap-2">
            <h2 className="font-semibold">{label}</h2>
            <Button size="sm" variant="ghost" onClick={onDetails}>
              {t("Details")}
            </Button>
          </div>
          {p && (
            <p className="text-sm text-fg-muted">
              {[p.viewer, p.device, p.app].filter(Boolean).join(" · ")}
            </p>
          )}
          {p && (p.video || p.bitrateKbps) && (
            <p className="text-sm">
              {[
                p.video,
                p.bitrateKbps ? t("{rate} Mbps", { rate: fixed(p.bitrateKbps / 1000, 1) }) : null,
              ]
                .filter(Boolean)
                .join(" · ")}
            </p>
          )}
          <dl className="grid gap-2 pt-2 text-sm sm:grid-cols-3">
            {[
              [t("Source"), fresh ? name(a.sourceNodeId) : t("Stale status")],
              [t("Transcoding"), fresh ? name(a.encoderNodeId) : t("Stale status")],
              [t("Delivery"), a.deliveryNodeId ? name(a.deliveryNodeId) : t("Any delivery node")],
            ].map(([label, value]) => (
              <div key={label}>
                <dt className="text-xs text-fg-muted">{label}</dt>
                <dd className="font-medium">{value}</dd>
              </div>
            ))}
          </dl>
        </div>
      </div>
    </article>
  );
}

function recoverySentence(e: Event, name: (id?: string) => string): string {
  const values = {
    role: roleName(e.role),
    from: name(e.failedNodeId),
    to: name(e.replacementNodeId),
  };
  return e.status === "recovering"
    ? t("{role} is moving from {from} to {to}", values)
    : e.status === "failed"
      ? t("{role} could not move from {from} to {to}", values)
      : e.status === "recovered"
        ? t("{role} moved from {from} to {to}", values)
        : t("Recovery history");
}
