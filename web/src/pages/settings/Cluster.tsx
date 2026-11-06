import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { HealthPanel } from "../Home";
import type { HealthItem } from "../../lib/health";
import { SubtabNav } from "../../components/layout";
import { copyText } from "../../components/CopyButton";
import {
  Alert,
  AlertDescription,
  AlertTitle,
  Badge,
  Button,
  confirmDialog,
  EmptyState,
  Input,
  PagedTable,
  SettingsSection,
  Textarea,
  toast,
} from "@braedonsaunders/appkit-ui";
import { Copy, KeyRound, Plus } from "lucide-react";
import { useState } from "react";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Field, Segmented } from "../../components/fields";
import { ClusterPlayback } from "./ClusterPlayback";
import { ClusterSetup } from "./ClusterSetup";
import { Section } from "../../components/layout";
import { SettingsRow } from "../../components/SettingsRow";
import { pagedTableLabels } from "../../i18n/appkit-labels";
import { localized, t, tn } from "../../i18n";
import { api, errorMessage } from "../../lib/api";
import { formatDateTime, parseTime, relativeTime } from "../../lib/format";
import { useAsync, useInterval, type AsyncState } from "../../lib/hooks";
import type {
  ClusterNode,
  ClusterControl,
  ClusterSettings,
  ClusterStatus,
  CreatedJoinToken,
  JoinTokenInfo,
} from "../../lib/types";

const CLUSTER_DOCS =
  "https://github.com/braedonsaunders/ether/blob/main/docs/install.md#running-a-cluster";

/** How often the page refreshes membership and tokens while it is visible. */
export const CLUSTER_POLL_MS = 10_000;

/** A node is live within 20 s of its last heartbeat; older means the database is unreachable. */
const HEARTBEAT_STALE_MS = 20_000;
/** Clock differences beyond this confuse liveness and token expiry. */
const SKEW_WARN_NS = 2e9;

function isClustered(s: ClusterStatus | null | undefined): boolean {
  return s?.mode === "postgres";
}

/**
 * Whether this server is a cluster node, read once per page; false while
 * loading and when the server doesn't report it (standalone behaviour).
 */
export function useClustered(): boolean {
  const status = useAsync((signal) => api.clusterStatus(signal).catch(() => null), []);
  return isClustered(status.data);
}

function nodeName(n: ClusterNode): string {
  return n.name || n.id;
}

/** "videotoolbox", "Software" or "Unavailable" (no ffmpeg on the node). */
export function transcodeLabel(n: ClusterNode): string {
  if (!n.caps?.transcode) return t("Unavailable");
  const hw = n.caps.hwAccel;
  return hw && hw !== "none" ? hw : t("Software");
}

/** Role metadata describes configured work, independently of detected hardware. */
export function nodeRoles(n: ClusterNode): string[] {
  const roles = n.caps?.roles;
  const labels: Record<string, string> = {
    server: t("Server"),
    source: t("Source"),
    transcode: t("Transcoding"),
    delivery: t("Delivery"),
    background: t("Background"),
  };
  if (
    !roles?.length ||
    (roles.length === 5 && new Set(roles).size === 5 && roles.every((r) => r in labels))
  )
    return [t("Full server")];
  return roles.map((r) => labels[r] ?? t("Unknown"));
}

export function nodeStatusFresh(n: ClusterNode, now = Date.now()): boolean {
  const seen = parseTime(n.lastSeen);
  return n.live && !!seen && Math.abs(now - seen.getTime()) <= HEARTBEAT_STALE_MS;
}

/** Never attach a local observation to a peer or to a replaced process boot. */
export function nodeRuntime(s: ClusterStatus, n: ClusterNode) {
  const r = s.selfRuntime;
  return r &&
    !!r.nodeId &&
    !!r.boot &&
    r.nodeId === s.self.id &&
    r.boot === s.self.incarnation &&
    r.nodeId === n.id &&
    r.boot === n.incarnation
    ? r
    : undefined;
}

// Two polling intervals, independent of membership and retained metric clocks.
export function runtimeFresh(at: string, now = Date.now()): boolean {
  const sampled = parseTime(at)?.getTime();
  return sampled !== undefined && sampled <= now && now - sampled <= 2 * CLUSTER_POLL_MS;
}

function RuntimeObservation({
  status,
  node,
  cached,
}: {
  status: ClusterStatus;
  node: ClusterNode;
  cached: boolean;
}) {
  const [, tick] = useState(0);
  useInterval(() => tick((n) => n + 1), 1_000);
  const now = Date.now();
  const observed = node.caps?.diagnostics;
  const runtime =
    nodeRuntime(status, node) ??
    (observed ? { ...observed, runtimeSampledAt: observed.sampledAt } : undefined);
  const fresh = !!runtime && !cached && runtimeFresh(runtime.runtimeSampledAt, now);
  const value = (v: boolean | undefined) =>
    !fresh || typeof v !== "boolean" ? t("Unknown") : v ? t("Yes") : t("No");
  const sampled = runtime && parseTime(runtime.runtimeSampledAt);
  return (
    <dl className="min-w-0 max-w-64 space-y-1 whitespace-normal text-xs">
      <div>
        <dt className="text-fg-muted">{t("Serving existing work")}</dt>
        <dd>{value(runtime?.readyExisting)}</dd>
      </div>
      <div>
        <dt className="text-fg-muted">{t("Accepting new work")}</dt>
        <dd>{value(runtime?.acceptingNew)}</dd>
      </div>
      <div>
        <dt className="text-fg-muted">{t("Draining")}</dt>
        <dd>{value(runtime?.draining)}</dd>
      </div>
      <div>
        <dt className="text-fg-muted">{t("Runtime observed at")}</dt>
        <dd>
          {sampled ? (
            <time dateTime={runtime!.runtimeSampledAt} title={formatDateTime(sampled)}>
              {relativeTime(sampled, new Date(now))}
            </time>
          ) : (
            t("Unknown")
          )}
        </dd>
      </div>
      {runtime && !fresh ? <Badge variant="warning">{t("Stale status")}</Badge> : null}
    </dl>
  );
}

