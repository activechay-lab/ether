/**
 * Sources › Statistics mock data: deterministic daily history for the mock
 * config's Usenet providers, indexers and debrid accounts, shaped like the
 * real /stats/sources, /stats/infrastructure and /indexer-usage documents.
 */

const DAY = 86_400_000;

/** A stable 0..1 value per key, so reloads draw the same charts. */
function noise(key: string): number {
  let h = 2166136261;
  for (let i = 0; i < key.length; i++) h = Math.imul(h ^ key.charCodeAt(i), 16777619);
  return ((h >>> 0) % 10_000) / 10_000;
}

const iso = (at: number) => new Date(at).toISOString().slice(0, 10);

function days(n: number): string[] {
  const today = Math.floor(Date.now() / DAY) * DAY;
  return Array.from({ length: n }, (_, i) => iso(today - (n - 1 - i) * DAY));
}

/** Evenings and weekends are busier; a slow upward trend over the window. */
function load(day: string, i: number, n: number, key: string): number {
  const weekday = new Date(`${day}T00:00:00Z`).getUTCDay();
  const weekend = weekday === 0 || weekday === 6 ? 1.35 : weekday === 5 ? 1.15 : 1;
  return weekend * (0.75 + (0.35 * i) / Math.max(1, n - 1)) * (0.7 + 0.6 * noise(key + day));
}

const PROVIDERS = [
  { id: "p1", gb: 38, articleKB: 740, miss: 0.006, err: 0.0008 },
  { id: "p2", gb: 2.4, articleKB: 740, miss: 0.08, err: 0.002 },
];
const INDEXERS = [
  {
    name: "Indexer One",
    searches: 160,
    feeds: 48,
    cached: 95,
    success: 0.86,
    latency: 420,
    grabs: 9,
  },
  {
    name: "Indexer Two",
    searches: 24,
    feeds: 0,
    cached: 18,
    success: 0.71,
    latency: 910,
    grabs: 3,
  },
  {
    name: "Torrent Tracker",
    searches: 70,
    feeds: 24,
    cached: 42,
    success: 0.64,
    latency: 1350,
    grabs: 0,
  },
  {
    name: "Stream Addon",
    searches: 95,
    feeds: 0,
    cached: 64,
    success: 0.92,
    latency: 260,
    grabs: 0,
  },
];
const DEBRID = [
  { id: "rd-1", name: "Real-Debrid", requests: 210, fail: 0.015, cached: 0.78, gb: 26 },
  { id: "tb-1", name: "TorBox backup", requests: 38, fail: 0.09, cached: 0.52, gb: 3.5 },
];
/** Per-account data is recorded from a later build than the other debrid counters. */
const DEBRID_BYTES_DAYS = 9;

