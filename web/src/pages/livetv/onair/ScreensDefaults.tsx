import { Select, Switch, Tooltip } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { SaveBar, useDraft } from "../../../components/SaveBar";
import { SettingsRow } from "../../../components/SettingsRow";
import { ErrorPanel } from "../../../components/feedback";
import { api } from "../../../lib/api";
import { useAsync } from "../../../lib/hooks";
import { t } from "../../../i18n";
import { onair } from "./model";
import { ScreenPicker } from "./ScreenPicker";
export function ScreensDefaults() {
  const styles = useAsync(() => onair.looks(), []);
  const config = useAsync(() => api.getConfig(), []);
  const { draft, setDraft, dirty, reset } = useDraft(config.data);
  const [saving, setSaving] = useState(false),
    [error, setError] = useState<unknown>(null);
  if (config.error) return <ErrorPanel error={config.error} onRetry={() => void config.reload()} />;
  if (!draft) return null;
  const screens = draft.liveTV?.screens ?? {
    offAir: "card" as const,
    noSignal: "card" as const,
    stationId: true,
    applyTo: "converted" as "converted" | "all",
  };
  const set = (patch: Partial<typeof screens>) =>
    setDraft({ ...draft, liveTV: { ...draft.liveTV, screens: { ...screens, ...patch } } });
  return (
    <section className="space-y-5 rounded-xl border border-border bg-surface p-4">
      <h2 className="text-base font-semibold text-fg">{t("Default style")}</h2>
      <SettingsRow
        title={t("Applies to every channel without an override")}
        control={
          <Select
            aria-label={t("Default style")}
            value={draft.liveTV?.defaultStyleId ?? ""}
            onChange={(e) =>
              setDraft({
                ...draft,
                liveTV: { ...draft.liveTV, defaultStyleId: e.currentTarget.value },
              })
            }
          >
            <option value="">{t("No style")}</option>
            {styles.data?.map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
          </Select>
        }
      />
      <details>
        <summary className="cursor-pointer text-sm">
          {t("Fallback screens without a style")}
        </summary>
        <div className="grid gap-6 xl:grid-cols-2">
          <ScreenPicker
            label={t("When a channel is off air")}
            value={screens.offAir}
            onChange={(offAir) => set({ offAir })}
            station={screens.stationId ? draft.server.friendlyName : ""}
          />
          <ScreenPicker
            label={t("When a channel can't play")}
            value={screens.noSignal}
            onChange={(noSignal) => set({ noSignal })}
            allowError
            station={screens.stationId ? draft.server.friendlyName : ""}
          />
        </div>
        <SettingsRow
          title={
            <span>
              {t("Apply to")}{" "}
              <Tooltip
                content={t(
                  "Every stream adds video conversion for channels Ether would otherwise relay.",
                )}
              >
                <span tabIndex={0} aria-label={t("About stream conversion")}>
                  ⓘ
                </span>
              </Tooltip>
            </span>
          }
          control={
            <Select
              value={screens.applyTo ?? "converted"}
              onChange={(e) => set({ applyTo: e.currentTarget.value as "converted" | "all" })}
            >
              <option value="converted">{t("Streams Ether already converts")}</option>
              <option value="all">{t("Every stream")}</option>
            </Select>
          }
        />
        <SettingsRow
          title={t("Show station ID")}
          control={
            <Switch
              aria-label={t("Show station ID")}
              checked={screens.stationId}
              onChange={(e) => set({ stationId: e.currentTarget.checked })}
            />
          }
        />
      </details>
      <SaveBar
        dirty={dirty}
        saving={saving}
        error={error}
        onDiscard={reset}
        onSave={async () => {
          setSaving(true);
          setError(null);
          try {
            await api.putConfig(draft);
            config.setData(draft);
          } catch (e) {
            setError(e);
          } finally {
            setSaving(false);
          }
        }}
      />
    </section>
  );
}
