import { t } from "../../../i18n";
import type { PluginDetail } from "../types";

/** What a plugin can add to Ether's own surfaces, most visible first. */
const CONTRIBUTIONS = [
  "livetv_channels",
  "list_source",
  "stream_source",
  "debrid_provider",
  "subtitles",
  "metadata",
  "plugin_directory",
] as const;

export type Contribution = (typeof CONTRIBUTIONS)[number];

function capabilitiesOf(detail: PluginDetail): string[] {
  return detail.capabilities?.length
    ? detail.capabilities
    : (detail.manifest.ether?.capabilities ?? []);
}

export function contributionsOf(detail: PluginDetail): Contribution[] {
  const caps = new Set(capabilitiesOf(detail));
  for (const r of detail.roles ?? []) caps.add(r.role);
  return CONTRIBUTIONS.filter((c) => caps.has(c));
}

/** The plugin's main contribution, which names its own drawer tab. */
export function primaryContribution(detail: PluginDetail): Contribution | undefined {
  return contributionsOf(detail)[0];
}

export function contributionTab(c: Contribution): string {
  switch (c) {
    case "livetv_channels":
      return t("Channels");
    case "list_source":
      return t("Catalogues");
    case "debrid_provider":
    case "stream_source":
      return t("Sources");
    case "subtitles":
      return t("Subtitles");
    case "metadata":
      return t("Details");
    case "plugin_directory":
      return t("Directories");
  }
}

export function liveTVKind(pluginId: string): string {
  return `plugin:${pluginId}/channels`;
}
