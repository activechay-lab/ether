import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { Badge, Button, Input, toast } from "@braedonsaunders/appkit-ui";
import { ArrowUpRight } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { StackedColumns, TimeBars } from "../../../components/charts";
import { Drawer } from "../../../components/Drawer";
import { LoadingRows } from "../../../components/feedback";
import { NumberInput, Segmented } from "../../../components/fields";
import { getLocale, t, tn } from "../../../i18n";
import { apiFetch, errorMessage } from "../../../lib/api";
import {
  fixed,
  formatDateTime,
  formatNumber,
  formatPercent,
  formatThroughput,
  relativeTime,
} from "../../../lib/format";
import { Fact } from "../../activity/ExperienceDrawer";
import { ProviderAccountCard } from "../../livetv/SourceGroups";
import { IndexerUsageDetails, USAGE_PERIODS, type UsagePeriod } from "../IndexerUsage";
import {
  KIND_LABEL,
  daily,
  dayLabel,
  formatGB,
  longDay,
  periodLabel,
  periodOptions,
  ratio,
  sum,
  useDrawerHistory,
  usenetProviders,
  windowDays,
  type Period,
  type SourceHistory,
  type SourceRow,
  type StatsData,
} from "./data";
import { HealthLabel } from "./UsageTable";

const DASH = "—";
const pct = (v: number | null) => (v == null ? DASH : formatPercent(v, 0));
const ms = (v: number | null | undefined) =>
  v == null || !Number.isFinite(v) || v <= 0 ? DASH : t("{ms} ms", { ms: formatNumber(v, 0) });
/** Go durations arrive in nanoseconds. */
const nsMs = (ns: number | undefined) => ms(ns ? ns / 1e6 : undefined);
const count = (n: number) => formatNumber(n, 0);

function Facts({ children }: { children: ReactNode }) {
  return <dl className="grid grid-cols-2 gap-x-4 gap-y-4 sm:grid-cols-3">{children}</dl>;
}

function Block({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-3">
      <h3 className="text-xs font-semibold uppercase tracking-wider text-fg-subtle">{title}</h3>
      {children}
    </section>
  );
}

function LastError({ message, at }: { message?: string; at?: string }) {
  if (!message) return null;
  return (
    <p
      role="status"
      className="rounded-lg border border-danger/30 bg-danger-subtle px-3 py-2 text-sm"
    >
      <span className="font-medium text-danger">
        {at ? t("Last error {when}", { when: relativeTime(at) }) : t("Last error")}
      </span>
      <span className="mt-0.5 block break-words text-fg">{message}</span>
    </p>
  );
}

/** Monthly plan price for one source, saved from its drawer. */
function PlanPrice({
  row,
  detail,
  onSaved,
}: {
  row: SourceRow;
  detail?: (monthly: number, currency: string) => string | undefined;
  onSaved: () => Promise<void> | void;
}) {
  const saved = row.cost?.price;
  const [monthly, setMonthly] = useState(saved?.monthly ?? 0);
  const [currency, setCurrency] = useState(saved?.currency ?? "USD");
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    setMonthly(saved?.monthly ?? 0);
    setCurrency(saved?.currency ?? "USD");
  }, [saved?.monthly, saved?.currency, row.key]);
  if (!row.cost) return null;
  const { kind, id } = row.cost;
  const dirty = monthly !== (saved?.monthly ?? 0) || currency !== (saved?.currency ?? "USD");
  const valid = /^[A-Z]{3}$/.test(currency) && monthly >= 0;
  const save = async () => {
    setSaving(true);
    try {
      await apiFetch("/stats/costs", {
        method: "PUT",
        body: JSON.stringify({ kind, id, monthly, currency }),
      });
      toast.success(t("Plan price saved"));
      await onSaved();
    } catch (e) {
      toast.error(t("Couldn't save the plan price"), { description: errorMessage(e) });
    } finally {
      setSaving(false);
    }
  };
  const note = monthly > 0 ? detail?.(monthly, currency) : undefined;
  return (
    <Block title={t("Plan price")}>
      <div className="flex flex-wrap items-center gap-2">
        <NumberInput
          aria-label={t("Monthly price for {name}", { name: row.name })}
          className="w-28"
          min={0}
          step={0.01}
          value={monthly}
          onChange={setMonthly}
        />
        <Input
          aria-label={t("Currency for {name}", { name: row.name })}
          className="w-20 uppercase"
          maxLength={3}
          value={currency}
          onChange={(e) => setCurrency(e.target.value.toUpperCase())}
        />
        <span className="text-sm text-fg-muted">{t("/ month")}</span>
        <Button
          size="sm"
          className="ml-auto"
          disabled={!dirty || !valid || saving}
          onClick={() => void save()}
        >
          {saving ? t("Saving…") : t("Save price")}
        </Button>
      </div>
      <p className="text-xs text-fg-muted">
        {note ??
          t(
            "Enter a monthly equivalent for annual or block plans. Totals appear on the Usage table.",
          )}
      </p>
    </Block>
  );
}

