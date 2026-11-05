import { QualityHelp } from "../releases/QualityHelp";
import { SearchSelect, SettingsSection, Switch } from "@braedonsaunders/appkit-ui";
import { useMemo, useState } from "react";
import { MoreOptions } from "../../components/ConfigSection";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import { localized, t } from "../../i18n";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { useResolver } from "../releases/resolverDraft";

const RACE = localized(() => [
  { value: "0", label: t("Ranked selection") },
  ...[2, 3, 4].map((n) => ({ value: String(n), label: t("First healthy of {n}", { n }) })),
]);

/** How soon playback starts (Quality › Searching). */
export function StartSpeed() {
  const { config, resolver, save } = useResolver();
  const saved = useMemo(
    () =>
      resolver
        ? {
            optimisticStart: resolver.optimisticStart ?? false,
            healthRace: resolver.healthRace ?? 0,
          }
        : null,
    [resolver],
  );
  const { draft, setDraft, dirty, reset } = useDraft(saved);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);

  if (config.error) return <ErrorPanel error={config.error} onRetry={() => void config.reload()} />;
  if (!resolver || !draft) return <LoadingRows rows={2} />;

  async function submit() {
    if (!draft) return;
    setSaving(true);
    setError(null);
    try {
      await save(draft, t("Start speed saved"));
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  }

  return (
    <SettingsSection title={t("Start speed")}>
      <SettingsRow
        setting="start-before-the-health-check-finishes"
        title={t("Start before the health check finishes")}
        description={
          <QualityHelp
            summary={t("Play the top release immediately.")}
            detail={t("Missing articles still trigger failover.")}
          />
        }
        control={
          <Switch
            checked={draft.optimisticStart}
            onChange={(e) => setDraft({ ...draft, optimisticStart: e.currentTarget.checked })}
            aria-label={t("Start the top release before its health check completes")}
          />
        }
      />
      <MoreOptions setting="start-speed" label={t("More options: Start speed")}>
        <SettingsRow
          setting="concurrent-checks"
          title={t("Concurrent checks")}
          description={
            <QualityHelp
              summary={t("Check several releases at once.")}
              detail={t("Uses extra NZB downloads. Detail-page warming has its own budget.")}
            />
          }
          control={
            <div className="w-48">
              <SearchSelect
                {...searchSelectLabels()}
                value={String(draft.healthRace)}
                onChange={(v) => setDraft({ ...draft, healthRace: Number(v) })}
                options={RACE}
                ariaLabel={t("Concurrent checks")}
                searchable={false}
              />
            </div>
          }
        />
      </MoreOptions>
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
