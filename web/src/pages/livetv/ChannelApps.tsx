import { Switch } from "@braedonsaunders/appkit-ui";
import { t } from "../../i18n";

/** The apps Ether lists Live TV channels in (livetv.Apps on the server). */
export const LIVE_APPS = ["plex", "jellyfin", "emby", "silo", "xtream", "m3u"] as const;
export type LiveApp = (typeof LIVE_APPS)[number];

export function liveAppName(app: LiveApp): string {
  switch (app) {
    case "plex":
      return "Plex";
    case "jellyfin":
      return "Jellyfin";
    case "emby":
      return "Emby";
    case "silo":
      return "Silo";
    case "xtream":
      return t("IPTV apps (Xtream)");
    case "m3u":
      return t("M3U & XMLTV");
  }
}

/** Per-channel app visibility, shown on the channel drawer's Stream (or Details) tab. */
export function ChannelApps({
  hidden,
  onChange,
}: {
  hidden?: string[];
  onChange: (hidden: string[] | undefined) => void;
}) {
  const off = new Set(hidden ?? []);
  const toggle = (app: LiveApp, show: boolean) => {
    const next = new Set(off);
    if (show) next.delete(app);
    else next.add(app);
    onChange(next.size ? LIVE_APPS.filter((a) => next.has(a)) : undefined);
  };
  return (
    <fieldset className="space-y-2">
      <legend className="text-sm font-medium text-fg">{t("Show in apps")}</legend>
      <p className="text-xs text-fg-muted">
        {t(
          "Turn an app off to leave this channel out of its lineup. Viewer access still applies in every app.",
        )}
      </p>
      <div className="grid gap-2 pt-1 sm:grid-cols-2">
        {LIVE_APPS.map((app) => (
          <label
            key={app}
            className="flex items-center justify-between gap-3 rounded-xl border border-border bg-surface px-3 py-2.5 text-sm font-medium text-fg"
          >
            {liveAppName(app)}
            <Switch
              aria-label={t("Show in {app}", { app: liveAppName(app) })}
              checked={!off.has(app)}
              onChange={(e) => toggle(app, e.currentTarget.checked)}
            />
          </label>
        ))}
      </div>
    </fieldset>
  );
}
