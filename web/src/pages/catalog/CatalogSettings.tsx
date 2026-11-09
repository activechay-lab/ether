import { SubtabNav } from "../../components/layout";
import { CheckCircle2, AlertTriangle } from "lucide-react";
import { SettingsSection, Spinner, toast } from "@braedonsaunders/appkit-ui";
import { Segmented } from "../../components/fields";
import { MoreOptions } from "../../components/ConfigSection";
import { useNavigate, useParams } from "react-router-dom";
import { useCallback, useMemo, useState } from "react";
import { useApp } from "../../app-context";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { ErrorPanel } from "../../components/feedback";
import { t, tn } from "../../i18n";
import { relativeTime } from "../../lib/format";
import { api, apiFetch, ApiError, errorMessage } from "../../lib/api";
import { useAsync, useDebouncedValue } from "../../lib/hooks";
import { isRuleComplete } from "../../lib/rules";
import { asObj, SchemaField, type FieldGroup } from "../settings/SchemaForm";
import { CatalogPickers, isCatalogPicker } from "./CatalogPickers";
import { CATALOG } from "../settings/schema";
import { CatalogActivity, UpcomingEpisodeSettings } from "./HomeReleaseSettings";
import { WatchlistAlerts } from "../viewers/sections/WatchlistDefaults";
import { NewReleasesSettings, useAvailabilityDraft } from "./NewReleasesSettings";
import { RulesPanel, type VisibilityRule } from "./LibraryVisibility";
import { TrendingStatus } from "./TrendingStatus";
import { TierMigrationStatus } from "./TierMigrationStatus";
import { LevelBadge } from "../../components/status";
import { SettingsRow } from "../../components/SettingsRow";
import "./catalog.css";

type Obj = Record<string, unknown>;

function briefGroup(g: FieldGroup): FieldGroup {
  return {
    ...g,
    description: undefined,
    fields: g.fields.map((f) =>
      "help" in f && typeof f.help === "string" && f.help.length > 90
        ? { ...f, help: undefined }
        : f,
    ),
  };
}

/** Off / Some / Most for catalog.minPopularity + catalog.minVotes. Some is the default. Off (0/0)
 * is the server's floor, votes >= 10 or popularity >= 2.5 (config.Catalog.Bars). */
const OBSCURE_PRESETS = {
  off: { minPopularity: 0, minVotes: 0 },
  some: { minPopularity: 5, minVotes: 100 },
  most: { minPopularity: 15, minVotes: 500 },
} as const;
type ObscurePreset = keyof typeof OBSCURE_PRESETS;

function obscurePreset(draft: Obj): ObscurePreset | "custom" {
  const pop = Number(draft.minPopularity ?? 0);
  const votes = Number(draft.minVotes ?? 0);
  for (const [key, v] of Object.entries(OBSCURE_PRESETS))
    if (v.minPopularity === pop && v.minVotes === votes) return key as ObscurePreset;
  return "custom";
}

function ObscureTitles({ draft, onChange }: { draft: Obj; onChange: (next: Obj) => void }) {
  const preset = obscurePreset(draft);
  return (
    <SettingsRow
      setting="hide-obscure-titles"
      title={t("Hide obscure titles")}
      description={
        preset === "custom"
          ? t("Custom limits are set under More options.")
          : preset === "off"
            ? t(
                "Browse lists every title with a real audience. Everything else is still found by search.",
              )
            : t("Hidden titles are still found by search.")
      }
      control={
        <Segmented
          ariaLabel={t("Hide obscure titles")}
          value={preset === "custom" ? "" : preset}
          onChange={(v) => onChange({ ...draft, ...OBSCURE_PRESETS[v as ObscurePreset] })}
          options={[
            { value: "off", label: t("Off") },
            { value: "some", label: t("Some") },
            { value: "most", label: t("Most") },
          ]}
        />
      }
    />
  );
}