export function sourceHistory(n: number, withLifetime = true) {
  const window = days(n);
  const plays: object[] = [];
  const providerDays: object[] = [];
  const indexerDays: object[] = [];
  const debridDays: object[] = [];
  const streamDays: object[] = [];
  window.forEach((day, i) => {
    const l = (key: string) => load(day, i, n, key);
    for (const [origin, base] of [
      ["Usenet", 14],
      ["Real-Debrid", 8],
      ["TorBox backup", 1.5],
      ["HTTP", 2],
    ] as const) {
      const count = Math.round(base * l(origin));
      if (!count) continue;
      plays.push({
        day,
        origin,
        plays: count,
        failovers: Math.round(count * 0.08 * noise(`fo${origin}${day}`)),
        failed: noise(`fail${origin}${day}`) > 0.82 ? 1 : 0,
      });
    }
    for (const p of PROVIDERS) {
      const bytes = Math.round(p.gb * 1e9 * l(p.id));
      const articles = Math.round(bytes / (p.articleKB * 1024));
      const misses = Math.round(articles * p.miss * (0.5 + noise(`m${p.id}${day}`)));
      providerDays.push({
        id: p.id,
        day,
        bytes,
        articles,
        misses,
        errors: Math.round(articles * p.err * noise(`e${p.id}${day}`)),
        backfillSaves: p.id === "p2" ? Math.round(articles * 0.04) : 0,
        bodyMisses: Math.round(misses * 0.7),
        bodyTakedowns: Math.round(misses * 0.05),
        spillovers: p.id === "p2" ? Math.round(articles * 0.01) : 0,
      });
    }
    for (const x of INDEXERS) {
      const searches = Math.round(x.searches * l(x.name));
      const successes = Math.round(
        searches * x.success * (0.92 + 0.16 * noise(`s${x.name}${day}`)),
      );
      const grabs = Math.round(x.grabs * l(`g${x.name}`));
      const feeds = x.feeds;
      const cacheHits = Math.round(x.cached * l(`c${x.name}`));
      indexerDays.push({
        indexer: x.name,
        day,
        requests: searches + grabs + feeds,
        cacheHits,
        searches,
        successes: Math.min(searches, successes),
        grabs,
        grabSuccess: Math.max(0, grabs - (noise(`gs${x.name}${day}`) > 0.85 ? 1 : 0)),
        budgets: x.name === "Indexer Two" && noise(`b${day}`) > 0.7 ? 3 : 0,
        latencyMs: Math.round(searches * x.latency * (0.8 + 0.4 * noise(`l${x.name}${day}`))),
        ageSeconds: Math.round(successes * 86400 * (12 + 90 * noise(`a${x.name}`))),
        ageSamples: successes,
      });
    }
    // Debrid history starts with this release, so the oldest days are empty.
    if (i < n - 21) return;
    let blocks = 0;
    for (const d of DEBRID) {
      const requests = Math.round(d.requests * l(d.id));
      const checks = Math.round(requests * 0.45);
      const cached = Math.round(checks * d.cached);
      blocks += requests * 6;
      debridDays.push({
        id: d.id,
        day,
        requests,
        failures: Math.round(requests * d.fail * (0.4 + 1.2 * noise(`f${d.id}${day}`))),
        cached,
        notCached: checks - cached,
        blocked: noise(`bl${d.id}${day}`) > 0.85 ? 1 : 0,
        linksRefreshed: Math.round(requests * 0.03),
        reused: Math.round(requests * 0.12),
        bytes: i >= n - DEBRID_BYTES_DAYS ? Math.round(d.gb * 1e9 * l(`b${d.id}`)) : 0,
      });
    }
    const hits = Math.round(blocks * (1.4 + 0.6 * noise(`hit${day}`)));
    streamDays.push({
      day,
      memoryHits: Math.round(hits * 0.35),
      diskHits: hits - Math.round(hits * 0.35),
      blocksFetched: blocks,
      bytesFetched: blocks * 4 * 1024 * 1024,
      hedges: Math.round(blocks * 0.01),
      hedgeWins: Math.round(blocks * 0.006),
      linkRefreshes: Math.round(blocks * 0.002),
      errors: noise(`se${day}`) > 0.9 ? 1 : 0,
    });
  });
  // Today's plays spread over the UTC hours so far, matching the day's total.
  const hours: object[] = [];
  if (n === 1) {
    const now = new Date().getUTCHours();
    type P = { origin: string; plays: number; failovers: number; failed: number };
    for (const p of plays as P[]) {
      const weights = Array.from({ length: now + 1 }, (_, h) => 0.2 + noise(`h${p.origin}${h}`));
      const total = weights.reduce((a, w) => a + w, 0);
      let left = { plays: p.plays, failovers: p.failovers, failed: p.failed };
      weights.forEach((w, hour) => {
        const last = hour === now;
        const take = (k: keyof typeof left) =>
          last ? left[k] : Math.min(left[k], Math.round((p[k] * w) / total));
        const row = { plays: take("plays"), failovers: take("failovers"), failed: take("failed") };
        left = {
          plays: left.plays - row.plays,
          failovers: left.failovers - row.failovers,
          failed: left.failed - row.failed,
        };
        if (row.plays) hours.push({ hour, origin: p.origin, ...row });
      });
    }
  }
  return {
    days: n,
    since: window[0],
    until: window[window.length - 1],
    plays,
    providerDays,
    indexerDays,
    debridDays,
    streamDays,
    hours,
    lifetime: withLifetime ? lifetime() : null,
  };
}

