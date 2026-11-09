import { getLocale, localized, t, tn } from "../../../i18n";
import { api, apiFetch } from "../../../lib/api";
import { fixed } from "../../../lib/format";
import { useAsync } from "../../../lib/hooks";
import type {
  DebridProviderStatus,
  DebridStatus,
  EtherConfig,
  UsenetProvider,
} from "../../../lib/types";
import { tv, type Source } from "../../livetv/model";
import { DEBRID_KINDS } from "../cards";
import type { UsageReport } from "../IndexerUsage";

/** GET /stats/sources: daily rows per source over a window of UTC days. */
export type PlayDay = {
  day: string;
  origin: string;
  plays: number;
  failovers: number;
  failed: number;
};
export type ProviderDay = {
  id: string;
  day: string;
  bytes: number;
  articles: number;
  misses: number;
  errors: number;
  backfillSaves: number;
  bodyMisses: number;
  bodyTakedowns: number;
  spillovers: number;
};
export type IndexerDay = {
  indexer: string;
  day: string;
  searches: number;
  successes: number;
  grabs: number;
  grabSuccess: number;
  budgets: number;
  latencyMs: number;
  ageSeconds: number;
  ageSamples: number;
};
export type DebridDay = {
  id: string;
  day: string;
  requests: number;
  failures: number;
  cached: number;
  notCached: number;
  blocked: number;
  linksRefreshed: number;
  reused: number;
  /** Bytes the streaming engine fetched through this account. */
  bytes?: number;
};
export type StreamDay = {
  day: string;
  memoryHits: number;
  diskHits: number;
  blocksFetched: number;
  bytesFetched: number;
  hedges: number;
  hedgeWins: number;
  linkRefreshes: number;
  errors: number;
};
/** Today's plays in one UTC hour (0..23); only for a one-day window. */
export type PlayHour = {
  hour: number;
  origin: string;
  plays: number;
  failovers: number;
  failed: number;
};
/** Every source's totals since its first recorded day. */
export type Lifetime = {
  providers: {
    id: string;
    firstDay: string;
    bytes: number;
    articles: number;
    misses: number;
    errors: number;
  }[];
  indexers: {
    indexer: string;
    firstDay: string;
    searches: number;
    successes: number;
    grabs: number;
    grabSuccess: number;
  }[];
  debrid: {
    id: string;
    firstDay: string;
    requests: number;
    failures: number;
    cached: number;
    notCached: number;
    bytes: number;
  }[];
};
export type SourceHistory = {
  days: number;
  since: string;
  until: string;
  plays: PlayDay[] | null;
  providerDays: ProviderDay[] | null;
  indexerDays: IndexerDay[] | null;
  debridDays: DebridDay[] | null;
  streamDays: StreamDay[] | null;
  hours?: PlayHour[] | null;
  lifetime?: Lifetime | null;
};

/** One Usenet provider's live sample (usenet.ProviderStats). */
export type UsenetSample = {
  id: string;
  name: string;
  host: string;
  priority?: number;
  backfill: boolean;
  state: string;
  backoffUntil?: string;
  lastError?: string;
  lastErrorAt?: string;
  maxConnections?: number;
  configuredConnections?: number;
  liveConnections?: number;
  activeConnections?: number;
  idleConnections?: number;
  limitLearned?: boolean;
  serverLimit?: number;
  limitRefusals?: number;
  bytes: number;
  articles: number;
  misses: number;
  errors: number;
  dials?: number;
  /** Durations are nanoseconds. */
  avgLatency: number;
  avgFetchTime?: number;
  tcpDialTime?: number;
  tlsHandshakeTime?: number;
  authTime?: number;
  liveFetchP50?: number;
  hedges?: number;
  hedgeWins?: number;
  hedgeLosses?: number;
};
type Budget = {
  indexer: string;
  hitsUsed: number;
  hitsRemaining: number;
  grabsUsed: number;
  grabsRemaining: number;
  retryAt?: number;
};
type ProviderMonth = { id: string; bytes: number };
export type Cost = { kind: string; id: string; monthly: number; currency: string };
export type Infrastructure = {
  monthPlays: number;
  snapshots: {
    providers?: { data: UsenetSample[] | null; at: number };
    indexers?: { data: Budget[] | null; at: number };
  };
  providerMonth: ProviderMonth[] | null;
  costs: Cost[] | null;
  month: string;
  failovers?: number;
  failedPlays?: number;
};

export type IndexerUsage = NonNullable<UsageReport["indexers"]>[number];

export type PluginSourceSummary = {
  id: string;
  label: string;
  kind: string;
  summary: string;
};