export function CatalogSettings() {
  const { tab } = useParams();
  const navigate = useNavigate();
  const section = tab === "rules" ? "rules" : "library";
  const { health, dashboard, refreshDashboard } = useApp();
  const catalogStatus = asObj(dashboard?.sections.catalog);
  const titleCount =
    Number(catalogStatus.browsableMovies ?? catalogStatus.movies ?? 0) +
    Number(catalogStatus.browsableShows ?? catalogStatus.shows ?? 0);
  const catalogHealth = health.find((h) => h.id === "catalog");
  const config = useAsync(() => api.getConfig(), []);
  const stored = config.data ? asObj(config.data.catalog) : null;
  const { draft, setDraft, dirty, reset } = useDraft<Obj>(stored);
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const rules = (Array.isArray(draft?.exclusions) ? draft.exclusions : []) as VisibilityRule[];
  const fallback = draft?.visibilityDefault === "hide" ? "hide" : "show";
  const rulesReady = rules.every((g) => g.rules.length > 0 && g.rules.every(isRuleComplete));
  const visibilitySpec = useMemo(
    () => ({ exclusions: rules, visibilityDefault: fallback }),
    [draft],
  );
  const visibilityDraft = useDebouncedValue(visibilitySpec, 400);
  const visibility = useAsync(
    async (signal) => {
      if (section !== "library" || !rulesReady) return null;
      return apiFetch<{ total: number; visible: number; hidden: number }>(
        "/catalog/visibility/preview",
        {
          method: "POST",
          body: JSON.stringify(visibilityDraft),
          signal,
        },
      );
    },
    [visibilityDraft, section, rulesReady],
  );

  const save = useCallback(async () => {
    if (!draft) return;
    setSaving(true);
    setError(null);
    try {
      await api.putSection("catalog", draft);
      toast.success(t("Settings saved"));
      await config.reload();
      void refreshDashboard();
    } catch (e) {
      setError(e);
      if (!(e instanceof ApiError)) toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  }, [config, draft, refreshDashboard]);
  const availability = useAvailabilityDraft();
  const groups = [...CATALOG, UpcomingEpisodeSettings].map(briefGroup);
  return (
    <div className="catalog-scroll app-scroll">
      <div className="space-y-4 px-5 pb-5 pt-3 lg:px-7">
        <div className="pb-1">
          <SubtabNav
            ariaLabel={t("Catalog settings")}
            active={section}
            onSelect={(key) => navigate(key === "rules" ? "/catalog/rules" : "/catalog/settings")}
            tabs={[
              { key: "library", label: t("General") },
              { key: "rules", label: t("Rules") },
            ]}
          />
        </div>
        {config.error ? (
          <ErrorPanel error={config.error} onRetry={() => void config.reload()} />
        ) : null}
        {!draft ? (
          <div className="py-10 text-center">
            <Spinner size={20} label={t("Loading settings")} />
          </div>
        ) : section === "rules" ? (
          <>
            <p className="text-sm text-fg-muted">
              {t("Global rules are the default for every viewer. Set exceptions in Viewers.")}
            </p>
            <RulesPanel
              rules={rules}
              fallback={fallback}
              region={String(draft.region ?? "")}
              onRules={(next) => setDraft({ ...draft, exclusions: next })}
              onFallback={(value) => setDraft({ ...draft, visibilityDefault: value })}
            />
          </>
        ) : (
          <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_300px]">
            <div className="space-y-5">
              {availability.draft ? (
                <NewReleasesSettings
                  availability={availability.draft}
                  onAvailability={availability.setDraft}
                />
              ) : null}
              {groups.map((g, i) => (
                <SettingsSection key={g.id ?? i} title={g.title}>
                  {g.id === "browsable" ? (
                    <ObscureTitles draft={draft} onChange={setDraft} />
                  ) : null}
                  {g.fields.map((f, j) =>
                    f.kind === "group" || isCatalogPicker(f.key) ? null : (
                      <SchemaField
                        key={`${f.key}-${j}`}
                        def={f}
                        value={draft[f.key]}
                        path={`catalog.${f.key}`}
                        errors={{}}
                        onChange={(v) => setDraft({ ...draft, [f.key]: v })}
                      />
                    ),
                  )}
                  {g.fields.some((f) => isCatalogPicker(f.key)) ? (
                    <CatalogPickers
                      language={draft.language}
                      region={draft.region}
                      refreshInterval={draft.refreshInterval}
                      onChange={(key, value) => setDraft({ ...draft, [key]: value })}
                    />
                  ) : null}
                  {g.more?.length ? (
                    <MoreOptions
                      setting={g.id}
                      label={t("More options: {title}", { title: g.title ?? "" })}
                    >
                      {g.more.map((f, j) => (
                        <SchemaField
                          key={`more-${f.key}-${j}`}
                          def={f}
                          value={draft[f.key]}
                          path={`catalog.${f.key}`}
                          errors={{}}
                          onChange={(v) => setDraft({ ...draft, [f.key]: v })}
                        />
                      ))}
                    </MoreOptions>
                  ) : null}
                </SettingsSection>
              ))}
              <WatchlistAlerts />
            </div>
            <aside className="space-y-5">
              {catalogHealth ? (
                <section
                  className="rounded-xl border border-border bg-surface p-5"
                  aria-label={t("Your catalog")}
                >
                  <div className="mb-4 flex items-center gap-3">
                    {catalogHealth.level === "ok" ? (
                      <CheckCircle2 size={36} className="text-success" />
                    ) : (
                      <AlertTriangle size={36} className="text-warning" />
                    )}
                    <h3 className="text-lg font-semibold">{t("Your catalog")}</h3>
                  </div>
                  <p className="text-xs text-fg-muted">{t("Catalog size before rules")}</p>
                  <p className="mt-1 text-3xl font-semibold tabular-nums">
                    {tn(visibility.data?.total ?? titleCount, "{count} title", "{count} titles")}
                  </p>
                  <p className="mt-2 text-sm">
                    {visibility.data
                      ? tn(
                          visibility.data.visible ?? visibility.data.total - visibility.data.hidden,
                          "{count} shown after your rules",
                          "{count} shown after your rules",
                        )
                      : t("Checking your rules…")}
                  </p>
                  <p className="mt-2 text-sm text-fg-muted">
                    {catalogStatus.lastSync
                      ? t("Synced {when}", { when: relativeTime(String(catalogStatus.lastSync)) })
                      : t("Not synced yet")}
                  </p>
                  <div className="mt-4">
                    <LevelBadge level={catalogHealth.level} />
                  </div>
                  {catalogHealth.level === "warn" ? (
                    <p className="mt-3 break-words text-sm text-warning">{catalogHealth.summary}</p>
                  ) : null}
                </section>
              ) : null}
              <TierMigrationStatus />
              <CatalogActivity />
              <TrendingStatus />
            </aside>
          </div>
        )}
        <SaveBar
          dirty={dirty}
          saving={saving}
          error={error}
          disabled={dirty && !rulesReady}
          onSave={() => void save()}
          onDiscard={() => {
            reset();
            setError(null);
          }}
        />
        {availability.saveBar}
      </div>
    </div>
  );
}
