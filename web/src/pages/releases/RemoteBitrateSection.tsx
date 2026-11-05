import { SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { useMemo, useState } from "react";
import { useApp } from "../../app-context";
import { NumberInput } from "../../components/fields";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { useAsync } from "../../lib/hooks";

type Draft = { capMbps: number };

/**
 * The away-from-home bitrate cap. Unlike the Away profile, this applies to
 * both movies and shows, so it lives with the shared settings.
 */
export function RemoteBitrateSection() {
  const { refreshDashboard } = useApp();
  const config = useAsync(() => api.getConfig(), []);
  const kbps = config.data?.transcode?.remoteMaxBitrateKbps;
  const saved = useMemo<Draft | null>(
    () => (kbps === undefined ? null : { capMbps: Math.round((kbps ?? 0) / 100) / 10 }),
    [kbps],
  );
  const { draft, setDraft, dirty, reset } = useDraft(saved);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  if (!draft || !saved) return null;
  async function submit() {
    if (!draft || !saved) return;
    setSaving(true);
    setError(null);
    try {
      if (draft.capMbps !== saved.capMbps)
        await api.putSection("transcode", {
          remoteMaxBitrateKbps: Math.round(Math.max(0, draft.capMbps) * 1000),
        });
      toast.success(t("Settings saved"), { description: t("Remote bitrate limit") });
      await config.reload();
      void refreshDashboard();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  }
  return (
    <SettingsSection
      title={t("Remote bitrate limit")}
      description={t("Caps the stream for people away from home. 0 means no limit.")}
    >
      <SettingsRow
        title={
          <label htmlFor="away-bitrate-cap" data-setting="bitrate-limit">
            {t("Bitrate limit")}
          </label>
        }
        control={
          <span className="flex items-center gap-2">
            <NumberInput
              id="away-bitrate-cap"
              className="w-24"
              min={0}
              step={0.5}
              value={draft.capMbps}
              onChange={(capMbps) => setDraft({ ...draft, capMbps })}
            />
            <span className="text-sm text-fg-muted">{t("Mbps")}</span>
          </span>
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
