import { sourceImportError } from "./sourceErrors";
import { DashboardPanel } from "@braedonsaunders/appkit-dashboard/primitives";
import { Badge, Button, cn } from "@braedonsaunders/appkit-ui";
import { AlertTriangle, KeyRound, ListVideo, Pencil, Plug } from "lucide-react";
import { useMemo } from "react";
import { Meter } from "../../components/charts";
import { Kpi, KpiRow } from "../../components/Kpi";
import { t, tn } from "../../i18n";
import { formatDateTime, formatNumber, formatPercent, relativeTime } from "../../lib/format";
import {
  MINUTE,
  isChannelSource,
  type ChannelSummary,
  type Coverage,
  type LiveUsage,
  type Source,
} from "./model";
import { StabilityPanel } from "./stability";

const DAY = 24 * 60 * MINUTE;

/**
 * The Sources tab's workspace: every channel source's health in one place
 * (account, connections, stream starts, refreshes, groups and listings).
 * Editing stays in the source drawer.
 */
export function SourcesDashboard({
  sources,
  channels,
  coverage,
  usage,
  selected,
  now,
  onEdit,
}: {
  sources: Source[];
  channels: ChannelSummary[];
  coverage: Map<string, Coverage> | null;
  usage: LiveUsage | null;
  selected: string | null;
  now: number;
  onEdit: (id: string) => void;
}) {
  const list = sources.filter(isChannelSource);
  const bySource = useMemo(() => {
    const m = new Map<string, { on: number; listed: number }>();
    for (const c of channels) {
      if (c.kind !== "iptv" || !c.sourceId) continue;
      const v = m.get(c.sourceId) ?? { on: 0, listed: 0 };
      if (c.enabled) {
        v.on++;
        if ((coverage?.get(c.id)?.programmes ?? 0) > 0) v.listed++;
      }
      m.set(c.sourceId, v);
    }
    return m;
  }, [channels, coverage]);

  const total = list.reduce((n, s) => n + (s.channelCount ?? 0), 0);
  const failing = list.filter((s) => s.lastError).length;
  const expiring = list.filter((s) => {
    const at = s.account?.expiresAt ? Date.parse(s.account.expiresAt) : NaN;
    return Number.isFinite(at) && at - now < 3 * DAY;
  }).length;
  const capped = (usage?.sources ?? []).filter((u) => u.limit > 0);

  if (!list.length)
    return (
      <div className="grid h-full place-items-center p-10 text-center">
        <div className="max-w-sm space-y-2">
          <ListVideo className="mx-auto text-fg-subtle" size={28} />
          <p className="text-sm font-medium text-fg">{t("No channel sources yet")}</p>
          <p className="text-xs text-fg-muted">
            {t("Use Add to sign in to an IPTV provider or bring in an M3U playlist.")}
          </p>
        </div>
      </div>
    );

  return (
    <div className="space-y-6 p-4 sm:p-6">
      <KpiRow className="lg:grid-cols-2">
        <Kpi
          label={t("Sources")}
          value={formatNumber(list.length)}
          detail={tn(total, "{count} channel", "{count} channels")}
          icon={<ListVideo size={17} />}
        />
        <Kpi
          label={t("Connections in use")}
          value={
            capped.length
              ? t("{open} of {limit}", {
                  open: formatNumber(capped.reduce((n, u) => n + u.open, 0)),
                  limit: formatNumber(capped.reduce((n, u) => n + u.limit, 0)),
                })
              : usage
                ? t("No limit")
                : "—"
          }
          detail={t("Shared by everyone watching")}
          icon={<Plug size={17} />}
          tone="info"
        />
        <Kpi
          label={t("Needs attention")}
          value={formatNumber(
            list.filter(
              (s) =>
                s.lastError ||
                (s.account?.expiresAt && Date.parse(s.account.expiresAt) - now < 3 * DAY),
            ).length,
          )}
          detail={
            failing
              ? tn(failing, "{count} refresh failing", "{count} refreshes failing")
              : expiring
                ? tn(expiring, "{count} account expiring soon", "{count} accounts expiring soon")
                : t("All healthy")
          }
          icon={<AlertTriangle size={17} />}
          tone={failing ? "danger" : expiring ? "warning" : "success"}
        />
        <Kpi
          label={t("New streams this hour")}
          value={
            usage ? formatNumber(usage.sources.reduce((n, u) => n + u.startsLastHour, 0)) : "—"
          }
          detail={t("Shared by everyone watching")}
          icon={<KeyRound size={17} />}
        />
      </KpiRow>

      <StabilityPanel />

      {list.map((s) => (
        <SourcePanel
          key={s.id}
          source={s}
          coverageKnown={coverage !== null}
          stats={bySource.get(s.id) ?? { on: 0, listed: 0 }}
          usage={usage?.sources.find((u) => u.sourceId === s.id)}
          selected={selected === s.id}
          now={now}
          onEdit={onEdit}
        />
      ))}
    </div>
  );
}

