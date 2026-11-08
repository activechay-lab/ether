import { EmptyState, PagedTable, cn } from "@braedonsaunders/appkit-ui";
import { Database } from "lucide-react";
import { t, tn } from "../../../i18n";
import { pagedTableLabels } from "../../../i18n/appkit-labels";
import { fixed, formatNumber, formatPercent } from "../../../lib/format";
import { KIND_LABEL, type Health, type SourceRow } from "./data";

export const HEALTH_DOT: Record<Health, string> = {
  ok: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
  off: "bg-fg-subtle",
};

/** A source's state as a coloured dot and words, so colour is never the only cue. */
export function HealthLabel({ health, label }: { health: Health; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap text-sm">
      <span aria-hidden className={cn("size-2 rounded-full", HEALTH_DOT[health])} />
      <span className={health === "danger" ? "text-danger" : "text-fg"}>{label}</span>
    </span>
  );
}

/** Daily activity as tiny columns, scaled to the row's own busiest day. */
function Activity({ values }: { values: number[] }) {
  if (!values.length) return <span className="text-fg-subtle">—</span>;
  const max = Math.max(...values);
  if (max <= 0) return <span className="text-xs text-fg-subtle">{t("No activity")}</span>;
  return (
    <span className="flex h-6 w-28 items-end gap-px" aria-hidden>
      {values.map((v, i) => (
        <span
          key={i}
          className={cn("min-w-px flex-1 rounded-[1px]", v > 0 ? "bg-primary/70" : "bg-border")}
          style={{ height: `${v > 0 ? Math.max(10, (v / max) * 100) : 6}%` }}
        />
      ))}
    </span>
  );
}

const money = (monthly: number, currency: string) =>
  t("{amount} {currency}/mo", { amount: fixed(monthly, 2), currency });

/** Plan totals across every priced source, one per currency. */
function PlanTotals({ rows, plays }: { rows: SourceRow[]; plays: number }) {
  const totals = new Map<string, number>();
  for (const r of rows) {
    const p = r.cost?.price;
    if (p && p.monthly > 0) totals.set(p.currency, (totals.get(p.currency) ?? 0) + p.monthly);
  }
  return (
    <p className="text-sm text-fg-muted">
      {totals.size
        ? [...totals]
            .map(([currency, total]) =>
              plays
                ? t("Plans total {amount} {currency} a month · {perPlay} per play this month", {
                    amount: fixed(total, 2),
                    currency,
                    perPlay: fixed(total / plays, 2),
                  })
                : t("Plans total {amount} {currency} a month", {
                    amount: fixed(total, 2),
                    currency,
                  }),
            )
            .join(" · ")
        : t("Open a source to add its plan price and see what streaming costs.")}
    </p>
  );
}

/** Statistics › Usage: every source in one table; a row opens its detail. */
export function SourceUsageTable({
  rows,
  monthPlays,
  days,
  onOpen,
}: {
  rows: SourceRow[];
  monthPlays: number;
  days: number;
  onOpen: (row: SourceRow) => void;
}) {
  return (
    <>
      <PlanTotals rows={rows} plays={monthPlays} />
      <PagedTable<SourceRow>
        rows={rows}
        rowKey={(r) => r.key}
        pageSize={25}
        onRowClick={onOpen}
        defaultSort={{ key: "source", dir: "asc" }}
        labels={pagedTableLabels({
          searchLabel: t("Search sources"),
          searchPlaceholder: t("Find a source…"),
        })}
        empty={
          <EmptyState
            icon={<Database />}
            title={t("No sources yet")}
            description={t("Add a Usenet provider, indexer, debrid account or IPTV source first.")}
          />
        }
        columns={[
          {
            key: "source",
            header: t("Source"),
            search: (r) => `${r.name} ${KIND_LABEL[r.kind]} ${r.detail}`,
            sortValue: (r) => r.order,
            cell: (r) => (
              <>
                <p className="max-w-56 truncate font-medium text-fg">{r.name}</p>
                <p className="max-w-56 truncate text-xs text-fg-muted">
                  {r.detail && r.detail !== r.name
                    ? t("{kind} · {detail}", { kind: KIND_LABEL[r.kind], detail: r.detail })
                    : KIND_LABEL[r.kind]}
                </p>
              </>
            ),
          },
          {
            key: "health",
            header: t("Status"),
            sortValue: (r) => ["danger", "warning", "off", "ok"].indexOf(r.health),
            cell: (r) => <HealthLabel health={r.health} label={r.healthLabel} />,
          },
          {
            key: "activity",
            header: tn(days, "Last {count} day", "Last {count} days"),
            cell: (r) => <Activity values={r.series} />,
          },
          {
            key: "usage",
            header: t("Usage"),
            align: "right",
            sortValue: (r) => r.usageSort,
            cell: (r) => (
              <span className="whitespace-nowrap tabular-nums text-fg">{r.usage || "—"}</span>
            ),
          },
          {
            key: "success",
            header: t("Success"),
            align: "right",
            sortValue: (r) => r.success ?? -1,
            cell: (r) => (
              <span
                className={cn(
                  "tabular-nums",
                  r.success != null && r.success < 0.8 ? "text-warning" : "text-fg",
                )}
              >
                {r.success == null ? "—" : formatPercent(r.success, 0)}
              </span>
            ),
          },
          {
            key: "latency",
            header: t("Response time"),
            align: "right",
            sortValue: (r) => r.latencyMs ?? -1,
            cell: (r) => (
              <span className="whitespace-nowrap tabular-nums">
                {r.latencyMs == null ? "—" : t("{ms} ms", { ms: formatNumber(r.latencyMs, 0) })}
              </span>
            ),
          },
          {
            key: "plan",
            header: t("Plan"),
            align: "right",
            sortValue: (r) => r.cost?.price?.monthly ?? -1,
            cell: (r) =>
              r.cost?.price && r.cost.price.monthly > 0 ? (
                <span className="whitespace-nowrap tabular-nums">
                  {money(r.cost.price.monthly, r.cost.price.currency)}
                </span>
              ) : (
                <span className="text-fg-subtle">—</span>
              ),
          },
        ]}
      />
    </>
  );
}
