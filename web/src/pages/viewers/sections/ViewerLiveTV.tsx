import { Input, Select, SettingsSection, Spinner, Switch, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { MoreOptions } from "../../../components/ConfigSection";
import { ErrorPanel } from "../../../components/feedback";
import { SaveBar, useDraft } from "../../../components/SaveBar";
import { SettingsRow } from "../../../components/SettingsRow";
import { CONTENT_RATINGS } from "../../../components/UserLimits";
import { t } from "../../../i18n";
import { apiFetch } from "../../../lib/api";
import { canonicalRating } from "../../../lib/contentRatings";
import { useAsync } from "../../../lib/hooks";

type Policy = {
  userId: number;
  disabled: boolean;
  transcode?: boolean;
  /** Only `channels` (none when empty); otherwise every channel, new ones too. */
  restricted: boolean;
  channels?: string[];
  /** Private channels given to an unrestricted viewer. */
  granted?: string[];
  favorites: string[];
  maxRating: string;
  personal?: { enabled: boolean | null; name: string };
};
/** One viewer's Live TV access: channels and extra parental limits (viewer profile › Access). */
export function ViewerLiveTVAccess({ viewerId }: { viewerId: number }) {
  const policy = useAsync(
    (signal) => apiFetch<Policy>(`/livetv/policies/${viewerId}`, { signal }),
    [viewerId],
  );
  const { draft, setDraft, dirty, reset } = useDraft(policy.data);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  if (policy.error) return <ErrorPanel error={policy.error} onRetry={() => void policy.reload()} />;
  if (!draft)
    return (
      <div className="py-6 text-center">
        <Spinner size={18} label={t("Loading Live TV access")} />
      </div>
    );

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      await apiFetch(`/livetv/policies/${viewerId}`, {
        method: "PUT",
        body: JSON.stringify(draft),
      });
      policy.setData(draft);
      toast.success(t("Live TV access saved"));
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <SettingsSection
        title={t("Live TV")}
        description={t(
          "This viewer's existing rating and library limits also apply to the guide and every channel.",
        )}
      >
        <SettingsRow
          title={
            <label htmlFor={`livetv-off-${viewerId}`}>{t("Disable Live TV for this viewer")}</label>
          }
          description={t("Hides Live TV and the guide in their apps.")}
          control={
            <Switch
              id={`livetv-off-${viewerId}`}
              aria-label={t("Disable Live TV for this viewer")}
              checked={draft.disabled}
              onChange={(e) => setDraft({ ...draft, disabled: e.currentTarget.checked })}
            />
          }
        />
        <SettingsRow
          title={t("Transcode for compatibility")}
          description={t("Transcode IPTV codecs for client compatibility")}
          control={
            <Switch
              aria-label={t("Transcode for compatibility")}
              disabled={draft.disabled}
              checked={draft.transcode ?? false}
              onChange={(e) => setDraft({ ...draft, transcode: e.currentTarget.checked })}
            />
          }
        />
        <SettingsRow
          title={t("Personal channel")}
          control={
            <Switch
              aria-label={t("Personal channel")}
              disabled={draft.disabled}
              checked={draft.personal?.enabled ?? !draft.disabled}
              onChange={(e) =>
                setDraft({
                  ...draft,
                  personal: { name: draft.personal?.name || "", enabled: e.currentTarget.checked },
                })
              }
            />
          }
        />
        <MoreOptions
          label={t("More options: {title}", { title: t("Live TV") })}
          hasError={Boolean(draft.maxRating)}
        >
          <SettingsRow
            title={t("Name")}
            control={
              <Input
                aria-label={t("Personal channel name")}
                placeholder={t("{viewer} TV")}
                value={draft.personal?.name || ""}
                onChange={(e) =>
                  setDraft({
                    ...draft,
                    personal: {
                      enabled: draft.personal?.enabled ?? null,
                      name: e.currentTarget.value,
                    },
                  })
                }
              />
            }
          />
          <SettingsRow
            title={
              <label htmlFor={`livetv-rating-${viewerId}`}>
                {t("Different limit for Live TV")}
              </label>
            }
            description={t("A stricter rating limit for Live TV only.")}
            control={
              <Select
                id={`livetv-rating-${viewerId}`}
                className="sm:w-56"
                value={canonicalRating(draft.maxRating || "")}
                disabled={draft.disabled}
                onChange={(e) => setDraft({ ...draft, maxRating: e.currentTarget.value })}
              >
                {CONTENT_RATINGS.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.value ? r.label : t("Same as the content rating limit")}
                  </option>
                ))}
              </Select>
            }
          />
        </MoreOptions>
      </SettingsSection>
      <SaveBar
        dirty={dirty}
        saving={saving}
        error={error}
        onSave={save}
        onDiscard={() => {
          reset();
          setError(null);
        }}
      />
    </>
  );
}
