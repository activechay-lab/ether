import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { Badge, Button, Input, toast } from "@braedonsaunders/appkit-ui";
import { ArrowUpRight } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { StackedColumns, TimeBars } from "../../../components/charts";
import { Drawer } from "../../../components/Drawer";
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
  ratio,
  sum,
  windowDays,
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

function UsenetDetail({ row, data }: { row: SourceRow; data: StatsData }) {
  const h = data.history;
  const days = windowDays(h);
  const p = data.infra?.snapshots.providers?.data?.find((x) => (x.id || x.host) === row.id);
  const mine = (h.providerDays ?? []).filter((d) => d.id === row.id);
  const bytes = sum(mine, (d) => d.bytes);
  const articles = sum(mine, (d) => d.articles);
  const misses = sum(mine, (d) => d.misses);
  const errors = sum(mine, (d) => d.errors);
  const bodyMisses = sum(mine, (d) => d.bodyMisses);
  const at = data.infra?.snapshots.providers?.at;
  return (
    <>
      <Block title={tn(h.days, "Last {count} day", "Last {count} days")}>
        <Facts>
          <Fact label={t("Downloaded")}>{formatGB(bytes)}</Fact>
          <Fact label={t("Articles")}>{count(articles)}</Fact>
          <Fact label={t("Article success")}>
            {pct(ratio(articles, articles + misses + errors))}
          </Fact>
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
      <DashboardPanel title={t("Downloaded each day (GB)")}>
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
      ) : null}
    </>
  );
}

function IndexerDetail({ row, data }: { row: SourceRow; data: StatsData }) {
  const [period, setPeriod] = useState<UsagePeriod>("today");
  const h = data.history;
  const days = windowDays(h);
  const usage = data.usage?.indexers?.find((u) => u.name === row.id);
  const mine = (h.indexerDays ?? []).filter((d) => d.indexer === row.id);
  const searches = sum(mine, (d) => d.searches);
  const grabs = sum(mine, (d) => d.grabs);
  const ages = sum(mine, (d) => d.ageSamples);
  const limited = sum(mine, (d) => d.budgets);
  return (
    <>
      {usage?.retryAt && usage.retryAt > Date.now() / 1000 ? (
        <p className="rounded-lg border border-warning/30 bg-warning-subtle px-3 py-2 text-sm text-fg">
          {t("Service limit reached · retries automatically after {time}", {
            time: new Date(usage.retryAt * 1000).toLocaleTimeString(getLocale()),
          })}
        </p>
      ) : null}
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
      <Block title={tn(h.days, "Last {count} day", "Last {count} days")}>
        <Facts>
          <Fact label={t("Searches")}>{count(searches)}</Fact>
          <Fact label={t("Search success")}>
            {pct(
              ratio(
                sum(mine, (d) => d.successes),
                searches,
              ),
            )}
          </Fact>
          <Fact label={t("Search speed")}>
            {ms(searches ? sum(mine, (d) => d.latencyMs) / searches : null)}
          </Fact>
          <Fact label={t("Downloads")}>{count(grabs)}</Fact>
          <Fact label={t("Grab success")}>
            {pct(
              ratio(
                sum(mine, (d) => d.grabSuccess),
                grabs,
              ),
            )}
          </Fact>
          <Fact label={t("Average release age")}>
            {ages
              ? t("{days} days", { days: fixed(sum(mine, (d) => d.ageSeconds) / ages / 86400, 1) })
              : DASH}
          </Fact>
          <Fact label={t("Limit reached")} hint={t("Requests refused by the daily limit")}>
            {count(limited)}
          </Fact>
        </Facts>
      </Block>
      {usage?.days?.length ? (
        <DashboardPanel title={t("API hits each day")}>
          <TimeBars
            ariaLabel={t("API hits each day")}
            height={140}
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
      {searches ? (
        <DashboardPanel title={t("Searches")}>
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
        </DashboardPanel>
      ) : null}
    </>
  );
}

function DebridDetail({ row, data }: { row: SourceRow; data: StatsData }) {
  const h = data.history;
  const days = windowDays(h);
  const p = data.debrid?.providers?.find((x) => x.id === row.id);
  const mine = (h.debridDays ?? []).filter((d) => d.id === row.id);
  const requests = sum(mine, (d) => d.requests);
  const failures = sum(mine, (d) => d.failures);
  const cached = sum(mine, (d) => d.cached);
  const notCached = sum(mine, (d) => d.notCached);
  const l = p?.latency;
  return (
    <>
      <Block title={tn(h.days, "Last {count} day", "Last {count} days")}>
        <Facts>
          <Fact label={t("Requests")}>{count(requests)}</Fact>
          <Fact label={t("Failures")}>{count(failures)}</Fact>
          <Fact label={t("Success")}>{pct(ratio(requests - failures, requests))}</Fact>
          <Fact
            label={t("Cached when checked")}
            hint={t("{cached} cached · {missing} not cached", {
              cached: count(cached),
              missing: count(notCached),
            })}
          >
            {pct(ratio(cached, cached + notCached))}
          </Fact>
          <Fact label={t("Refused files")} hint={t("Cached files the service would not serve")}>
            {count(sum(mine, (d) => d.blocked))}
          </Fact>
          <Fact label={t("Links refreshed")}>{count(sum(mine, (d) => d.linksRefreshed))}</Fact>
          <Fact label={t("Reused from the account")}>{count(sum(mine, (d) => d.reused))}</Fact>
        </Facts>
      </Block>
      {requests ? (
        <DashboardPanel title={t("Requests each day")}>
          <StackedColumns
            ariaLabel={t("Debrid requests each day, succeeded and failed")}
            aLabel={t("Succeeded")}
            bLabel={t("Failed")}
            height={140}
            valueFormat={count}
            data={days.map((day, i) => {
              const n = daily(days, mine, (d) => d.requests)[i]!;
              const bad = daily(days, mine, (d) => d.failures)[i]!;
              return { key: day, label: dayLabel(day), a: n - bad, b: bad };
            })}
          />
        </DashboardPanel>
      ) : (
        <p className="text-sm text-fg-muted">{t("No requests recorded in this period.")}</p>
      )}
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
  onClose,
  onSaved,
}: {
  row: SourceRow | null;
  data: StatsData | undefined;
  onClose: () => void;
  onSaved: () => Promise<void> | void;
}) {
  const month = data?.infra?.providerMonth?.find((p) => p.id === row?.id);
  const plays = data?.infra?.monthPlays ?? 0;
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
            {row.kind === "usenet" &&
            data.infra?.snapshots.providers?.data?.find((p) => (p.id || p.host) === row.id)
              ?.backfill ? (
              <Badge variant="secondary">{t("Backfill")}</Badge>
            ) : null}
          </div>
          {row.kind === "usenet" ? <UsenetDetail row={row} data={data} /> : null}
          {row.kind === "indexer" ? <IndexerDetail row={row} data={data} /> : null}
          {row.kind === "debrid" ? <DebridDetail row={row} data={data} /> : null}
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
