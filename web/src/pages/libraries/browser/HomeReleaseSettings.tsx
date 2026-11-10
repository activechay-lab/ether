import { useAsync, useInterval } from "../../../lib/hooks";
import { localized, t } from "../../../i18n";
import { apiFetch } from "../../../lib/api";
import { Activity, CheckCheck, Plus } from "lucide-react";
import { formatNumber } from "../../../lib/format";
import type { FieldGroup } from "../../settings/SchemaForm";

export const UpcomingEpisodeSettings: FieldGroup = localized(() => ({
  title: t("Upcoming content"),
  fields: [
    {
      key: "showUpcomingMovies",
      label: t("Show upcoming movies"),
      kind: "bool",
      help: t("List movies before their release date."),
    },
    {
      key: "showUpcomingEpisodes",
      label: t("Show upcoming episodes"),
      kind: "bool",
      help: t("List upcoming episodes in seasons that are already airing."),
    },
  ],
}));

type CatalogActivityStatus = {
  lastSync?: string;
  sync?: {
    running?: boolean;
    titlesAdded?: number;
    titlesSeen?: number;
    finishedAt?: string;
    lastError?: string;
  };
};

export function CatalogActivity() {
  const data = useAsync(() => apiFetch<{ value: CatalogActivityStatus }>("/status/catalog"), []);
  useInterval(() => void data.reload(), 60000);
  const status = data.data?.value;
  if (!status) return null;
  const sync = status.sync;
  const completed = sync?.finishedAt ?? status.lastSync;
  const hasSync = sync?.running || (completed && !completed.startsWith("0001-"));
  return (
    <section
      className="rounded-xl border border-border bg-surface p-5"
      aria-label={t("Recent activity")}
    >
      <h3 className="flex items-center gap-2 text-sm font-semibold">
        <Activity size={18} aria-hidden />
        {t("Recent activity")}
      </h3>
      {hasSync ? (
        <>
          <dl className="mt-3 space-y-3 text-sm">
            <div className="flex items-center gap-2">
              <Plus size={16} className="text-fg-muted" aria-hidden />
              <dt className="flex-1">
                {sync?.running ? t("Added in this sync") : t("Added in last sync")}
              </dt>
              <dd className="font-semibold tabular-nums">
                {sync?.titlesAdded == null ? "—" : formatNumber(sync.titlesAdded)}
              </dd>
            </div>
            <div className="flex items-center gap-2">
              <CheckCheck size={16} className="text-fg-muted" aria-hidden />
              <dt className="flex-1">
                {sync?.running ? t("Checked in this sync") : t("Checked in last sync")}
              </dt>
              <dd className="font-semibold tabular-nums">
                {sync?.titlesSeen == null ? "—" : formatNumber(sync.titlesSeen)}
              </dd>
            </div>
          </dl>
          {sync?.running ? <p className="mt-3 text-xs text-fg-muted">{t("Syncing now")}</p> : null}
        </>
      ) : (
        <p className="mt-3 text-sm text-fg-muted">{t("No sync activity yet")}</p>
      )}
      {sync?.lastError ? (
        <p className="mt-2 text-xs text-danger">{t("Last sync needs attention")}</p>
      ) : null}
    </section>
  );
}
