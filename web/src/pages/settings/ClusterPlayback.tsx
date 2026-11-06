import { Badge, EmptyState } from "@braedonsaunders/appkit-ui";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { relativeTime } from "../../lib/format";
import { useAsync, useInterval } from "../../lib/hooks";
import type { ClusterStatus } from "../../lib/types";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { CLUSTER_POLL_MS, runtimeFresh } from "./Cluster";

export function ClusterPlayback({
  status,
  recovery,
}: {
  status: ClusterStatus;
  recovery: boolean;
}) {
  const state = useAsync((signal) => api.clusterConsole(signal), []);
  useInterval(() => void state.reload(), CLUSTER_POLL_MS);
  if (state.error) return <ErrorPanel error={state.error} onRetry={() => void state.reload()} />;
  if (!state.data) return <LoadingRows rows={3} />;
  const data = state.data;
  const name = (id?: string) =>
    id ? status.nodes?.find((n) => n.id === id)?.name || id : t("Unknown");
  const fresh = runtimeFresh(data.sampledAt);
  return (
    <section
      className="min-h-0 flex-1 space-y-4 overflow-auto"
      aria-label={recovery ? t("Recovery history") : t("Playback")}
    >
      <p className="text-sm text-fg-muted">
        {recovery
          ? t("Recovery outcomes and client impact are shown only when measured.")
          : t("Current assignments. Delivery can use any ready delivery node.")}
      </p>
      {!fresh && <Badge variant="warning">{t("Stale status")}</Badge>}
      {recovery ? (
        data.events.length === 0 ? (
          <EmptyState title={t("No recovery events reported")} />
        ) : (
          data.events.map((e, i) => (
            <article
              key={`${e.sessionId}-${e.generation}-${i}`}
              className="space-y-3 rounded-xl border border-border bg-surface p-4"
            >
              <div className="flex flex-wrap justify-between gap-2">
                <strong className="break-all text-sm">{e.sessionId}</strong>
                <Badge
                  variant={
                    e.status === "failed"
                      ? "destructive"
                      : e.status === "recovering"
                        ? "warning"
                        : "outline"
                  }
                >
                  {e.status === "recovered"
                    ? t("Recovered")
                    : e.status === "recovering"
                      ? t("Recovering")
                      : e.status === "failed"
                        ? t("Failed")
                        : t("Unknown")}
                </Badge>
              </div>
              <p className="text-sm">
                {name(e.failedNodeId)} → {name(e.replacementNodeId)}
              </p>
              <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
                {[
                  [t("Generation"), e.generation],
                  [
                    t("Recovery time"),
                    e.completedAt
                      ? t("{value} ms", {
                          value: Math.max(0, Date.parse(e.completedAt) - Date.parse(e.startedAt)),
                        })
                      : t("Unknown"),
                  ],
                  [
                    t("Interruption"),
                    e.interruptionMs === undefined
                      ? t("Unknown")
                      : t("{value} ms", { value: e.interruptionMs }),
                  ],
                  [t("Playback stalls"), e.clientStalls ?? t("Unknown")],
                ].map(([label, value]) => (
                  <div key={label}>
                    <dt className="text-fg-muted">{label}</dt>
                    <dd>{value}</dd>
                  </div>
                ))}
              </dl>
              <p className="text-xs text-fg-muted">{relativeTime(e.startedAt)}</p>
            </article>
          ))
        )
      ) : data.assignments.length === 0 ? (
        <EmptyState title={t("No active playback")} />
      ) : (
        data.assignments.map((a) => (
          <article
            key={a.sessionId}
            className="space-y-3 rounded-xl border border-border bg-surface p-4"
          >
            <div className="flex flex-wrap justify-between gap-2">
              <strong className="break-all text-sm">{a.sessionId}</strong>
              <Badge variant="outline">{t("Generation {value}", { value: a.generation })}</Badge>
            </div>
            <dl className="grid gap-3 sm:grid-cols-3">
              {[
                [t("Source"), a.sourceNodeId],
                [t("Transcoding"), a.encoderNodeId],
                [t("Delivery"), a.deliveryNodeId],
              ].map(([label, id]) => (
                <div key={label} className="rounded-lg bg-surface-hover p-3">
                  <dt className="text-xs text-fg-muted">{label}</dt>
                  <dd className="break-all text-sm font-medium">
                    {fresh ? name(id) : t("Unknown")}
                  </dd>
                </div>
              ))}
            </dl>
            <p className="text-xs text-fg-muted">
              {t("Last move")}: {a.movedAt ? relativeTime(a.movedAt) : t("Unknown")} · {t("Server")}
              : {name(a.controllerNodeId)}
            </p>
          </article>
        ))
      )}
      {data.truncated && !recovery && (
        <p className="text-sm text-fg-muted">{t("Showing the first 200 assignments.")}</p>
      )}
    </section>
  );
}
