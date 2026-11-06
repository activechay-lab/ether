import { Badge, Button, EmptyState } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { fixed, relativeTime } from "../../lib/format";
import { useAsync, useInterval } from "../../lib/hooks";
import type { ClusterStatus, ClusterConsole } from "../../lib/types";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { CLUSTER_POLL_MS, runtimeFresh } from "./Cluster";
type Event = ClusterConsole["events"][number];
const roleName = (role: string) =>
  role === "source"
    ? t("Source")
    : role === "transcode"
      ? t("Transcoding")
      : role === "delivery"
        ? t("Delivery")
        : t("Server");

export function ClusterRecovery({ status }: { status: ClusterStatus }) {
  const [params] = useSearchParams();
  const session = params.get("session");
  const state = useAsync((signal) => api.clusterConsole(signal), []);
  const [detail, setDetail] = useState<Event | null>(null);
  useInterval(() => void state.reload(), CLUSTER_POLL_MS);
  if (state.error) return <ErrorPanel error={state.error} onRetry={() => void state.reload()} />;
  if (!state.data) return <LoadingRows rows={3} />;
  const data = state.data;
  const name = (id?: string) => {
    if (!id) return t("Not reported");
    const node = status.nodes?.find((n) => n.id === id);
    return node ? node.name || t("Unnamed node") : t("Previous node");
  };
  const fresh = runtimeFresh(data.sampledAt);
  return (
    <section className="min-h-0 flex-1 space-y-3 overflow-auto" aria-label={t("Recovery history")}>
      {!fresh && <Badge variant="warning">{t("Stale status")}</Badge>}
      {data.events.filter((e) => !session || e.sessionId === session).length === 0 ? (
        <EmptyState title={t("No recovery events reported")} />
      ) : (
        data.events
          .filter((e) => !session || e.sessionId === session)
          .map((e, i) => {
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
                      <p className="min-w-0 break-words font-semibold">
                        {assignment.playback.title}
                      </p>
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
      )}
      {detail && (
        <Drawer
          open
          onClose={() => setDetail(null)}
          title={t("Playback details")}
          description={
            data.assignments.find((a) => a.sessionId === detail.sessionId)?.playback?.title
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
              [
                t("Nodes"),
                [detail.failedNodeId, detail.replacementNodeId].filter(Boolean).join(" · "),
              ],
              ...[
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
              ],
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
