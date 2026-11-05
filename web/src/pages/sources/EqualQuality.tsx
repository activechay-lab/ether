import { QualityHelp } from "../releases/QualityHelp";
import { SearchSelect, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { useMemo, useState } from "react";
import { ErrorPanel } from "../../components/feedback";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import { localized, t, tn } from "../../i18n";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { api } from "../../lib/api";
import { formatNanos } from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import { useResolver } from "../releases/resolverDraft";

const SOURCE_PREFERENCE = localized(() => [
  { value: "auto", label: t("Start fastest") },
  { value: "usenet", label: t("Usenet first") },
  { value: "debrid", label: t("Debrid first") },
  { value: "usenet-always", label: t("Always Usenet") },
  { value: "debrid-always", label: t("Always cached debrid") },
]);

/** The select value for resolver.sourcePreference + sourceAlwaysFirst. */
function sourceOrderValue(r?: { sourcePreference?: string; sourceAlwaysFirst?: boolean }): string {
  const pref = r?.sourcePreference || "auto";
  return r?.sourceAlwaysFirst && (pref === "usenet" || pref === "debrid") ? `${pref}-always` : pref;
}

/** The resolver fields a select value stands for. */
function sourceOrderFields(value: string): {
  sourcePreference: string;
  sourceAlwaysFirst: boolean;
} {
  const always = value.endsWith("-always");
  return {
    sourcePreference: always ? value.slice(0, -"-always".length) : value,
    sourceAlwaysFirst: always,
  };
}

/** Measured startup per source, as the resolver sees it. */
function SourceStartupNote() {
  const startup = useAsync(() => api.sourceStartup().catch(() => []), []);
  const rows = startup.data ?? [];
  if (rows.length === 0) return <>{t("No plays measured yet.")}</>;
  return (
    <>
      {t("Measured so far: {list}.", {
        list: rows
          .map((r) =>
            tn(
              r.samples,
              "{source} {duration} ({count} play)",
              "{source} {duration} ({count} plays)",
              {
                source: r.protocol === "torrent" ? t("debrid") : "Usenet",
                duration: formatNanos(r.average),
              },
            ),
          )
          .join(", "),
      })}
    </>
  );
}

/**
 * Which kind of source leads, for every profile (Quality › Profiles). The
 * first three choices only decide between releases that are equal after the
 * profile's ranking ladder; the "Always" choices put that source first
 * whatever the other has (resolver.sourcePreference + sourceAlwaysFirst).
 */
export function SourceOrderSection() {
  const { config, resolver } = useResolver();
  const saved = useMemo(() => (resolver ? sourceOrderValue(resolver) : null), [resolver]);
  const { draft, setDraft, dirty, reset } = useDraft(saved);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  if (config.error) return <ErrorPanel error={config.error} onRetry={() => void config.reload()} />;
  if (draft === null) return null;
  async function submit() {
    if (draft === null) return;
    setSaving(true);
    setError(null);
    try {
      await api.putSection("resolver", sourceOrderFields(draft));
      toast.success(t("Source order saved"));
      await config.reload();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  }
  return (
    <SettingsSection title={t("Which source goes first")}>
      <SettingsRow
        setting="source-order"
        title={t("Source order")}
        description={
          <div className="space-y-1">
            <QualityHelp
              summary={t("Prefer speed or a source.")}
              detail={t(
                "Start fastest picks whichever source has been quicker to start. Usenet first and Debrid first only decide between equal releases. The Always choices prefer that source regardless of quality, with fallback when nothing plays.",
              )}
            />
            <span className="block text-xs text-fg-muted">
              <SourceStartupNote />
            </span>
          </div>
        }
        control={
          <div className="w-52">
            <SearchSelect
              {...searchSelectLabels()}
              value={draft || "auto"}
              onChange={setDraft}
              options={SOURCE_PREFERENCE}
              ariaLabel={t("Which source goes first")}
              searchable={false}
            />
          </div>
        }
      />
      <SaveBar
        dirty={dirty}
        saving={saving}
        error={error}
        onSave={submit}
        onDiscard={() => {
          reset();
          setError(null);
        }}
      />
    </SettingsSection>
  );
}
