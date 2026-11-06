import { Switch } from "@braedonsaunders/appkit-ui";
import { t } from "../../../i18n";
import { SettingsRow } from "../../../components/SettingsRow";
import { ScreenPicker } from "./ScreenPicker";
import type { Look, StyleScreens } from "./model";
export function StyleScreensEditor({
  look,
  onChange,
}: {
  look: Look;
  onChange: (l: Look) => void;
}) {
  const screens = look.screens ?? { offAir: "card", noSignal: "card", stationId: true };
  const set = (patch: Partial<StyleScreens>) =>
    onChange({ ...look, screens: { ...screens, ...patch } });
  return (
    <section className="space-y-5">
      <h3 className="font-semibold">{t("Screens for {name}", { name: look.name })}</h3>
      <ScreenPicker
        label={t("When a channel is off air")}
        value={screens.offAir}
        onChange={(offAir) => set({ offAir })}
        look={look}
        station={screens.stationId ? look.name : ""}
      />
      <ScreenPicker
        label={t("When a channel can't play")}
        value={screens.noSignal}
        onChange={(noSignal) => set({ noSignal })}
        look={look}
        station={screens.stationId ? look.name : ""}
        allowError
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
    </section>
  );
}
