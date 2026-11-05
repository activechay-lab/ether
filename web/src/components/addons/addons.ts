import { localized, t } from "../../i18n";
import { apiFetch } from "../../lib/api";

/** A role a Stremio add-on can play in Ether (a plugin capability). */
export type AddonRole =
  | "stream_source"
  | "list_source"
  | "livetv_channels"
  | "subtitles"
  | "metadata"
  | "plugin_directory";

interface AddonRoleState {
  role: AddonRole | string;
  on: boolean;
}

export interface AddonCatalog {
  /** The add-on list source key ("catalog-…"), as in plugin:<id>/<key>. */
  key: string;
  type: string;
  id: string;
  name: string;
}

/** POST /plugins/addons/preview */
export interface AddonPreview {
  id: string;
  name: string;
  description?: string;
  logo?: string;
  version?: string;
  host: string;
  roles: AddonRoleState[] | null;
  catalogs?: AddonCatalog[] | null;
  alreadyInstalled?: boolean;
  configureFirst?: boolean;
}

/** An installed add-on (plugin summary). */
export interface AddonSummary {
  id: string;
  name: string;
  version: string;
  enabled: boolean;
  icon?: string;
  summary?: string;
  host?: string;
  health?: string;
  roles?: AddonRoleState[] | null;
  stremioOnly?: boolean;
}

export const ADDON_ROLE_LABEL = localized<Record<AddonRole, string>>(() => ({
  stream_source: t("Streams to play"),
  list_source: t("Catalogs for collections and Home rows"),
  livetv_channels: t("Live TV channels"),
  subtitles: t("Subtitles"),
  metadata: t("Title details"),
  plugin_directory: t("Plugin directory"),
}));

export const addonsApi = {
  /** Installed Stremio add-ons (plugins without an Ether block). */
  list: async () =>
    ((await apiFetch<{ plugins: AddonSummary[] | null }>("/plugins")).plugins ?? []).filter(
      (p) => p.stremioOnly,
    ),
  setRoles: (id: string, off: string[]) =>
    apiFetch<{ roles: AddonRoleState[] }>(`/plugins/${encodeURIComponent(id)}/roles`, {
      method: "PUT",
      body: JSON.stringify({ off }),
    }),
};

/** An add-on install link as typed: trimmed, with stremio:// read as https://. */
export function normalizeAddonUrl(url: string): string {
  const trimmed = url.trim();
  return /^stremio:\/\//i.test(trimmed) ? `https://${trimmed.slice("stremio://".length)}` : trimmed;
}

const DEBRID_KEY_PARAM =
  /(?:^|[/|&?,;])(?:realdebrid|torbox|alldebrid|premiumize|debridlink|offcloud|putio)=/i;
const DEBRID_KEY_FIELD = /"(?:debridApiKey|apiKey|debridService)"\s*:\s*"[^"]+"/i;

/** A base64 or base64url path segment decoded, when it is a JSON object. */
function decodeJsonSegment(segment: string): string | null {
  if (segment.length < 16 || !/^[A-Za-z0-9+/_=-]+$/.test(segment)) return null;
  const b64 = segment.replace(/-/g, "+").replace(/_/g, "/");
  try {
    const text = atob(b64.padEnd(Math.ceil(b64.length / 4) * 4, "="));
    return text.trimStart().startsWith("{") ? text : null;
  } catch {
    return null;
  }
}

/** Whether an addon URL embeds a debrid API key in its configuration. */
export function addonUrlHasDebridKey(url: string): boolean {
  let path = normalizeAddonUrl(url);
  try {
    path = new URL(path).pathname;
  } catch {
    // Not a URL yet: inspect what was typed.
  }
  let decoded = path;
  try {
    decoded = decodeURIComponent(path);
  } catch {
    // Keep the raw path.
  }
  if (DEBRID_KEY_PARAM.test(decoded)) return true;
  return path.split("/").some((segment) => {
    const json = decodeJsonSegment(segment);
    return json !== null && DEBRID_KEY_FIELD.test(json);
  });
}