/** One total: the period's value over the all-time value where Ether has it. */
type Total = { label: string; value: string; lifetime?: string; hint?: string };

/** The period's totals, each with its all-time figure underneath. */
function Totals({ h, items, since }: { h: SourceHistory; items: Total[]; since?: string }) {
  return (
    <Block title={t("Totals · {period}", { period: periodLabel(h.days) })}>
      <dl className="grid grid-cols-2 gap-2 sm:grid-cols-3">
        {items.map((i) => (
          <div
            key={i.label}
            className="min-w-0 rounded-lg border border-border bg-bg-subtle px-3 py-2.5"
          >
            <dt className="truncate text-xs text-fg-muted">{i.label}</dt>
            <dd className="mt-0.5 truncate text-lg font-semibold tabular-nums text-fg">
              {i.value}
            </dd>
            {i.lifetime != null ? (
              <dd className="truncate text-xs tabular-nums text-fg-subtle">
                {t("All time {value}", { value: i.lifetime })}
              </dd>
            ) : i.hint ? (
              <dd className="text-xs leading-snug text-fg-subtle">{i.hint}</dd>
            ) : null}
          </div>
        ))}
      </dl>
      {since ? (
        <p className="text-xs text-fg-muted">
          {t("All time counts from {date}, the first day Ether recorded for this source.", {
            date: longDay(since),
          })}
        </p>
      ) : null}
    </Block>
  );
}

/** A daily chart only means something over more than one day. */
const multiDay = (h: SourceHistory) => h.days > 1;

function Quiet({ children }: { children: ReactNode }) {
  return <p className="py-4 text-center text-sm text-fg-muted">{children}</p>;
}