export type CostKind = "provider" | "indexer" | "debrid" | "iptv";
export type SourceKind = "usenet" | "indexer" | "debrid" | "iptv" | "plugin";
export type Health = "ok" | "warning" | "danger" | "off";

/** One row of Statistics › Usage: any source, with what it did in the window. */
export type SourceRow = {
  key: string;
  /** Position in the owner's setup order (kind, then priority). */
  order: number;
  kind: SourceKind;
  id: string;
  name: string;
  detail: string;
  health: Health;
  healthLabel: string;
  /** Daily activity across the window (bytes, searches or requests). */
  series: number[];
  usage: string;
  usageSort: number;
  success: number | null;
  latencyMs: number | null;
  cost?: { kind: CostKind; id: string; price?: Cost };
};

export const PERIODS = ["1", "7", "30", "90"] as const;
export type Period = (typeof PERIODS)[number];
export const periodOf = (v: string | null): Period =>
  (PERIODS as readonly string[]).includes(v ?? "") ? (v as Period) : "30";

/** "Today" or "Last 30 days". */
export const periodLabel = (days: number | string) =>
  Number(days) === 1 ? t("Today") : tn(Number(days), "Last {count} day", "Last {count} days");

/** Short labels for the period switches ("Today", "7 days"). */
export const periodOptions = () =>
  PERIODS.map((n) => ({
    value: n,
    label: n === "1" ? t("Today") : tn(Number(n), "{count} day", "{count} days"),
  }));

export const KIND_LABEL = localized((): Record<SourceKind, string> => ({
  usenet: t("Usenet"),
  indexer: t("Indexer"),
  debrid: t("Debrid"),
  iptv: t("IPTV"),
  plugin: t("Plugin"),
}));

export type StatsData = {
  history: SourceHistory;
  infra: Infrastructure | null;
  usage: UsageReport | null;
  debrid: DebridStatus | null;
  iptv: Source[] | null;
  /** Configured Usenet providers, so every one is listed even with no traffic. */
  usenet: UsenetProvider[] | null;
};

const fetchHistory = (days: string | number, signal?: AbortSignal) =>
  apiFetch<SourceHistory>(`/stats/sources?days=${days}`, { signal });

/**
 * Everything Statistics shows, read in parallel. Only the history is
 * required; each live source document fills in when it answers.
 */
export function useSourceStats(days: Period) {
  return useAsync<StatsData>(
    async (signal) => {
      const [history, infra, usage, debrid, iptv, config] = await Promise.all([
        fetchHistory(days, signal),
        apiFetch<Infrastructure>("/stats/infrastructure", { signal }).catch(() => null),
        apiFetch<UsageReport>("/indexer-usage", { signal }).catch(() => null),
        api.debridStatus().catch(() => null),
        tv.sources(signal).catch(() => null),
        api.getConfig().catch((): EtherConfig | null => null),
      ]);
      return {
        history,
        infra,
        usage,
        debrid,
        iptv: Array.isArray(iptv) ? iptv : null,
        usenet: config?.usenet?.providers ?? null,
      };
    },
    [days],
  );
}

/**
 * A drawer's own window: the page's history when the periods match, else
 * one bounded /stats/sources read for the drawer's period.
 */
export function useDrawerHistory(page: SourceHistory | undefined, days: Period) {
  const same = page != null && String(page.days) === days;
  const own = useAsync<SourceHistory | null>(
    (signal) => (same ? Promise.resolve(null) : fetchHistory(days, signal)),
    [same, days],
  );
  return same ? page : (own.data ?? undefined);
}

/** Every UTC day in the window, oldest first. */
export function windowDays(h: SourceHistory): string[] {
  const out: string[] = [];
  const end = Date.parse(`${h.until}T00:00:00Z`);
  for (let at = Date.parse(`${h.since}T00:00:00Z`); at <= end; at += 86_400_000)
    out.push(new Date(at).toISOString().slice(0, 10));
  return out;
}

