import { useEffect, useState } from "react";
import { Select, Switch } from "@braedonsaunders/appkit-ui";
import { SettingsRow } from "../../../components/SettingsRow";
import { t } from "../../../i18n";
import { onair, type Look, type Moment, type OnAirAsset } from "./model";

/** Place in W15's selected-moment inspector; changes join the look's save bar. */
export function MomentVideoPicker({
  look,
  moment,
  onChange,
}: {
  look: Look;
  moment: Moment;
  onChange: (look: Look) => void;
}) {
  const [assets, setAssets] = useState<OnAirAsset[]>([]);
  useEffect(() => {
    void onair.assets().then(setAssets);
  }, []);
  if (!["break", "back", "ident"].includes(moment)) return null;
  return (
    <div className="space-y-3">
      <SettingsRow
        title={t("Bumper video")}
        control={
          <Select
            aria-label={t("Bumper video")}
            value={look.moments[moment]?.assetDigest || ""}
            onChange={(e) =>
              onChange({
                ...look,
                moments: {
                  ...look.moments,
                  [moment]: {
                    duration: look.moments[moment]?.duration || 8,
                    poster: look.moments[moment]?.poster || 0,
                    ...look.moments[moment],
                    assetDigest: e.currentTarget.value || undefined,
                  },
                },
              })
            }
          >
            <option value="">{t("Scene")}</option>
            {assets
              .filter((a) => a.kind === "video" && (!a.status || a.status === "ready"))
              .map((a) => (
                <option key={a.digest} value={a.digest}>
                  {a.name}
                </option>
              ))}
          </Select>
        }
      />
      <SettingsRow
        title={t("Crossfade")}
        control={
          <Switch
            aria-label={t("Crossfade")}
            disabled={!look.moments[moment]?.assetDigest}
            checked={look.moments[moment]?.crossfade || false}
            onChange={(e) =>
              onChange({
                ...look,
                moments: {
                  ...look.moments,
                  [moment]: {
                    duration: look.moments[moment]?.duration || 8,
                    poster: look.moments[moment]?.poster || 0,
                    ...look.moments[moment],
                    crossfade: e.currentTarget.checked,
                  },
                },
              })
            }
          />
        }
      />
    </div>
  );
}