function SourcePanel({
  source: s,
  stats,
  coverageKnown,
  usage,
  selected,
  now,
  onEdit,
}: {
  source: Source;
  stats: { on: number; listed: number };
  coverageKnown: boolean;
  usage?: LiveUsage["sources"][number];
  selected: boolean;
  now: number;
  onEdit: (id: string) => void;
}) {
  const groups = s.groups ?? [];
  const included = groups.filter((g) => !g.excluded).length;
  const expires = s.account?.expiresAt ? Date.parse(s.account.expiresAt) : NaN;
  const expiringSoon = Number.isFinite(expires) && expires - now < 3 * DAY;
  const listedPct = stats.on ? stats.listed / stats.on : 0;
  const status = s.lastError
    ? { label: t("Refresh failing"), variant: "destructive" as const }
    : s.account?.status && s.account.status.toLowerCase() !== "active"
      ? { label: s.account.status, variant: "destructive" as const }
      : { label: t("Healthy"), variant: "success" as const };
  return (
    <DashboardPanel
      className={cn(selected && "ring-2 ring-primary/40")}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{s.name}</span>
          {s.disabled ? <Badge>{t("Disabled")}</Badge> : null}
          <Badge variant={status.variant}>{status.label}</Badge>
          {s.auth?.type === "xtream" ? (
            <Badge variant="secondary">{t("Xtream Codes")}</Badge>
          ) : null}
          {s.account?.trial ? <Badge variant="info">{t("Trial")}</Badge> : null}
        </span>
      }
      actions={
        <Button size="sm" variant="outline" onClick={() => onEdit(s.id)}>
          <Pencil size={13} /> {t("Edit")}
        </Button>
      }
    >
      <div className="space-y-4">
        {s.lastError ? (
          <p
            role="alert"
            className="rounded-lg border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger"
          >
            {sourceImportError(s.lastError)}
          </p>
        ) : null}
        <dl className="grid gap-4 sm:grid-cols-2 2xl:grid-cols-4">
          <Fact
            label={t("Channels")}
            value={formatNumber(s.channelCount ?? 0)}
            detail={
              groups.length
                ? t("{included} of {total} groups included", {
                    included: formatNumber(included),
                    total: formatNumber(groups.length),
                  })
                : undefined
            }
          />
          <Fact
            label={t("Listings")}
            value={coverageKnown && stats.on ? formatPercent(listedPct) : "—"}
            detail={t("{covered} of {total} on air", {
              covered: formatNumber(stats.listed),
              total: formatNumber(stats.on),
            })}
          />
          <Fact
            label={t("Last refresh")}
            value={relativeTime(s.updatedAt)}
            detail={
              s.nextRefresh
                ? t("Next {when}", { when: relativeTime(s.nextRefresh) })
                : t("Refreshes only when you ask")
            }
          />
          <Fact
            label={t("Account")}
            value={
              Number.isFinite(expires)
                ? relativeTime(new Date(expires))
                : s.account?.status || t("No account details")
            }
            detail={Number.isFinite(expires) ? formatDateTime(new Date(expires)) : undefined}
            warn={expiringSoon}
          />
        </dl>
        {usage && (usage.limit > 0 || usage.perHour > 0) ? (
          <div className="grid gap-4 sm:grid-cols-2">
            {usage.limit > 0 ? (
              <Meter
                label={t("Connections in use")}
                valueLabel={t("{open} of {limit}", {
                  open: formatNumber(usage.open),
                  limit: formatNumber(usage.limit),
                })}
                value={usage.open / usage.limit}
                tone={usage.open >= usage.limit ? "warning" : "primary"}
              />
            ) : null}
            {usage.perHour > 0 ? (
              <Meter
                label={t("New streams this hour")}
                valueLabel={t("{open} of {limit}", {
                  open: formatNumber(usage.startsLastHour),
                  limit: formatNumber(usage.perHour),
                })}
                value={usage.startsLastHour / usage.perHour}
                tone={usage.startsLastHour >= usage.perHour ? "danger" : "primary"}
              />
            ) : (
              <p className="self-end text-xs text-fg-muted">
                {usage.gapSeconds > 0
                  ? tn(
                      usage.startsLastHour,
                      "{count} new stream this hour, at least {gap} seconds apart.",
                      "{count} new streams this hour, at least {gap} seconds apart.",
                      { gap: formatNumber(usage.gapSeconds) },
                    )
                  : tn(
                      usage.startsLastHour,
                      "{count} new stream this hour.",
                      "{count} new streams this hour.",
                    )}
              </p>
            )}
          </div>
        ) : null}
      </div>
    </DashboardPanel>
  );
}

function Fact({
  label,
  value,
  detail,
  warn,
}: {
  label: string;
  value: React.ReactNode;
  detail?: React.ReactNode;
  warn?: boolean;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">{label}</dt>
      <dd className={cn("mt-1 truncate text-lg font-semibold", warn ? "text-warning" : "text-fg")}>
        {value}
      </dd>
      {detail ? <dd className="truncate text-xs text-fg-muted">{detail}</dd> : null}
    </div>
  );
}