function UsenetDetail({ row, data, h }: { row: SourceRow; data: StatsData; h: SourceHistory }) {
  const days = windowDays(h);
  const provider = usenetProviders(data).find((x) => x.id === row.id);
  const p = provider?.sample;
  const all = h.providerDays ?? [];
  const mine = all.filter((d) => d.id === row.id);
  const bytes = sum(mine, (d) => d.bytes);
  const articles = sum(mine, (d) => d.articles);
  const misses = sum(mine, (d) => d.misses);
  const errors = sum(mine, (d) => d.errors);
  const bodyMisses = sum(mine, (d) => d.bodyMisses);
  const life = h.lifetime?.providers?.find((x) => x.id === row.id);
  const month = data.infra?.providerMonth?.find((x) => x.id === row.id);
  const at = data.infra?.snapshots.providers?.at;
  return (
    <>
      <Totals
        h={h}
        since={life?.firstDay}
        items={[
          {
            label: t("Downloaded"),
            value: formatGB(bytes),
            lifetime: life ? formatGB(life.bytes) : undefined,
          },
          {
            label: t("Articles"),
            value: count(articles),
            lifetime: life ? count(life.articles) : undefined,
          },
          {
            label: t("Article success"),
            value: pct(ratio(articles, articles + misses + errors)),
            lifetime: life
              ? pct(ratio(life.articles, life.articles + life.misses + life.errors))
              : undefined,
          },
          {
            label: t("Share of Usenet"),
            value: pct(
              ratio(
                bytes,
                sum(all, (d) => d.bytes),
              ),
            ),
            hint: t("Of all Usenet data in this period"),
          },
          {
            label: t("This month"),
            value: formatGB(month?.bytes ?? 0),
            hint: t("Calendar month, for plan limits"),
          },
        ]}
      />
      <Block title={t("Details")}>
        <Facts>
          <Fact
            label={t("Body availability")}
            hint={tn(
              sum(mine, (d) => d.bodyTakedowns),
              "{count} takedown (451)",
              "{count} takedowns (451)",
            )}
          >
            {pct(ratio(articles, articles + bodyMisses))}
          </Fact>
          <Fact label={t("Misses")}>{count(misses)}</Fact>
          <Fact label={t("Errors")}>{count(errors)}</Fact>
          <Fact label={t("Backfill saves")} hint={t("Articles saved after other providers missed")}>
            {count(sum(mine, (d) => d.backfillSaves))}
          </Fact>
          <Fact label={t("Spillovers")} hint={t("Taken over from busy providers")}>
            {count(sum(mine, (d) => d.spillovers))}
          </Fact>
        </Facts>
      </Block>
      {multiDay(h) ? (
        <>
          <DashboardPanel title={t("Downloaded each day (GB)")}>
            {bytes ? (
              <TimeBars
                ariaLabel={t("Gigabytes downloaded each day")}
                height={140}
                valueFormat={(v) => fixed(v, v > 0 && v < 10 ? 1 : 0)}
                data={days.map((day, i) => ({
                  key: day,
                  label: dayLabel(day),
                  value: daily(days, mine, (d) => d.bytes / 1e9)[i]!,
                }))}
              />
            ) : (
              <Quiet>{t("Nothing downloaded from this provider in this period.")}</Quiet>
            )}
          </DashboardPanel>
          {misses + errors ? (
            <DashboardPanel title={t("Misses and errors")}>
              <StackedColumns
                ariaLabel={t("Missing articles and errors each day")}
                aLabel={t("Misses")}
                bLabel={t("Errors")}
                height={140}
                valueFormat={count}
                data={days.map((day, i) => ({
                  key: day,
                  label: dayLabel(day),
                  a: daily(days, mine, (d) => d.misses)[i]!,
                  b: daily(days, mine, (d) => d.errors)[i]!,
                }))}
              />
            </DashboardPanel>
          ) : null}
        </>
      ) : null}
      {p ? (
        <Block
          title={
            at
              ? t("Right now · sampled {time}", { time: relativeTime(new Date(at * 1000)) })
              : t("Right now")
          }
        >
          <LastError message={p.lastError} at={p.lastErrorAt} />
          <Facts>
            <Fact
              label={t("Connections")}
              hint={
                p.limitLearned && p.serverLimit
                  ? t("The server allows {count}", { count: p.serverLimit })
                  : undefined
              }
            >
              {t("{open} open of {max}", {
                open: count(p.liveConnections ?? 0),
                max: count(p.maxConnections ?? p.configuredConnections ?? 0),
              })}
            </Fact>
            <Fact label={t("Busy now")}>{count(p.activeConnections ?? 0)}</Fact>
            <Fact label={t("Response time")}>{nsMs(p.avgLatency)}</Fact>
            <Fact label={t("Article download time")}>{nsMs(p.avgFetchTime)}</Fact>
            <Fact label={t("Connect time")} hint={t("Network, encryption and sign-in")}>
              {[nsMs(p.tcpDialTime), nsMs(p.tlsHandshakeTime), nsMs(p.authTime)].join(" · ")}
            </Fact>
            <Fact label={t("Hedged fetches")} hint={t("Duplicate fetches for a stalled article")}>
              {p.hedges
                ? t("{wins} of {count} won", {
                    wins: count(p.hedgeWins ?? 0),
                    count: count(p.hedges),
                  })
                : DASH}
            </Fact>
          </Facts>
        </Block>
      ) : (
        <p className="text-sm text-fg-muted">
          {provider && !provider.enabled
            ? t("This provider is turned off, so Ether is not connecting to it.")
            : t(
                "Ether has not connected to this provider yet. Live connection details appear once it is used.",
              )}
        </p>
      )}
    </>
  );
}