type Row = Record<string, number | string>;
const add = (rows: Row[], key: string, id: string, field: string) =>
  rows.filter((r) => r[key] === id).reduce((n, r) => n + Number(r[field] ?? 0), 0);
const first = (rows: Row[], key: string, id: string) =>
  rows.filter((r) => r[key] === id && Number(r.requests ?? 1) > 0).map((r) => String(r.day))[0] ??
  "";

/**
 * All-time totals: Usenet and indexers have about a year more history than
 * the 90-day window (four times it); debrid history is all inside it.
 */
function lifetime() {
  const h = sourceHistory(90, false);
  const p = h.providerDays as Row[];
  const x = h.indexerDays as Row[];
  const d = h.debridDays as Row[];
  const older = iso(Date.now() - 400 * DAY);
  return {
    providers: PROVIDERS.map(({ id }) => ({
      id,
      firstDay: older,
      bytes: 4 * add(p, "id", id, "bytes"),
      articles: 4 * add(p, "id", id, "articles"),
      misses: 4 * add(p, "id", id, "misses"),
      errors: 4 * add(p, "id", id, "errors"),
    })),
    indexers: INDEXERS.map(({ name }) => ({
      indexer: name,
      firstDay: older,
      requests: 4 * add(x, "indexer", name, "requests"),
      cacheHits: 4 * add(x, "indexer", name, "cacheHits"),
      searches: 4 * add(x, "indexer", name, "searches"),
      successes: 4 * add(x, "indexer", name, "successes"),
      grabs: 4 * add(x, "indexer", name, "grabs"),
      grabSuccess: 4 * add(x, "indexer", name, "grabSuccess"),
      budgets: 4 * add(x, "indexer", name, "budgets"),
    })),
    debrid: DEBRID.map(({ id }) => ({
      id,
      firstDay: first(d, "id", id),
      requests: add(d, "id", id, "requests"),
      failures: add(d, "id", id, "failures"),
      cached: add(d, "id", id, "cached"),
      notCached: add(d, "id", id, "notCached"),
      bytes: add(d, "id", id, "bytes"),
    })),
  };
}

type Cost = { kind: string; id: string; monthly: number; currency: string };
const costs: Cost[] = [
  { kind: "provider", id: "p1", monthly: 12.99, currency: "USD" },
  { kind: "indexer", id: "Indexer One", monthly: 1.5, currency: "USD" },
  { kind: "debrid", id: "rd-1", monthly: 4.25, currency: "USD" },
];

/** PUT /stats/costs: one plan price, kept for the session. */
export function saveCost(c: Cost) {
  const i = costs.findIndex((x) => x.kind === c.kind && x.id === c.id);
  const next = { ...c, currency: c.currency.toUpperCase() };
  if (i >= 0) costs[i] = next;
  else costs.push(next);
}

