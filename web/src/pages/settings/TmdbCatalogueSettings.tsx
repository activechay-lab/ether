import { SettingsSection } from "@braedonsaunders/appkit-ui";
import { MoreOptions } from "../../components/ConfigSection";
import { Segmented } from "../../components/fields";
import { SettingsRow } from "../../components/SettingsRow";
import { t } from "../../i18n";
import { CATALOG } from "./schema";
import { SchemaField, type FieldGroup } from "./SchemaForm";
import { CatalogPickers, isCatalogPicker } from "../libraries/browser/CatalogPickers";
import { UpcomingEpisodeSettings } from "../libraries/browser/HomeReleaseSettings";
import "../libraries/browser/catalog.css";
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

/** Off / Some / Most for catalog.minPopularity + catalog.minVotes. Some is the default. */
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

export function TmdbCatalogueSettings({
  draft,
  onChange,
  errors,
}: {
  draft: Obj;
  onChange: (value: Obj) => void;
  errors: Record<string, string>;
}) {
  const groups = [...CATALOG, UpcomingEpisodeSettings].map(briefGroup);
  return (
    <div className="min-w-0 space-y-5">
      {groups.map((g, i) => (
        <SettingsSection key={g.id ?? i} title={g.title}>
          {g.id === "browsable" ? <ObscureTitles draft={draft} onChange={onChange} /> : null}
          {g.fields.map((f, j) =>
            f.kind === "group" || isCatalogPicker(f.key) ? null : (
              <SchemaField
                key={`${f.key}-${j}`}
                def={f}
                value={draft[f.key]}
                path={`catalog.${f.key}`}
                errors={errors}
                onChange={(v) => onChange({ ...draft, [f.key]: v })}
              />
            ),
          )}
          {g.fields.some((f) => isCatalogPicker(f.key)) ? (
            <CatalogPickers
              language={draft.language}
              region={draft.region}
              refreshInterval={draft.refreshInterval}
              onChange={(key, value) => onChange({ ...draft, [key]: value })}
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
                  errors={errors}
                  onChange={(v) => onChange({ ...draft, [f.key]: v })}
                />
              ))}
            </MoreOptions>
          ) : null}
        </SettingsSection>
      ))}
    </div>
  );
}