function IndexerDetail({ row, data, h }: { row: SourceRow; data: StatsData; h: SourceHistory }) {
  const [period, setPeriod] = useState<UsagePeriod>("today");
  const days = windowDays(h);
  const usage = data.usage?.indexers?.find((u) => u.name === row.id);
  const mine = (h.indexerDays ?? []).filter((d) => d.indexer === row.id);
  const searches = sum(mine, (d) => d.searches);
  const successes = sum(mine, (d) => d.successes);
  const grabs = sum(mine, (d) => d.grabs);
  const grabOk = sum(mine, (d) => d.grabSuccess);
  const ages = sum(mine, (d) => d.ageSamples);
  const limited = sum(mine, (d) => d.budgets);
  const life = h.lifetime?.indexers?.find((x) => x.indexer === row.id);
  return (
    <>
      {usage?.retryAt && usage.retryAt > Date.now() / 1000 ? (
        <p className="rounded-lg border border-warning/30 bg-warning-subtle px-3 py-2 text-sm text-fg">
          {t("Service limit reached · retries automatically after {time}", {
            time: new Date(usage.retryAt * 1000).toLocaleTimeString(getLocale()),
          })}
        </p>
      ) : null}
      <Totals
        h={h}
        since={life?.firstDay}
        items={[
          {
            label: t("Searches"),
            value: count(searches),
            lifetime: life ? count(life.searches) : undefined,
          },
          {
            label: t("Search success"),
            value: pct(ratio(successes, searches)),
            lifetime: life ? pct(ratio(life.successes, life.searches)) : undefined,
          },
          {
            label: t("Downloads"),
            value: count(grabs),
            lifetime: life ? count(life.grabs) : undefined,
          },
          {
            label: t("Grab success"),
            value: pct(ratio(grabOk, grabs)),
            lifetime: life ? pct(ratio(life.grabSuccess, life.grabs)) : undefined,
          },
          {
            label: t("Search speed"),
            value: ms(searches ? sum(mine, (d) => d.latencyMs) / searches : null),
            hint: t("Average response"),
          },
          {
            label: t("Limit reached"),
            value: count(limited),
            hint: t("Requests refused by the daily limit"),
          },
        ]}
      />
      <Block title={t("Details")}>
        <Facts>
          <Fact label={t("Average release age")}>
            {ages
              ? t("{days} days", { days: fixed(sum(mine, (d) => d.ageSeconds) / ages / 86400, 1) })
              : DASH}
          </Fact>
          <Fact label={t("Nothing found")}>{count(searches - successes)}</Fact>
          <Fact label={t("Failed downloads")}>{count(grabs - grabOk)}</Fact>
        </Facts>
      </Block>
      {usage ? (
        <Block title={t("API hits")}>
          <Segmented
            ariaLabel={t("Usage period")}
            size="sm"
            value={period}
            onChange={setPeriod}
            options={USAGE_PERIODS.map((p) => ({ value: p.value, label: p.label }))}
          />
          <IndexerUsageDetails usage={usage} period={period} />
        </Block>
      ) : null}
      {multiDay(h) ? (
        <>
          <DashboardPanel title={t("Searches")}>
            {searches ? (
              <StackedColumns
                ariaLabel={t("Searches each day, with and without results")}
                aLabel={t("Found releases")}
                bLabel={t("Nothing found")}
                height={140}
                valueFormat={count}
                data={days.map((day, i) => {
                  const ok = daily(days, mine, (d) => d.successes)[i]!;
                  return {
                    key: day,
                    label: dayLabel(day),
                    a: ok,
                    b: daily(days, mine, (d) => d.searches)[i]! - ok,
                  };
                })}
              />
            ) : (
              <Quiet>{t("No searches in this period.")}</Quiet>
            )}
          </DashboardPanel>
          {searches ? (
            <DashboardPanel title={t("Search speed (ms)")}>
              <TimeBars
                ariaLabel={t("Average search response each day")}
                height={120}
                valueFormat={count}
                data={days.map((day) => {
                  const d = mine.filter((x) => x.day === day);
                  const n = sum(d, (x) => x.searches);
                  return {
                    key: day,
                    label: dayLabel(day),
                    value: n ? sum(d, (x) => x.latencyMs) / n : 0,
                  };
                })}
              />
            </DashboardPanel>
          ) : null}
          {grabs ? (
            <DashboardPanel title={t("Downloads")}>
              <StackedColumns
                ariaLabel={t("Downloads sent each day, succeeded and failed")}
                aLabel={t("Succeeded")}
                bLabel={t("Failed")}
                height={120}
                valueFormat={count}
                data={days.map((day, i) => {
                  const ok = daily(days, mine, (d) => d.grabSuccess)[i]!;
                  return {
                    key: day,
                    label: dayLabel(day),
                    a: ok,
                    b: daily(days, mine, (d) => d.grabs)[i]! - ok,
                  };
                })}
              />
            </DashboardPanel>
          ) : null}
        </>
      ) : null}
      {usage?.days?.length ? (
        <DashboardPanel title={t("API hits each day")}>
          <TimeBars
            ariaLabel={t("API hits each day")}
            height={120}
            valueFormat={count}
            data={usage.days.map((d) => ({
              key: d.day,
              label: dayLabel(d.day),
              value: d.hits,
              detail: tn(d.grabs, "{count} download", "{count} downloads"),
            }))}
          />
        </DashboardPanel>
      ) : null}
    </>
  );
}