function translateClusterWarning(w: string): string {
  const m = /^nodes reach providers from different public addresses \(([^)]*)\)/.exec(w);
  if (m) {
    return t(
      "These computers reach indexers and debrid from different public addresses ({detail}). One debrid account can be limited when it is used from more than one address.",
      { detail: m[1] ?? "" },
    );
  }
  return w.charAt(0).toUpperCase() + w.slice(1);
}

/** Server warnings plus conditions this node's own health reports. */
export function clusterWarnings(s: ClusterStatus, now: Date = new Date()): string[] {
  // Server warnings are log-style sentences; capitalise them for the banner.
  const out = (s.warnings ?? []).map(translateClusterWarning);
  if (s.error)
    out.push(t("Couldn't read the cluster's nodes from the database: {error}", { error: s.error }));
  const h = s.health;
  if (h) {
    const beat = parseTime(h.lastHeartbeat);
    if (!beat || now.getTime() - beat.getTime() > HEARTBEAT_STALE_MS)
      out.push(
        beat
          ? t(
              "This node hasn't reached the database since {when}. Streams keep playing, but other nodes may treat it as offline.",
              { when: relativeTime(beat, now) },
            )
          : t("This node hasn't reached the database yet."),
      );
    else if (!h.busConnected)
      out.push(
        t(
          "This node's event connection to the database is down, so changes made on other nodes may reach it late.",
        ),
      );
    if (Math.abs(h.clockSkewNs ?? 0) > SKEW_WARN_NS)
      out.push(
        tn(
          Math.round(Math.abs(h.clockSkewNs) / 1e9),
          "This node's clock is {count} second off the database server's. Sync the clock (NTP) on every node.",
          "This node's clock is {count} seconds off the database server's. Sync the clock (NTP) on every node.",
        ),
      );
  }
  return out;
}

type TokenState = "used" | "expired" | "unused";

export function tokenState(token: JoinTokenInfo, now: Date = new Date()): TokenState {
  if (parseTime(token.usedAt)) return "used";
  const exp = parseTime(token.expiresAt);
  if (exp && exp.getTime() <= now.getTime()) return "expired";
  return "unused";
}

/** Live nodes first, the leader first among them, then by name. */
function sortNodes(nodes: ClusterNode[]): ClusterNode[] {
  return [...nodes].sort(
    (a, b) =>
      Number(b.live) - Number(a.live) ||
      Number(b.leader) - Number(a.leader) ||
      nodeName(a).localeCompare(nodeName(b)),
  );
}

function copyToken(text: string) {
  void copyText(text);
}

/**
 * Server › Cluster: on a standalone server, what clustering is and how to
 * start; on a cluster node, the nodes, their health and the join tokens.
 */
export function ClusterOverview() {
  const status = useAsync((signal) => api.clusterStatus(signal), []);
  const clustered = isClustered(status.data);
  const [tab, setTab] = useState("overview");
  const [, tick] = useState(0);
  useInterval(() => tick((n) => n + 1), 1000);
  const [addingNode, setAddingNode] = useState(false);
  const setup = useAsync((signal) => api.clusterSetupStatus(signal), []);
  const settings = useAsync(
    async (signal) => (clustered ? api.clusterSettings(signal) : null),
    [clustered],
  );
  useInterval(() => {
    void status.reload();
    if (clustered) void settings.reload();
  }, CLUSTER_POLL_MS);

  if (status.error && !status.data)
    return <ErrorPanel error={status.error} onRetry={() => void status.reload()} />;
  const s = status.data;
  if (!s) return <LoadingRows rows={3} />;
  if (!clustered)
    return (
      <div className="min-h-0 flex-1 space-y-5 overflow-auto p-4 sm:p-6">
        <h1 className="text-xl font-semibold">{t("Cluster")}</h1>
        <Standalone
          status={s}
          available={setup.data?.available ?? false}
          message={setup.data?.state === "failed" ? setup.data.message : undefined}
          onComplete={() => {
            void status.reload();
            void setup.reload();
          }}
        />
      </div>
    );

  const cached = !!status.error || !!s.error;
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-hidden p-4 sm:p-6">
      <header>
        <h1 className="text-xl font-semibold">{t("Cluster")}</h1>
        <p className="text-sm text-fg-muted">{t("Servers that share one database.")}</p>
      </header>
      <SubtabNav
        ariaLabel={t("Cluster")}
        active={tab}
        onSelect={setTab}
        tabs={[
          { key: "overview", label: t("Overview") },
          { key: "nodes", label: t("Nodes") },
          { key: "playback", label: t("Playback") },
          { key: "recovery", label: t("Recovery history") },
          { key: "tokens", label: t("Join tokens") },
        ]}
      />
      {tab === "overview" ? (
        <FleetOverview status={s} cached={cached} />
      ) : tab === "playback" || tab === "recovery" ? (
        <ClusterPlayback status={s} recovery={tab === "recovery"} />
      ) : (
        <div className="min-h-0 flex-1 space-y-4 overflow-auto">
          {tab === "nodes" ? (
            <Nodes
              status={s}
              cached={cached}
              onChanged={() => void status.reload()}
              onAdd={() => setAddingNode(true)}
            />
          ) : (
            <JoinTokens settings={settings} />
          )}
        </div>
      )}
      {addingNode ? (
        <JoinTokenDrawer
          onClose={() => setAddingNode(false)}
          onCreated={() => void settings.reload()}
        />
      ) : null}
    </div>
  );
}

/** Mock observations are opt-in at the mock-build boundary, never peer runtime evidence. */
export function clusterNodeHealth(
  s: ClusterStatus,
  n: ClusterNode,
  cached: boolean,
  mockHealth?: NonNullable<ClusterStatus["mockConsole"]>["health"],
) {
  if (cached) return "unknown";
  const observation = mockHealth?.[n.id];
  if (
    observation?.nodeId === n.id &&
    !!n.incarnation &&
    observation.boot === n.incarnation &&
    runtimeFresh(observation.sampledAt)
  )
    return observation.level;
  const diagnostic = n.caps?.diagnostics;
  if (diagnostic && nodeStatusFresh(n) && runtimeFresh(diagnostic.sampledAt))
    return diagnostic.readyExisting ? "ok" : "error";
  const runtime = nodeRuntime(s, n);
  return runtime && runtimeFresh(runtime.runtimeSampledAt)
    ? runtime.readyExisting
      ? "ok"
      : "error"
    : "unknown";
}