/** A UTC day as a short local date ("Nov 8"). */
export function dayLabel(day: string): string {
  return new Date(`${day}T00:00:00Z`).toLocaleDateString(getLocale(), {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

/** Sum rows into the window's days with `value`; missing days are 0. */
export function daily<T extends { day: string }>(
  days: string[],
  rows: T[],
  value: (row: T) => number,
): number[] {
  const at = new Map(days.map((d, i) => [d, i]));
  const out = days.map(() => 0);
  for (const r of rows) {
    const i = at.get(r.day);
    if (i !== undefined) out[i] = out[i]! + value(r);
  }
  return out;
}

export const sum = <T>(rows: T[], value: (row: T) => number) =>
  rows.reduce((n, r) => n + value(r), 0);

export const ratio = (n: number, d: number): number | null => (d > 0 ? n / d : null);

export const priceKey = (kind: CostKind, id: string) => `${kind}:${id}`;

const DAY_MS = 86_400_000;

function usenetHealth(p: UsenetSample): [Health, string] {
  if (p.state === "ok") return ["ok", t("Healthy")];
  if (p.state === "auth_fail" || p.state === "auth_failed") return ["danger", t("Sign-in failed")];
  return ["warning", t("Needs attention")];
}

function debridHealth(p: DebridProviderStatus): [Health, string] {
  if (!p.enabled || p.state === "disabled") return ["off", t("Turned off")];
  if (p.state === "ok") return ["ok", t("Healthy")];
  if (p.state === "auth_failed") return ["danger", t("Sign-in failed")];
  return ["warning", t("Needs attention")];
}

function iptvHealth(s: Source): [Health, string] {
  const expires = s.account?.expiresAt ? Date.parse(s.account.expiresAt) : NaN;
  if (!Number.isNaN(expires) && expires <= Date.now()) return ["danger", t("Expired")];
  if (s.lastError) return ["danger", t("Needs attention")];
  if (!Number.isNaN(expires) && expires - Date.now() < 7 * DAY_MS)
    return ["warning", t("Expires soon")];
  return ["ok", t("Healthy")];
}

/**
 * Every configured Usenet provider in priority order with its live sample,
 * so a provider with no traffic (or that the engine is not using) is still
 * listed. Without the config, the live sample alone.
 */
export function usenetProviders(data: StatsData) {
  const samples = data.infra?.snapshots.providers?.data ?? [];
  const sampleOf = (id: string, host: string) =>
    samples.find((s) => (s.id || s.host) === id) ??
    (id ? undefined : samples.find((s) => s.host === host));
  if (!data.usenet)
    return samples.map((s) => ({
      id: s.id || s.host,
      name: s.name || s.id || s.host,
      host: s.host,
      enabled: true,
      sample: s as UsenetSample | undefined,
    }));
  return [...data.usenet]
    .sort((a, b) => a.priority - b.priority)
    .map((p) => {
      const id = p.id || p.host;
      return {
        id,
        name: p.name || p.host,
        host: p.host,
        enabled: p.enabled !== false,
        sample: sampleOf(id, p.host),
      };
    });
}

/** Every source as one table row, in the order owners set them up. */
export function sourceRows(data: StatsData, plugins: PluginSourceSummary[]): SourceRow[] {
  const { history: h, infra, usage, debrid, iptv } = data;
  const days = windowDays(h);
  const costs = new Map((infra?.costs ?? []).map((c) => [priceKey(c.kind as CostKind, c.id), c]));
  const cost = (kind: CostKind, id: string) => ({
    kind,
    id,
    price: costs.get(priceKey(kind, id)),
  });
  const rows: Omit<SourceRow, "order">[] = [];

  for (const { id, name, host, sample: p, enabled } of usenetProviders(data)) {
    const mine = (h.providerDays ?? []).filter((d) => d.id === id);
    const bytes = sum(mine, (d) => d.bytes);
    const articles = sum(mine, (d) => d.articles);
    const [health, healthLabel]: [Health, string] = !enabled
      ? ["off", t("Turned off")]
      : p
        ? usenetHealth(p)
        : ["off", t("Idle")];
    rows.push({
      key: `usenet:${id}`,
      kind: "usenet",
      id,
      name,
      detail: host,
      health,
      healthLabel,
      series: daily(days, mine, (d) => d.bytes),
      usage: formatGB(bytes),
      usageSort: bytes,
      success: ratio(articles, articles + sum(mine, (d) => d.misses + d.errors)),
      latencyMs: p?.avgLatency ? p.avgLatency / 1e6 : null,
      cost: cost("provider", id),
    });
  }

  const budgets = infra?.snapshots.indexers?.data ?? [];
  const indexers: { name: string; usage?: IndexerUsage }[] =
    usage?.indexers?.map((u) => ({ name: u.name, usage: u })) ??
    budgets.map((b) => ({ name: b.indexer }));
  for (const { name, usage: u } of indexers) {
    const mine = (h.indexerDays ?? []).filter((d) => d.indexer === name);
    const searches = sum(mine, (d) => d.searches);
    const limited = Boolean(u?.retryAt && u.retryAt > Date.now() / 1000);
    rows.push({
      key: `indexer:${name}`,
      kind: "indexer",
      id: name,
      name,
      detail: u?.torrent ? t("Torrent indexer") : t("Usenet indexer"),
      health: u && !u.enabled ? "off" : limited ? "warning" : "ok",
      healthLabel:
        u && !u.enabled ? t("Turned off") : limited ? t("Daily limit reached") : t("Healthy"),
      series: daily(days, mine, (d) => d.searches),
      usage: tn(searches, "{count} search", "{count} searches"),
      usageSort: searches,
      success: ratio(
        sum(mine, (d) => d.successes),
        searches,
      ),
      latencyMs: searches ? sum(mine, (d) => d.latencyMs) / searches : null,
      cost: cost("indexer", name),
    });
  }

  for (const p of debrid?.providers ?? []) {
    const mine = (h.debridDays ?? []).filter((d) => d.id === p.id);
    const requests = sum(mine, (d) => d.requests);
    const bytes = sum(mine, (d) => d.bytes ?? 0);
    const [health, healthLabel] = debridHealth(p);
    rows.push({
      key: `debrid:${p.id}`,
      kind: "debrid",
      id: p.id,
      name: p.name || p.kind,
      detail: DEBRID_KINDS.find((k) => k.value === p.kind)?.label ?? p.kind,
      health,
      healthLabel,
      // Data streamed is what an account is for; days before per-account
      // data was recorded fall back to its API requests.
      series: bytes ? daily(days, mine, (d) => d.bytes ?? 0) : daily(days, mine, (d) => d.requests),
      usage: bytes ? formatGB(bytes) : tn(requests, "{count} request", "{count} requests"),
      usageSort: bytes || requests,
      success: ratio(requests - sum(mine, (d) => d.failures), requests),
      latencyMs: p.latency?.samples ? (p.latency.resolveMs ?? null) : null,
      cost: cost("debrid", p.id),
    });
  }

  for (const s of iptv ?? []) {
    if (!s.hasPlaylist && !s.account) continue;
    const [health, healthLabel] = iptvHealth(s);
    const channels = s.channelCount ?? 0;
    rows.push({
      key: `iptv:${s.id}`,
      kind: "iptv",
      id: s.id,
      name: s.name,
      detail: s.account ? t("Xtream provider") : t("Playlist"),
      health,
      healthLabel,
      series: [],
      usage: tn(channels, "{count} channel", "{count} channels"),
      usageSort: channels,
      success: null,
      latencyMs: null,
      cost: cost("iptv", s.id),
    });
  }

  for (const p of plugins)
    rows.push({
      key: `plugin:${p.id}`,
      kind: "plugin",
      id: p.id,
      name: p.label,
      detail: p.kind,
      health: "ok",
      healthLabel: t("Healthy"),
      series: [],
      usage: p.summary,
      usageSort: 0,
      success: null,
      latencyMs: null,
    });
  return rows.map((r, order) => ({ ...r, order }));
}

const GB = 1e9;

/** Bytes as decimal gigabytes, the unit providers bill in. */
export function formatGB(bytes: number): string {
  if (bytes >= 1e12) return t("{size} TB", { size: fixed(bytes / 1e12, 2) });
  if (bytes <= 0) return t("{size} GB", { size: "0" });
  return t("{size} GB", { size: fixed(bytes / GB, bytes >= 100 * GB ? 0 : 1) });
}

/** Hours of the one-day window so far: 24 for a past day, up to now for today. */
export function hoursSoFar(h: SourceHistory): number {
  const start = Date.parse(`${h.since}T00:00:00Z`);
  return Math.max(1, Math.min(24, Math.floor((Date.now() - start) / 3_600_000) + 1));
}

/** The window's UTC hours so far as local clock labels ("7 PM"). */
export function hourLabels(h: SourceHistory): string[] {
  const start = Date.parse(`${h.since}T00:00:00Z`);
  return Array.from({ length: hoursSoFar(h) }, (_, i) =>
    new Date(start + i * 3_600_000).toLocaleTimeString(getLocale(), { hour: "numeric" }),
  );
}

/** Sum rows into the window's hours so far. */
export function hourly(
  h: SourceHistory,
  rows: PlayHour[],
  value: (row: PlayHour) => number,
): number[] {
  const n = hoursSoFar(h);
  const out = Array.from({ length: n }, () => 0);
  for (const r of rows) if (r.hour >= 0 && r.hour < n) out[r.hour] = out[r.hour]! + value(r);
  return out;
}

/** A recorded UTC day as a local date with the year ("Oct 9, 2026"). */
export function longDay(day: string): string {
  return new Date(`${day}T00:00:00Z`).toLocaleDateString(getLocale(), {
    year: "numeric",
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}