function DebridDetail({ row, data, h }: { row: SourceRow; data: StatsData; h: SourceHistory }) {
  const days = windowDays(h);
  const p = data.debrid?.providers?.find((x) => x.id === row.id);
  const mine = (h.debridDays ?? []).filter((d) => d.id === row.id);
  // Plays record the account they streamed from by its name.
  const plays = (h.plays ?? []).filter((x) => x.origin === row.name);
  const requests = sum(mine, (d) => d.requests);
  const failures = sum(mine, (d) => d.failures);
  const cached = sum(mine, (d) => d.cached);
  const notCached = sum(mine, (d) => d.notCached);
  const bytes = sum(mine, (d) => d.bytes ?? 0);
  const playCount = sum(plays, (x) => x.plays);
  const life = h.lifetime?.debrid?.find((x) => x.id === row.id);
  const l = p?.latency;
  return (
    <>
      <Totals
        h={h}
        since={life?.firstDay}
        items={[
          {
            label: t("Data downloaded"),
            value: formatGB(bytes),
            lifetime: life ? formatGB(life.bytes) : undefined,
          },
          {
            label: t("Plays"),
            value: count(playCount),
            hint: playCount
              ? tn(
                  sum(plays, (x) => x.failed),
                  "{count} failed play",
                  "{count} failed plays",
                )
              : undefined,
          },
          {
            label: t("Requests"),
            value: count(requests),
            lifetime: life ? count(life.requests) : undefined,
          },
          {
            label: t("Success"),
            value: pct(ratio(requests - failures, requests)),
            lifetime: life ? pct(ratio(life.requests - life.failures, life.requests)) : undefined,
          },
          {
            label: t("Cached when checked"),
            value: pct(ratio(cached, cached + notCached)),
            lifetime: life ? pct(ratio(life.cached, life.cached + life.notCached)) : undefined,
          },
        ]}
      />
      <Block title={t("Details")}>
        <Facts>
          <Fact label={t("Failures")}>{count(failures)}</Fact>
          <Fact
            label={t("Cache checks")}
            hint={t("{cached} cached · {missing} not cached", {
              cached: count(cached),
              missing: count(notCached),
            })}
          >
            {count(cached + notCached)}
          </Fact>
          <Fact label={t("Refused files")} hint={t("Cached files the service would not serve")}>
            {count(sum(mine, (d) => d.blocked))}
          </Fact>
          <Fact label={t("Links refreshed")}>{count(sum(mine, (d) => d.linksRefreshed))}</Fact>
          <Fact label={t("Reused from the account")}>{count(sum(mine, (d) => d.reused))}</Fact>
          <Fact label={t("Failovers")}>{count(sum(plays, (x) => x.failovers))}</Fact>
        </Facts>
      </Block>
      {multiDay(h) ? (
        <>
          <DashboardPanel title={t("Downloaded each day (GB)")}>
            {bytes ? (
              <TimeBars
                ariaLabel={t("Gigabytes streamed from this account each day")}
                height={140}
                valueFormat={(v) => fixed(v, v > 0 && v < 10 ? 1 : 0)}
                data={days.map((day, i) => ({
                  key: day,
                  label: dayLabel(day),
                  value: daily(days, mine, (d) => (d.bytes ?? 0) / 1e9)[i]!,
                }))}
              />
            ) : (
              <Quiet>
                {t(
                  "No data recorded for this account in this period. Ether records it per account from version 0.3.6.",
                )}
              </Quiet>
            )}
          </DashboardPanel>
          {playCount ? (
            <DashboardPanel title={t("Plays each day")}>
              <StackedColumns
                ariaLabel={t("Plays from this account each day, played and failed")}
                aLabel={t("Played")}
                bLabel={t("Failed")}
                height={120}
                valueFormat={count}
                data={days.map((day, i) => {
                  const bad = daily(days, plays, (x) => x.failed)[i]!;
                  return {
                    key: day,
                    label: dayLabel(day),
                    a: daily(days, plays, (x) => x.plays)[i]! - bad,
                    b: bad,
                  };
                })}
              />
            </DashboardPanel>
          ) : null}
          <DashboardPanel title={t("Requests each day")}>
            {requests ? (
              <StackedColumns
                ariaLabel={t("Debrid requests each day, succeeded and failed")}
                aLabel={t("Succeeded")}
                bLabel={t("Failed")}
                height={120}
                valueFormat={count}
                data={days.map((day, i) => {
                  const n = daily(days, mine, (d) => d.requests)[i]!;
                  const bad = daily(days, mine, (d) => d.failures)[i]!;
                  return { key: day, label: dayLabel(day), a: n - bad, b: bad };
                })}
              />
            ) : (
              <Quiet>{t("No requests recorded in this period.")}</Quiet>
            )}
          </DashboardPanel>
          {cached + notCached ? (
            <DashboardPanel title={t("Cache checks each day")}>
              <StackedColumns
                ariaLabel={t("Cache checks each day, cached and not cached")}
                aLabel={t("Cached")}
                bLabel={t("Not cached")}
                height={120}
                valueFormat={count}
                data={days.map((day, i) => ({
                  key: day,
                  label: dayLabel(day),
                  a: daily(days, mine, (d) => d.cached)[i]!,
                  b: daily(days, mine, (d) => d.notCached)[i]!,
                }))}
              />
            </DashboardPanel>
          ) : null}
        </>
      ) : null}
      {p ? (
        <Block title={t("Right now")}>
          <LastError message={p.lastError} />
          <Facts>
            <Fact label={t("Last success")}>
              {p.lastSuccess ? relativeTime(p.lastSuccess) : DASH}
            </Fact>
            <Fact
              label={t("API calls at once")}
              hint={
                p.accountSlots && p.slots !== p.accountSlots
                  ? t("This server's share of {count}", { count: p.accountSlots })
                  : undefined
              }
            >
              {p.slots ? count(p.slots) : DASH}
            </Fact>
            {p.backoffUntil ? (
              <Fact label={t("Waiting until")}>{formatDateTime(p.backoffUntil)}</Fact>
            ) : null}
            <Fact label={t("Finding a file")}>{ms(l?.resolveMs)}</Fact>
            <Fact label={t("Making a link")}>{ms(l?.linkMs)}</Fact>
            <Fact label={t("Connect time")}>{ms(l?.connectMs)}</Fact>
            <Fact label={t("First byte")}>{ms(l?.ttfbMs)}</Fact>
            <Fact label={t("Download speed")}>
              {l?.throughputBps ? formatThroughput(l.throughputBps) : DASH}
            </Fact>
            <Fact label={t("Since Ether started")}>
              {tn(p.requests ?? 0, "{count} request", "{count} requests")}
            </Fact>
          </Facts>
        </Block>
      ) : null}
    </>
  );
}

