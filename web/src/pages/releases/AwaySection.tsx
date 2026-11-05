import { SearchSelect, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { useMemo, useState } from "react";
import { useApp } from "../../app-context";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import { t } from "../../i18n";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { api } from "../../lib/api";
import {
  MEDIA_MOVIE,
  compatibleProfiles,
  mediaApplies,
  mediaLabel,
  type ProfileMedia,
} from "../../lib/releases";
import type { Quality } from "./useQuality";

/** quality.remoteMovie/remoteTv, falling back to the legacy field when it fits. */
function awayProfile(quality: Quality, media: ProfileMedia): string {
  const scoped = media === MEDIA_MOVIE ? quality.remoteMovie : quality.remoteTv;
  if (scoped) return scoped;
  const legacy = quality.remote ?? "";
  if (!legacy) return "";
  const profile = (quality.profiles ?? []).find((p) => p.name === legacy);
  return !profile || mediaApplies(profile.mediaType, media) ? legacy : "";
}

/**
 * The Away profile for the selected media. The remote bitrate cap is shared
 * by both media and lives in RemoteBitrateSection.
 */
export function AwaySection({
  quality,
  media,
  onSaved,
}: {
  quality: Quality;
  media: ProfileMedia;
  onSaved: () => Promise<void>;
}) {
  const { refreshDashboard } = useApp();
  const saved = useMemo(() => awayProfile(quality, media), [quality, media]);
  const { draft, setDraft, dirty, reset } = useDraft(saved);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  if (draft === null) return null;
  const field = media === MEDIA_MOVIE ? "remoteMovie" : "remoteTv";
  const options = [
    { value: "", label: t("Same as at home") },
    ...compatibleProfiles(quality.profiles, media).map((p) => ({
      value: p.name,
      label: p.name,
    })),
  ];
  async function submit() {
    if (draft === null) return;
    setSaving(true);
    setError(null);
    try {
      if (draft !== saved) await api.putSection("quality", { [field]: draft });
      toast.success(t("Settings saved"), { description: t("Away from home") });
      await onSaved();
      void refreshDashboard();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  }
  return (
    <SettingsSection title={t("Away from home")}>
      <SettingsRow
        setting="away-profile"
        title={t("Away profile")}
        description={t(
          "Used away from home for {media}. Device and viewer rules still win.",
          { media: mediaLabel(media) },
        )}
        control={
          <div className="w-52">
            <SearchSelect
              {...searchSelectLabels()}
              value={draft}
              onChange={setDraft}
              options={options}
              ariaLabel={t("Away profile")}
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
