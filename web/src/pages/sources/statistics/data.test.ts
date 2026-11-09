import { describe, expect, it } from "vitest";
import {
  indexerRequestDetail,
  indexerRequests,
  sourceRows,
  type IndexerDay,
  type StatsData,
} from "./data";

function makeDay(overrides: Partial<IndexerDay> = {}): IndexerDay {
  return {
    indexer: "DrunkenSlug",
    day: "2026-11-09",
    requests: 533,
    cacheHits: 276,
    searches: 465,
    successes: 312,
    grabs: 20,
    grabSuccess: 20,
    budgets: 612,
    latencyMs: 93_000,
    ageSeconds: 0,
    ageSamples: 0,
    ...overrides,
  };
}

describe("indexer statistics helpers", () => {
  it("uses recorded HTTP API requests including feed polls, with a legacy fallback", () => {
    // 465 searches + 48 feed polls + 20 grabs = 533 real HTTP hits; cacheHits & budgets excluded.
    expect(indexerRequests(makeDay())).toBe(533);

    // Pre-0.3.7 imported or legacy row without requests falls back to searches + grabs.
    expect(indexerRequests(makeDay({ requests: undefined }))).toBe(485);
    expect(indexerRequests(makeDay({ requests: 0 }))).toBe(485);
  });

  it("formats cache answers and daily-limit holds in the headline detail", () => {
    expect(indexerRequestDetail(1284, 612)).toBe(
      "plus 1,284 answered from Ether's cache, 612 held back by your daily limit",
    );
  });

  it("aligns sourceRows usage with real API requests and Search success with found / searches", () => {
    const data: StatsData = {
      history: {
        days: 2,
        since: "2026-11-08",
        until: "2026-11-09",
        plays: null,
        providerDays: null,
        indexerDays: [
          makeDay({
            day: "2026-11-08",
            requests: 100,
            cacheHits: 400,
            searches: 80,
            successes: 60,
            grabs: 10,
            budgets: 500,
            latencyMs: 16_000,
          }),
          makeDay({
            day: "2026-11-09",
            requests: 533,
            cacheHits: 276,
            searches: 420,
            successes: 290,
            grabs: 20,
            budgets: 612,
            latencyMs: 84_000,
          }),
        ],
        debridDays: null,
        streamDays: null,
      },
      infra: null,
      usage: {
        today: "2026-11-09",
        weekStart: "2026-11-03",
        monthStart: "2026-11-01",
        indexers: [
          {
            id: "drunkenslug",
            name: "DrunkenSlug",
            enabled: true,
            torrent: false,
            dailyLimit: 1000,
            dailyGrabs: 100,
            today: { hits: 513, grabs: 20, purposes: null },
            week: { hits: 603, grabs: 30, purposes: null },
            month: { hits: 603, grabs: 30, purposes: null },
            days: null,
          },
        ],
      },
      debrid: null,
      iptv: null,
      usenet: null,
    };

    const rows = sourceRows(data, []);
    expect(rows).toHaveLength(1);
    const row = rows[0]!;
    expect(row.usageSort).toBe(633);
    expect(row.usage).toBe("633 API requests");
    expect(row.series).toEqual([100, 533]);
    // Search success = (60 + 290) / (80 + 420) = 350 / 500 = 0.7 (never diluted by budgets or cache).
    expect(row.success).toBeCloseTo(0.7, 5);
    expect(row.latencyMs).toBeCloseTo(200, 5);
  });
});