function IptvDetail({ row, data }: { row: SourceRow; data: StatsData }) {
  const s = data.iptv?.find((x) => x.id === row.id);
  if (!s) return null;
  return (
    <>
      {s.account ? <ProviderAccountCard account={s.account} /> : null}
      <LastError message={s.lastError} />
      <Block title={t("Lineup")}>
        <Facts>
          <Fact label={t("Channels")}>{count(s.channelCount ?? 0)}</Fact>
          <Fact label={t("With a guide")}>{count(s.guideChannelCount ?? 0)}</Fact>
          <Fact label={t("Programmes")}>{count(s.programmeCount ?? 0)}</Fact>
          <Fact label={t("Guide until")}>{s.guideUntil ? formatDateTime(s.guideUntil) : DASH}</Fact>
          <Fact label={t("Last refreshed")}>{relativeTime(s.updatedAt)}</Fact>
          <Fact label={t("Next refresh")}>
            {s.nextRefresh ? relativeTime(s.nextRefresh) : DASH}
          </Fact>
          <Fact label={t("Failed refreshes")}>{count(s.refreshFailures ?? 0)}</Fact>
          {s.account ? (
            <Fact label={t("Connections in use")}>
              {s.account.maxConnections
                ? t("{used} of {max}", {
                    used: count(s.account.activeConnections),
                    max: count(s.account.maxConnections),
                  })
                : count(s.account.activeConnections)}
            </Fact>
          ) : null}
          {s.skippedVod ? (
            <Fact label={t("Movies and series left out")}>{count(s.skippedVod)}</Fact>
          ) : null}
        </Facts>
      </Block>
      <Button asChild variant="outline" size="sm">
        <Link to={`/sources/iptv?source=${encodeURIComponent(s.id)}`}>
          {t("Open source settings")} <ArrowUpRight size={14} />
        </Link>
      </Button>
    </>
  );
}