function FleetOverview({ status: s, cached }: { status: ClusterStatus; cached: boolean }) {
  const [panel, setPanel] = useState("nodes");
  const nodes = sortNodes(s.nodes ?? []);
  const fresh = !cached && runtimeFresh(s.health?.lastHeartbeat ?? "");
  const preview = import.meta.env.MODE === "mock" ? s.mockConsole : undefined;
  const healthOf = (n: ClusterNode) => clusterNodeHealth(s, n, cached, preview?.health);
  const items: HealthItem[] = [
    {
      id: "database",
      label: t("Database"),
      level: cached ? "error" : fresh ? "ok" : "unknown",
      summary: "PostgreSQL",
      details: [],
      href: "/cluster",
    },
    {
      id: "remote",
      label: t("Event bus"),
      level: !fresh
        ? "unknown"
        : s.health?.busConnected === true
          ? "ok"
          : s.health?.busConnected === false
            ? "error"
            : "unknown",
      summary: t("This node"),
      details: [],
      href: "/cluster",
    },
    ...clusterWarnings(s).map((summary): HealthItem => ({
      id: "storage",
      label: t("Needs attention"),
      level: "warn",
      summary,
      details: [],
      href: "/cluster",
    })),
  ];
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      <div className="lg:hidden">
        <Segmented
          size="sm"
          ariaLabel={t("Overview")}
          value={panel}
          onChange={setPanel}
          options={[
            { value: "nodes", label: t("Nodes") },
            { value: "health", label: t("Health") },
            { value: "activity", label: t("Recent activity") },
          ]}
        />
      </div>
      <div className="grid min-h-0 flex-1 gap-4 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div className={`${panel === "nodes" ? "flex" : "hidden"} min-h-0 flex-col gap-4 lg:flex`}>
          <div className="flex flex-wrap gap-x-6 gap-y-2 rounded-xl border border-border bg-surface p-3 text-sm">
            <div>
              <span className="text-fg-muted">{t("Leader")}</span>
              <strong className="ml-2">
                {!cached
                  ? nodes.find((n) => n.id === s.leader)?.name || t("Unknown")
                  : t("Unknown")}
              </strong>
            </div>
            <div>
              {t("Nodes")} <strong>{nodes.length}</strong>
            </div>
            {preview ? <Badge variant="warning">{t("Mock preview")}</Badge> : null}
            <div>
              {t("Health")}: {t("Healthy")}{" "}
              <strong>{nodes.filter((n) => healthOf(n) === "ok").length}</strong> · {t("Failing")}{" "}
              <strong>{nodes.filter((n) => healthOf(n) === "error").length}</strong> ·{" "}
              {t("Unknown")}{" "}
              <strong>{nodes.filter((n) => healthOf(n) === "unknown").length}</strong>
            </div>
            <div>
              {t("Heartbeat")}: {t("Online")}{" "}
              <strong>{nodes.filter((n) => !cached && nodeStatusFresh(n)).length}</strong> ·{" "}
              {t("Unknown")}{" "}
              <strong>{nodes.filter((n) => cached || !nodeStatusFresh(n)).length}</strong>
            </div>
          </div>
          <DashboardPanel
            title={t("Nodes")}
            className="min-h-0 flex-1"
            bodyClassName="overflow-auto p-3"
          >
            <div className="mb-3 flex flex-wrap gap-2">
              {[...new Set(nodes.flatMap(nodeRoles))].map((role) => (
                <Badge key={role} variant="outline">
                  {role} · {nodes.filter((n) => nodeRoles(n).includes(role)).length}
                </Badge>
              ))}
            </div>
            <div className="space-y-2">
              {nodes.map((n) => (
                <article key={n.id} className="rounded-lg border border-border px-3 py-2">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <strong className="text-sm">{nodeName(n)}</strong>
                    <span className="ml-auto">
                      <Badge
                        variant={
                          healthOf(n) === "error"
                            ? "destructive"
                            : healthOf(n) === "ok"
                              ? "success"
                              : "outline"
                        }
                      >
                        {healthOf(n) === "error"
                          ? t("Failing")
                          : healthOf(n) === "ok"
                            ? t("Healthy")
                            : t("Unknown")}
                      </Badge>
                    </span>
                    <Badge variant={n.draining ? "warning" : "outline"}>
                      {n.draining ? t("Draining") : nodeRoles(n).join(" · ")}
                    </Badge>
                  </div>
                  <NodeCapacity node={n} cached={cached} />
                  {preview ? (
                    <dl className="mt-2 grid grid-cols-4 gap-2 text-[11px] sm:text-xs">
                      {[
                        [t("Streams"), !cached && nodeStatusFresh(n) ? n.load : t("Unknown")],
                        [
                          t("Transcoding"),
                          n.live && preview
                            ? (preview.metrics[n.id]?.transcodes ?? t("Unknown"))
                            : t("Unknown"),
                        ],
                        [
                          t("Capacity"),
                          n.live && preview
                            ? (preview.metrics[n.id]?.capacity ?? t("Unknown"))
                            : t("Unknown"),
                        ],
                        [
                          t("Load"),
                          n.live && preview ? `${preview.metrics[n.id]?.load}%` : t("Unknown"),
                        ],
                      ].map(([label, value]) => (
                        <div key={label}>
                          <dt className="text-fg-muted">{label}</dt>
                          <dd className="font-medium tabular-nums">{value}</dd>
                        </div>
                      ))}
                    </dl>
                  ) : null}
                </article>
              ))}
            </div>
          </DashboardPanel>
        </div>
        <div
          className={`${panel !== "nodes" ? "grid" : "hidden"} min-h-0 gap-3 lg:grid lg:grid-rows-[minmax(0,1fr)_minmax(0,0.65fr)]`}
        >
          <div
            className={`${panel === "health" ? "block" : "hidden"} min-h-0 overflow-auto lg:block`}
          >
            <HealthPanel loading={false} items={items} />
          </div>
          <DashboardPanel
            title={t("Recent activity")}
            className={`${panel === "activity" ? "flex" : "hidden"} min-h-0 lg:flex`}
            bodyClassName="overflow-auto p-3"
          >
            {preview ? (
              <ul className="space-y-3 text-xs">
                {preview.events.map((e, i) => (
                  <li key={i}>
                    <strong>{e.node}</strong> ·{" "}
                    {e.kind === "join"
                      ? t("Join cluster")
                      : e.kind === "drain"
                        ? t("Draining")
                        : t("Failover")}
                    <span className="block text-fg-muted">{relativeTime(e.at)}</span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-sm text-fg-muted">{t("Not reported")}</p>
            )}
          </DashboardPanel>
        </div>
      </div>
    </div>
  );
}

function Standalone({
  status,
  available,
  message,
  onComplete,
}: {
  status: ClusterStatus;
  available: boolean;
  message?: string;
  onComplete: () => void;
}) {
  return (
    <>
      {message ? (
        <Alert variant="destructive" role="alert">
          <AlertTitle>{t("Cluster setup needs attention")}</AlertTitle>
          <AlertDescription>{message}</AlertDescription>
        </Alert>
      ) : null}
      <SettingsSection title={t("This server")}>
        <SettingsRow
          title={t("Role")}
          description={t("Your data currently lives in this server’s SQLite database.")}
          control={<Badge variant="secondary">{t("Standalone (SQLite)")}</Badge>}
        />
        {status.self?.version ? (
          <SettingsRow title={t("Version")} description={status.self.version} />
        ) : null}
      </SettingsSection>
      <ClusterSetup available={available} onComplete={onComplete} />
      <a
        className="text-sm text-fg-muted underline"
        href={CLUSTER_DOCS}
        target="_blank"
        rel="noreferrer"
      >
        {t("Read the cluster guide")}
      </a>
    </>
  );
}

export function managerURL(n: ClusterNode): string | null {
  for (const addr of n.addrs ?? []) {
    try {
      const u = new URL(addr);
      if ((u.protocol === "http:" || u.protocol === "https:") && !u.username && !u.password)
        return new URL("/ether/cluster", u).href;
    } catch {
      /* skip malformed connections */
    }
  }
  return null;
}

function Nodes({
  status,
  onChanged,
  onAdd,
  cached,
}: {
  status: ClusterStatus;
  cached: boolean;
  onChanged: () => void;
  onAdd: () => void;
}) {
  const [manage, setManage] = useState<ClusterNode | null>(null);
  const nodes = sortNodes(status.nodes ?? []);
  return (
    <Section
      title={t("Nodes")}
      description={t(
        "Manage nodes here. Remote weight changes require node support; other controls may be unavailable.",
      )}
      actions={
        <div className="flex flex-wrap gap-2">
          <Button size="sm" onClick={onAdd}>
            <Plus size={14} /> {t("Add node")}
          </Button>
          <Button size="sm" variant="outline" onClick={() => setManage(status.self)}>
            {t("Manage this node")}
          </Button>
        </div>
      }
    >
      <div className="max-sm:[&_thead]:hidden max-sm:[&_tr]:grid max-sm:[&_tr]:grid-cols-2 max-sm:[&_td]:whitespace-normal max-sm:[&_td:first-child]:col-span-2 max-sm:[&_td]:text-left">
        <PagedTable<ClusterNode>
          rows={nodes}
          rowKey={(n) => n.id}
          pageSize={25}
          searchable={nodes.length > 8}
          labels={pagedTableLabels()}
          empty={
            <EmptyState
              title={t("No nodes reported")}
              description={t("The database couldn't be read. Nodes appear here once it can.")}
            />
          }
          columns={[
            {
              key: "name",
              header: t("Node"),
              search: (n) => `${nodeName(n)} ${n.version}`,
              cell: (n) => (
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <span className="font-medium text-fg">{nodeName(n)}</span>
                    {n.leader ? <Badge variant="info">{t("Leader")}</Badge> : null}
                    {n.id === status.self?.id ? (
                      <Badge variant="outline">{t("This node")}</Badge>
                    ) : null}
                  </div>
                  <div className="mt-1 flex flex-wrap gap-1" aria-label={t("Configured roles")}>
                    {nodeRoles(n).map((role, i) => (
                      <Badge key={i} variant="outline">
                        {role}
                      </Badge>
                    ))}
                  </div>
                  <span className="block text-xs text-fg-muted">
                    {n.version
                      ? t("Version {version}", { version: n.version })
                      : t("Version unknown")}
                  </span>
                </div>
              ),
            },
            {
              key: "status",
              header: t("Heartbeat"),
              cell: (n) => (
                <div className="flex flex-col items-start gap-1">
                  <span className="text-xs text-fg-muted sm:hidden">{t("Heartbeat")}</span>
                  <Badge
                    variant={
                      cached || (n.live && !nodeStatusFresh(n))
                        ? "warning"
                        : n.live
                          ? "success"
                          : "secondary"
                    }
                  >
                    {cached || (n.live && !nodeStatusFresh(n))
                      ? t("Stale status")
                      : n.live
                        ? t("Online")
                        : t("Offline")}
                  </Badge>
                  <span className="text-xs text-fg-muted" title={formatDateTime(n.lastSeen)}>
                    {t("Last seen {when}", { when: relativeTime(n.lastSeen) })}
                  </span>
                </div>
              ),
            },
            {
              key: "admission",
              header: t("New streams"),
              cell: (n) =>
                status.selfRuntime || n.caps?.diagnostics ? (
                  <RuntimeObservation status={status} node={n} cached={cached} />
                ) : (
                  <div className="space-y-1">
                    <span className="block text-xs text-fg-muted sm:hidden">
                      {t("New streams")}
                    </span>
                    <Badge variant={n.draining ? "warning" : "secondary"}>
                      {cached || !nodeStatusFresh(n)
                        ? t("Unknown")
                        : n.draining
                          ? t("Paused")
                          : t("Not reported")}
                    </Badge>
                    <p className="max-w-48 text-xs text-fg-muted">
                      {t("Playback readiness is not reported.")}
                    </p>
                  </div>
                ),
            },
            {
              key: "load",
              header: t("Streams"),
              align: "right",
              cell: (n) => (
                <span
                  className="tabular-nums"
                  aria-label={t("{node} streams", { node: nodeName(n) })}
                >
                  <span className="block text-xs text-fg-muted sm:hidden">{t("Streams")}</span>
                  {!cached && nodeStatusFresh(n) && Number.isFinite(n.load) ? n.load : t("Unknown")}
                </span>
              ),
            },
            {
              key: "transcode",
              header: t("Transcoding"),
              cell: (n) => (
                <span className="text-fg-muted">
                  <span className="block text-xs sm:hidden">{t("Transcoding")}</span>
                  {transcodeLabel(n)}
                </span>
              ),
            },
            {
              key: "weight",
              header: t("Weight"),
              align: "right",
              cell: (n) => (
                <span
                  className="tabular-nums"
                  aria-label={t("{node} weight", { node: nodeName(n) })}
                >
                  <span className="block text-xs text-fg-muted sm:hidden">{t("Weight")}</span>
                  {n.caps?.weight || 1}
                </span>
              ),
            },
            {
              key: "manage",
              header: "",
              cell: (n) => (
                <Button size="sm" variant="outline" onClick={() => setManage(n)}>
                  {t("Manage")}
                </Button>
              ),
            },
            {
              key: "ip",
              header: t("Source address"),
              cell: (n) => (
                <code
                  className="text-xs text-fg-muted"
                  title={t(
                    "The public address this computer uses for indexers, debrid and Usenet.",
                  )}
                >
                  <span className="block text-xs sm:hidden">{t("Source address")}</span>
                  {n.caps?.egressIp || "—"}
                </code>
              ),
            },
          ]}
        />
      </div>
      {manage ? (
        <NodeDrawer
          status={status}
          node={status.nodes?.find((n) => n.id === manage.id) ?? manage}
          cached={cached}
          onClose={() => setManage(null)}
          onChanged={onChanged}
        />
      ) : null}
    </Section>
  );
}

function NodeDrawer({
  status,
  node,
  cached,
  onClose,
  onChanged,
}: {
  status: ClusterStatus;
  node: ClusterNode;
  cached: boolean;
  onClose: () => void;
  onChanged: () => void;
}) {
  return (
    <Drawer
      open
      onClose={onClose}
      title={t("Manage")}
      description={nodeName(node)}
      footer={
        <Button variant="outline" onClick={onClose}>
          {t("Done")}
        </Button>
      }
    >
      <div className="space-y-5">
        <NodeCapacity node={node} cached={cached} />
        <SettingsSection title={t("Configured roles")}>
          <SettingsRow
            title={nodeRoles(node).join(", ")}
            description={t(
              "Roles come from this node’s startup settings. Change them in its deployment or settings file.",
            )}
          />
        </SettingsSection>
        <RuntimeObservation status={status} node={node} cached={cached} />
        <NodeControls node={node} cached={cached} onChanged={onChanged} />
        <RolePlan status={status} node={node} />
        {managerURL(node) ? (
          <a
            className="text-sm underline"
            href={managerURL(node)!}
            target="_blank"
            rel="noreferrer"
          >
            {t("Open manager")}
          </a>
        ) : null}
      </div>
    </Drawer>
  );
}

export function controlFresh(
  control: ClusterControl | null | undefined,
  node: ClusterNode,
  now = Date.now(),
) {
  const at = parseTime(control?.updatedAt)?.getTime();
  return (
    !!control &&
    !!node.incarnation &&
    control.nodeId === node.id &&
    control.boot === node.incarnation &&
    at !== undefined &&
    at <= now &&
    now - at <= 30_000
  );
}

function NodeControls({
  node,
  cached,
  onChanged,
}: {
  node: ClusterNode;
  cached: boolean;
  onChanged: () => void;
}) {
  const control = useAsync(
    (signal) =>
      node.incarnation
        ? api.clusterControl(node.id, node.incarnation, signal)
        : Promise.resolve(null),
    [node.id, node.incarnation],
  );
  const [weight, setWeight] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [submitted, setSubmitted] = useState<ClusterControl | null>(null);
  const [, tick] = useState(0);
  useInterval(() => tick((n) => n + 1), 1000);
  useInterval(() => void control.reload(), 3000);
  const c =
    submitted &&
    (!control.data ||
      control.data.requestedVersion < submitted.requestedVersion ||
      (control.data.requestedVersion === submitted.requestedVersion &&
        control.data.requestId !== submitted.requestId))
      ? submitted
      : control.data;
  const draftWeight = weight ?? c?.observed.weight ?? node.caps.weight ?? 1;
  const fresh = !cached && !control.error && controlFresh(c, node);
  const pending =
    c?.state === "pending" ||
    (!!c && c.requestedVersion > c.appliedVersion && c.state !== "failed");
  const can = (key: string) =>
    fresh &&
    !busy &&
    !pending &&
    c?.capabilities.some((x) => x.key === key && x.scope === "node" && x.supported);
  const save = async (patch: { weight?: number; draining?: boolean }) => {
    if (!c || !controlFresh(c, node) || pending) return;
    setBusy(true);
    setError("");
    try {
      const result = await api.updateClusterControl(node.id, {
        requestId: crypto.randomUUID(),
        boot: node.incarnation!,
        expectedVersion: c.requestedVersion,
        patch,
      });
      setSubmitted(result);
      void control.reload();
      onChanged();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <SettingsSection title={t("Settings")}>
      <div className="space-y-4 p-4">
        {c ? (
          <div className="flex flex-wrap gap-2">
            <Badge
              variant={
                !fresh
                  ? "warning"
                  : pending
                    ? "info"
                    : c.state === "failed"
                      ? "destructive"
                      : "secondary"
              }
            >
              {!fresh
                ? t("Stale status")
                : pending
                  ? t("Pending")
                  : c.state === "failed"
                    ? t("Failed")
                    : t("Applied")}
            </Badge>
            <span className="text-xs text-fg-muted">
              {t("Settings version")}: {c.appliedVersion} / {c.requestedVersion}
            </span>
          </div>
        ) : (
          <p className="text-sm text-fg-muted">
            {t("Remote controls are not available from this server.")}
          </p>
        )}
        {error ? <ErrorPanel error={error} /> : null}
        {c?.errorCode ? (
          <p className="text-sm text-danger">
            {t("Failed")} · <code>{c.errorCode}</code>
          </p>
        ) : null}
        <Field label={t("Node weight")} htmlFor="cluster-weight">
          <Input
            id="cluster-weight"
            type="number"
            min={1}
            max={100}
            value={draftWeight}
            disabled={!can("weight")}
            onChange={(e) => setWeight(e.target.valueAsNumber)}
          />
        </Field>
        <p className="text-xs text-fg-muted">
          {t("Applied")}: {fresh ? c?.observed.weight : t("Unknown")}
        </p>
        <Button
          disabled={
            !can("weight") || !Number.isInteger(draftWeight) || draftWeight < 1 || draftWeight > 100
          }
          onClick={() => void save({ weight: draftWeight })}
        >
          {t("Save weight")}
        </Button>
        <SettingsRow
          title={t("New streams")}
          description={t(
            "Pausing keeps current streams playing, hands scheduled work to another node and removes this node from published connections. It resumes automatically after a server restart.",
          )}
          control={
            <Badge variant="outline">
              {fresh ? (c?.observed.draining ? t("Paused") : t("Not reported")) : t("Unknown")}
            </Badge>
          }
        />
        <Button
          variant="outline"
          disabled={!can("draining")}
          onClick={() => void save({ draining: !c?.observed.draining })}
        >
          {c?.observed.draining ? t("Resume new streams") : t("Pause new streams")}
        </Button>
        <div className="divide-y divide-border">
          {[
            ["maxStreams", t("Streams")],
            ["defaultStreamLimit", t("Per viewer")],
            ["maxTranscodes", t("Transcoding")],
            ["hwAccel", t("Hardware acceleration")],
          ].map(([key, label]) => {
            const cap = c?.capabilities.find((x) => x.key === key);
            return (
              <div key={key} className="py-2 text-sm">
                <div className="flex justify-between gap-2">
                  <span>{label}</span>
                  <Badge variant="outline">
                    {cap?.scope === "fleet" ? t("Shared settings") : t("Unavailable")}
                  </Badge>
                </div>
                <p className="mt-1 break-words text-xs text-fg-muted">
                  {cap?.reason === "restart_required"
                    ? t("Restart required")
                    : cap?.reason === "fleet_setting"
                      ? t("Shared settings")
                      : t("Remote controls are not available from this server.")}
                </p>
              </div>
            );
          })}
        </div>
      </div>
    </SettingsSection>
  );
}

function RolePlan({ status, node }: { status: ClusterStatus; node: ClusterNode }) {
  const preflight = useAsync(
    (signal) =>
      node.incarnation
        ? api.clusterRolePreflight(node.id, node.incarnation, signal).catch(() => null)
        : Promise.resolve(null),
    [node.id, node.incarnation],
  );
  const evidence =
    preflight.data?.nodeId === node.id &&
    preflight.data.boot === node.incarnation &&
    runtimeFresh(preflight.data.observedAt)
      ? preflight.data
      : null;
  const mock = import.meta.env.MODE === "mock" && !!status.mockConsole;
  const [role, setRole] = useState("transcode");
  const [phase, setPhase] = useState(
    status.mockConsole?.transition.node === node.id && mock ? "drain" : "",
  );
  const [impact, setImpact] = useState(false);
  const snippet = `pools:\n  ${role}:\n    replicas: 1\n    bootstrap: false`;
  return (
    <SettingsSection title={t("Role")}>
      <div className="space-y-3 p-4">
        <p className="text-sm text-fg-muted">
          {t("Role changes require deployment controls and verified safety checks.")}
        </p>
        {evidence ? (
          <details className="text-sm">
            <summary className="cursor-pointer">{t("Details")}</summary>
            <dl className="my-2 grid grid-cols-2 gap-2">
              <dt>{t("Streams")}</dt>
              <dd>{t("Unknown")}</dd>
              <dt>{t("Restart required")}</dt>
              <dd>{t("Unavailable")}</dd>
            </dl>
            <ul className="space-y-1 text-xs text-fg-muted">
              {evidence.reasons.map((reason) => (
                <li key={reason}>
                  <code>{reason}</code>
                </li>
              ))}
            </ul>
          </details>
        ) : null}
        <Field label={t("Role")}>
          <select
            className="w-full rounded-lg border border-border bg-surface p-2"
            disabled={!!phase}
            value={role}
            onChange={(e) => setRole(e.target.value)}
          >
            <option value="full">{t("Full server")}</option>
            <option value="server">{t("Server")}</option>
            <option value="source">{t("Source")}</option>
            <option value="transcode">{t("Transcoding")}</option>
            <option value="delivery">{t("Delivery")}</option>
          </select>
        </Field>
        <Button variant="outline" onClick={() => setImpact(!impact)}>
          {t("Review impact")}
        </Button>
        {impact ? (
          <div className="space-y-3 rounded-lg border border-border p-3">
            <p className="text-sm">
              {t(
                "Pool-wide Helm values example. Adjust replica counts for the whole deployment; this does not change one node.",
              )}
            </p>
            <pre className="overflow-auto rounded bg-bg-subtle p-3 text-xs">{snippet}</pre>
            <Button size="sm" variant="outline" onClick={() => void copyText(snippet)}>
              <Copy size={14} />
              {t("Copy")}
            </Button>
            <p className="text-sm text-fg-muted">
              {t(
                "Pausing keeps current streams playing, hands scheduled work to another node and removes this node from published connections. It resumes automatically after a server restart.",
              )}
            </p>
            <p className="text-sm">
              {t("Live role changes are unavailable. No Kubernetes changes are made here.")}
            </p>
            {mock ? (
              <Button
                disabled={!!phase || node.leader || !nodeStatusFresh(node)}
                onClick={async () => {
                  if (
                    await confirmDialog({
                      title: t("Review impact"),
                      message: t(
                        "Mock only: drain {node}, restart it, then verify its new role ({role}). No live changes are made.",
                        {
                          node: nodeName(node),
                          role: nodeRoles({
                            ...node,
                            caps: { ...node.caps, roles: role === "full" ? [] : [role] },
                          }).join(", "),
                        },
                      ),
                      confirmLabel: t("Mock preview"),
                      tone: "danger",
                    })
                  )
                    setPhase("drain");
                }}
              >
                {t("Mock preview")}
              </Button>
            ) : (
              <Button disabled>{t("Unavailable")}</Button>
            )}
          </div>
        ) : null}
        {mock && phase ? (
          <div className="space-y-2 rounded-lg border border-border p-3">
            <Badge variant="warning">{t("Mock preview")}</Badge>
            <ol className="flex flex-wrap gap-3 text-sm">
              <li className={phase === "drain" ? "font-bold" : "text-fg-muted"}>
                1. {t("Draining")}
              </li>
              <li className={phase === "restart" ? "font-bold" : "text-fg-muted"}>
                2. {t("Restarting…")}
              </li>
              <li className={phase === "role" ? "font-bold" : "text-fg-muted"}>
                3. {t("Role")}:{" "}
                {nodeRoles({
                  ...node,
                  caps: { ...node.caps, roles: role === "full" ? [] : [role] },
                }).join(", ")}
              </li>
            </ol>
            <Button
              size="sm"
              variant="outline"
              disabled={phase === "role"}
              onClick={() => setPhase(phase === "drain" ? "restart" : "role")}
            >
              {t("Next")}
            </Button>
          </div>
        ) : null}
      </div>
    </SettingsSection>
  );
}

type SettingsState = AsyncState<ClusterSettings | null>;

const TOKEN_STATE: Record<
  TokenState,
  { label: string; variant: "success" | "secondary" | "warning" }
> = localized(() => ({
  unused: { label: t("Unused"), variant: "success" },
  used: { label: t("Used"), variant: "secondary" },
  expired: { label: t("Expired"), variant: "warning" },
}));

function JoinTokens({ settings }: { settings: SettingsState }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [, tick] = useState(0);
  useInterval(() => tick((n) => n + 1), 1000);
  const tokens = (settings.data?.joinTokens ?? []).filter(
    (token) => tokenState(token) === "unused",
  );

  const revoke = async (token: JoinTokenInfo) => {
    const ok = await confirmDialog({
      title: t("Revoke join token?"),
      message: t(
        "Token {id} stops working right away. Nodes that already joined are not affected.",
        { id: token.id },
      ),
      confirmLabel: t("Revoke"),
      tone: "danger",
    });
    if (!ok) return;
    setBusy(token.id);
    try {
      await api.revokeJoinToken(token.id);
      await settings.reload();
      toast.success(t("Join token revoked"));
    } catch (e) {
      toast.error(t("Couldn't revoke the token"), { description: errorMessage(e) });
    } finally {
      setBusy(null);
    }
  };

  return (
    <Section
      title={t("Join tokens")}
      description={t(
        "A new node joins with a one-time token created here. Each token works once and expires within the hour.",
      )}
      actions={
        <Button size="sm" onClick={() => setOpen(true)}>
          <Plus size={14} /> {t("Create join token")}
        </Button>
      }
    >
      {settings.error ? (
        <ErrorPanel error={settings.error} onRetry={() => void settings.reload()} />
      ) : !settings.data ? (
        <LoadingRows rows={2} />
      ) : (
        <div className="max-sm:[&_thead]:hidden max-sm:[&_tr]:grid max-sm:[&_tr]:grid-cols-2 max-sm:[&_td]:whitespace-normal max-sm:[&_td]:text-left">
          <PagedTable<JoinTokenInfo>
            rows={tokens}
            rowKey={(token) => token.id}
            pageSize={10}
            searchable={false}
            labels={pagedTableLabels()}
            empty={
              <EmptyState
                icon={<KeyRound />}
                title={t("No join tokens")}
                description={t("Create one when you're ready to add a node.")}
              />
            }
            columns={[
              {
                key: "id",
                header: t("Token"),
                cell: (token) => (
                  <div>
                    <span className="block text-xs text-fg-muted sm:hidden">{t("Token")}</span>
                    <code className="text-xs">{token.id}</code>
                  </div>
                ),
              },
              {
                key: "created",
                header: t("Created"),
                cell: (token) => (
                  <span className="text-fg-muted" title={formatDateTime(token.createdAt)}>
                    <span className="block text-xs sm:hidden">{t("Created")}</span>
                    {relativeTime(token.createdAt)}
                  </span>
                ),
              },
              {
                key: "expires",
                header: t("Expires"),
                cell: (token) => (
                  <span className="text-fg-muted" title={formatDateTime(token.expiresAt)}>
                    <span className="block text-xs sm:hidden">{t("Expires")}</span>
                    {tokenState(token) === "used" ? "—" : relativeTime(token.expiresAt)}
                  </span>
                ),
              },
              {
                key: "state",
                header: t("Status"),
                cell: (token) => {
                  const state = tokenState(token);
                  return (
                    <span className="flex flex-col items-start gap-0.5">
                      <Badge variant={TOKEN_STATE[state].variant}>{TOKEN_STATE[state].label}</Badge>
                      {state === "used" ? (
                        <span
                          className="text-xs text-fg-muted"
                          title={formatDateTime(token.usedAt)}
                        >
                          {token.usedBy
                            ? t("by {node}, {when}", {
                                node: token.usedBy,
                                when: relativeTime(token.usedAt),
                              })
                            : relativeTime(token.usedAt)}
                        </span>
                      ) : null}
                    </span>
                  );
                },
              },
              {
                key: "actions",
                header: "",
                align: "right",
                cell: (token) =>
                  tokenState(token) === "used" ? null : (
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={busy === token.id}
                      aria-label={t("Revoke token {id}", { id: token.id })}
                      onClick={() => void revoke(token)}
                    >
                      {t("Revoke")}
                    </Button>
                  ),
              },
            ]}
          />
        </div>
      )}
      {open ? (
        <JoinTokenDrawer onClose={() => setOpen(false)} onCreated={() => void settings.reload()} />
      ) : null}
    </Section>
  );
}

const TTL_OPTIONS = localized(() => [
  { value: "15", label: tn(15, "{count} minute", "{count} minutes") },
  { value: "30", label: tn(30, "{count} minute", "{count} minutes") },
  { value: "60", label: tn(1, "{count} hour", "{count} hours") },
]);

function JoinTokenDrawer({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [ttl, setTtl] = useState("60");
  const [busy, setBusy] = useState(false);
  // Held only while the drawer is open: the server never returns it again.
  const [created, setCreated] = useState<CreatedJoinToken | null>(null);

  const create = async () => {
    setBusy(true);
    try {
      setCreated(await api.createJoinToken(Number(ttl)));
      onCreated();
    } catch (e) {
      toast.error(t("Couldn't create a join token"), { description: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Drawer
      open
      onClose={onClose}
      title={t("Create join token")}
      description={t("Lets one new machine join this cluster.")}
      size="lg"
      footer={
        created ? (
          <Button variant="outline" onClick={onClose}>
            {t("Done")}
          </Button>
        ) : (
          <Button disabled={busy} onClick={() => void create()}>
            {busy ? t("Creating…") : t("Create join token")}
          </Button>
        )
      }
    >
      <div className="space-y-5">
        <Field
          label={t("Valid for")}
          hint={t("Use the token before it expires; you can always create another.")}
        >
          {created ? (
            <p className="text-sm text-fg">
              {t("Expires {when}", { when: formatDateTime(created.expiresAt) })}
            </p>
          ) : (
            <Segmented
              ariaLabel={t("Valid for")}
              options={TTL_OPTIONS}
              value={ttl}
              onChange={setTtl}
            />
          )}
        </Field>
        {created ? (
          <>
            <Alert variant="warning" role="alert">
              <AlertTitle>{t("Keep this token secret")}</AlertTitle>
              <AlertDescription>
                {created.warning ||
                  t(
                    "It contains the database password and the key that decrypts every stored credential.",
                  )}{" "}
                {t("It's shown only once: after you close this drawer it can't be shown again.")}
              </AlertDescription>
            </Alert>
            <Field label={t("Join token")}>
              <Textarea
                aria-label={t("Join token")}
                readOnly
                rows={4}
                value={created.token}
                className="font-mono text-xs break-all"
              />
            </Field>
            <Button variant="outline" size="sm" onClick={() => copyToken(created.token)}>
              <Copy size={14} /> {t("Copy token")}
            </Button>
            <Field
              label={t("On the new machine")}
              hint={t(
                "Open its Server Manager, choose Cluster → Join cluster and paste this token. Ether checks the connection, backs up its data and restarts automatically.",
              )}
            >
              <p className="text-sm text-fg-muted">
                {t(
                  "Install and open Ether on the new machine. You can join before completing its standalone setup.",
                )}
              </p>
            </Field>
          </>
        ) : (
          <p className="text-sm text-fg-muted">
            {t(
              "Paste the token into Cluster → Join cluster on the new machine. It is shown once and contains the database password and the credential key, so treat it like a password.",
            )}
          </p>
        )}
      </div>
    </Drawer>
  );
}

function readinessReason(reason: string): string {
  switch (reason) {
    case "age_at_or_above_maximum":
    case "not_live":
    case "self_missing":
      return t("Heartbeat unavailable");
    case "set_pool":
    case "set_target":
    case "initial_quota_read":
      return t("Placement unavailable");
    case "invalid_roles":
    case "cluster_mismatch":
    case "boot_mismatch":
    case "mounted_identity_changed":
      return t("Node identity or roles need attention");
    case "clock_skew":
      return t("Clock skew");
    case "credential_rotation_failed":
      return t("Credentials need attention");
    default:
      return t("Runtime unavailable");
  }
}
function NodeCapacity({ node, cached }: { node: ClusterNode; cached: boolean }) {
  const d = node.caps?.diagnostics;
  const fresh = !cached && nodeStatusFresh(node) && !!d && runtimeFresh(d.sampledAt);
  const value = (n: number | undefined) =>
    fresh && n !== undefined ? n.toLocaleString() : t("Unknown");
  return (
    <dl className="mt-2 grid gap-x-4 gap-y-1 text-xs sm:grid-cols-2">
      {[
        [t("Transcoding"), transcodeLabel(node)],
        [t("Accepting new work"), fresh ? (d?.acceptingNew ? t("Yes") : t("No")) : t("Unknown")],
        [t("Draining"), fresh ? (d?.draining ? t("Yes") : t("No")) : t("Unknown")],
        [
          t("Clock skew"),
          fresh && d?.clock && runtimeFresh(d.clock.sampledAt)
            ? t("{value} ms", { value: Math.round(d.clock.clockSkewNs / 1e6) })
            : t("Unknown"),
        ],
        [t("Transcode slots"), `${value(d?.transcodes)} / ${value(d?.transcodeCapacity)}`],
        [t("Source connections"), value(d?.sourceConnections)],
        [
          t("Delivery bandwidth"),
          fresh && d?.deliveryBytesPerSecond !== undefined
            ? t("{value} B/s", { value: value(d.deliveryBytesPerSecond) })
            : t("Unknown"),
        ],
        [t("Codecs"), fresh && d?.codecs?.length ? d.codecs.join(", ") : t("Unknown")],
        [
          t("Readiness"),
          fresh
            ? d?.readinessReason
              ? readinessReason(d.readinessReason)
              : d?.readyExisting
                ? t("Ready")
                : t("Runtime unavailable")
            : t("Unknown"),
        ],
        [
          t("Telemetry"),
          d?.sampledAt ? (fresh ? relativeTime(d.sampledAt) : t("Stale status")) : t("Unknown"),
        ],
      ].map(([label, v]) => (
        <div key={label}>
          <dt className="text-fg-muted">{label}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}