export function infrastructure() {
  const month = new Date().toISOString().slice(0, 8) + "01";
  const h = sourceHistory(31);
  type P = { id: string; day: string; bytes: number; articles: number };
  const inMonth = (h.providerDays as P[]).filter((d) => d.day >= month);
  const at = Math.floor(Date.now() / 1000) - 20;
  return {
    month: month.slice(0, 7),
    monthPlays: (h.plays as { day: string; plays: number }[])
      .filter((p) => p.day >= month)
      .reduce((n, p) => n + p.plays, 0),
    failovers: 9,
    failedPlays: 2,
    notes: null,
    snapshots: {
      providers: {
        at,
        data: [
          {
            id: "p1",
            name: "Primary news",
            host: "news.example.com",
            priority: 0,
            state: "ok",
            backfill: false,
            maxConnections: 20,
            configuredConnections: 20,
            liveConnections: 12,
            activeConnections: 4,
            idleConnections: 8,
            bytes: 0,
            articles: 0,
            misses: 0,
            errors: 0,
            dials: 31,
            avgLatency: 38_000_000,
            avgFetchTime: 112_000_000,
            tcpDialTime: 18_000_000,
            tlsHandshakeTime: 41_000_000,
            authTime: 22_000_000,
            hedges: 46,
            hedgeWins: 29,
            hedgeLosses: 17,
          },
          {
            id: "p2",
            name: "Block account",
            host: "block.example.net",
            priority: 1,
            state: "backoff",
            backfill: true,
            lastError: "502 too many connections for this account",
            lastErrorAt: new Date(Date.now() - 25 * 60_000).toISOString(),
            maxConnections: 6,
            configuredConnections: 8,
            liveConnections: 2,
            activeConnections: 0,
            idleConnections: 2,
            limitLearned: true,
            serverLimit: 6,
            bytes: 0,
            articles: 0,
            misses: 0,
            errors: 0,
            avgLatency: 84_000_000,
            avgFetchTime: 260_000_000,
            tcpDialTime: 35_000_000,
            tlsHandshakeTime: 80_000_000,
            authTime: 40_000_000,
            hedges: 3,
            hedgeWins: 1,
            hedgeLosses: 2,
          },
        ],
      },
      indexers: { at, data: [] },
    },
    providerMonth: PROVIDERS.map((p) => ({
      id: p.id,
      bytes: inMonth.filter((d) => d.id === p.id).reduce((n, d) => n + d.bytes, 0),
      articles: inMonth.filter((d) => d.id === p.id).reduce((n, d) => n + d.articles, 0),
      misses: 0,
      errors: 0,
      backfillSaves: 0,
      bodyMisses: 0,
      bodyTakedowns: 0,
    })),
    indexerMonth: [],
    providerDays: [],
    indexerDays: [],
    costs: [...costs],
  };
}

const PURPOSES = ["feed", "availability", "playback", "warmup", "rescue", "caps", "test"];
const SHARE = [0.42, 0.24, 0.14, 0.1, 0.05, 0.03, 0.02];

export function indexerUsage() {
  const window = days(14);
  const today = window[window.length - 1]!;
  const monthStart = today.slice(0, 8) + "01";
  const usage = (name: string, scale: number, limit: number, grabsLimit: number) => {
    const daily = window.map((day, i) => ({
      day,
      hits: Math.round(scale * load(day, i, window.length, `u${name}`)),
      grabs: Math.round((scale / 18) * noise(`ug${name}${day}`)),
    }));
    const total = (from: number) => {
      const slice = daily.slice(from);
      const hits = slice.reduce((n, d) => n + d.hits, 0);
      const grabs = slice.reduce((n, d) => n + d.grabs, 0);
      return {
        hits,
        grabs,
        purposes: PURPOSES.map((purpose, i) => ({
          purpose,
          hits: Math.round(hits * SHARE[i]!),
          grabs: purpose === "playback" ? grabs : 0,
        })),
      };
    };
    return {
      id: name.toLowerCase().replace(/\s+/g, "-"),
      name,
      enabled: true,
      torrent: name !== "Indexer One" && name !== "Indexer Two",
      dailyLimit: limit,
      dailyGrabs: grabsLimit,
      ...(name === "Indexer Two" ? { retryAt: Math.floor(Date.now() / 1000) + 3 * 3600 } : {}),
      today: total(13),
      week: total(7),
      month: total(Math.max(0, window.indexOf(monthStart))),
      days: daily,
    };
  };
  return {
    today,
    weekStart: window[7],
    monthStart,
    indexers: [
      usage("Indexer One", 150, 300, 50),
      usage("Indexer Two", 25, 25, 10),
      usage("Torrent Tracker", 60, 0, 0),
      usage("Stream Addon", 90, 0, 0),
    ],
  };
}