function PluginDetail({ row }: { row: SourceRow }) {
  return (
    <>
      <Block title={t("Declared limits")}>
        <p className="text-sm text-fg">{row.usage}</p>
        <p className="text-xs text-fg-muted">
          {t("Plugins report their own limits. Ether does not record usage for them yet.")}
        </p>
      </Block>
      <Button asChild variant="outline" size="sm">
        <Link to="/plugins">
          {t("Open Plugins")} <ArrowUpRight size={14} />
        </Link>
      </Button>
    </>
  );
}

/** Everything Ether records about one source, opened from the Usage table. */
export function SourceDrawer({
  row,
  data,
  days,
  onClose,
  onSaved,
}: {
  row: SourceRow | null;
  data: StatsData | undefined;
  /** The page's period; the drawer starts there and can pick its own. */
  days: Period;
  onClose: () => void;
  onSaved: () => Promise<void> | void;
}) {
  const [period, setPeriod] = useState<Period>(days);
  useEffect(() => setPeriod(days), [days, row?.key]);
  const h = useDrawerHistory(data?.history, period);
  const month = data?.infra?.providerMonth?.find((p) => p.id === row?.id);
  const plays = data?.infra?.monthPlays ?? 0;
  const backfill =
    row?.kind === "usenet" && data
      ? usenetProviders(data).find((p) => p.id === row.id)?.sample?.backfill
      : false;
  const timed = row ? row.kind !== "iptv" && row.kind !== "plugin" : false;
  return (
    <Drawer
      open={row != null}
      onClose={onClose}
      size="lg"
      title={row?.name ?? t("Source")}
      description={
        row
          ? row.detail && row.detail !== row.name
            ? t("{kind} · {detail}", { kind: KIND_LABEL[row.kind], detail: row.detail })
            : KIND_LABEL[row.kind]
          : undefined
      }
    >
      {row && data ? (
        <div className="space-y-6">
          <div className="flex flex-wrap items-center gap-2">
            <HealthLabel health={row.health} label={row.healthLabel} />
            {backfill ? <Badge variant="secondary">{t("Backfill")}</Badge> : null}
            {timed ? (
              <Segmented
                className="ml-auto"
                ariaLabel={t("Period for {name}", { name: row.name })}
                size="sm"
                value={period}
                onChange={setPeriod}
                options={periodOptions()}
              />
            ) : null}
          </div>
          {timed && !h ? <LoadingRows rows={3} /> : null}
          {row.kind === "usenet" && h ? <UsenetDetail row={row} data={data} h={h} /> : null}
          {row.kind === "indexer" && h ? <IndexerDetail row={row} data={data} h={h} /> : null}
          {row.kind === "debrid" && h ? <DebridDetail row={row} data={data} h={h} /> : null}
          {row.kind === "iptv" ? <IptvDetail row={row} data={data} /> : null}
          {row.kind === "plugin" ? <PluginDetail row={row} /> : null}
          <PlanPrice
            row={row}
            onSaved={onSaved}
            detail={(monthly, currency) =>
              row.kind === "usenet" && month && month.bytes > 0
                ? t("{amount} {currency}/GB observed", {
                    amount: fixed(monthly / (month.bytes / 1073741824), 2),
                    currency,
                  })
                : plays
                  ? t("{amount} {currency} per play this month", {
                      amount: fixed(monthly / plays, 2),
                      currency,
                    })
                  : undefined
            }
          />
        </div>
      ) : null}
    </Drawer>
  );
}
