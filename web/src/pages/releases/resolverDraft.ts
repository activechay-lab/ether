import { toast } from "@braedonsaunders/appkit-ui";
import { useCallback } from "react";
import { useApp } from "../../app-context";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import type { CustomFormatDefinition, EtherConfig, QualityProfile } from "../../lib/types";

export type ResolverPolicy = EtherConfig["resolver"];

/** The resolver section. A save sends only the patch; the server keeps every other field. */
export function useResolver() {
  const { refreshDashboard } = useApp();
  const config = useAsync(() => api.getConfig(), []);
  const resolver = config.data?.resolver ?? null;
  const save = useCallback(
    async (patch: Partial<ResolverPolicy>, message: string) => {
      if (!resolver) return;
      await api.putSection("resolver", patch);
      toast.success(message);
      await config.reload();
      void refreshDashboard();
    },
    [config, refreshDashboard, resolver],
  );
  return { config, resolver, save };
}

/** The two JSON editors, parsed. */
export interface ParsedScoring {
  customFormats: CustomFormatDefinition[];
  customFormatScores: Record<string, number>;
}

/** Parse the two JSON editors; throws a readable error when either is invalid. */
export function parseScoring(formats: string, scores: string): ParsedScoring {
  let definitions: unknown;
  let overrides: unknown;
  try {
    definitions = JSON.parse(formats);
  } catch {
    throw Error(t("Custom format definitions aren't valid JSON."));
  }
  try {
    overrides = JSON.parse(scores);
  } catch {
    throw Error(t("Score overrides aren't valid JSON."));
  }
  if (!Array.isArray(definitions)) throw Error(t("Definitions must be a JSON array."));
  if (
    !overrides ||
    Array.isArray(overrides) ||
    typeof overrides !== "object" ||
    Object.values(overrides).some((v) => typeof v !== "number" || !Number.isInteger(v))
  )
    throw Error(t("Scores must map format IDs to whole numbers."));
  return {
    customFormats: definitions as CustomFormatDefinition[],
    customFormatScores: overrides as Record<string, number>,
  };
}

/** The key a definition is matched by: trash_id, then id, then name. */
export function formatKey(format: CustomFormatDefinition): string {
  return format.trash_id || format.id || format.name || "";
}

/** Keep an existing app tag when the edited JSON omits it. */
function preserveApp(
  edited: CustomFormatDefinition,
  original: CustomFormatDefinition | undefined,
  latest: CustomFormatDefinition | undefined,
): CustomFormatDefinition {
  if (edited.app) return edited;
  const app = latest?.app || original?.app;
  return app ? { ...edited, app } : edited;
}

/** Tag a genuinely new definition with the profile's app when it has none. */
function tagNew(
  format: CustomFormatDefinition,
  app: "radarr" | "sonarr" | null | undefined,
): CustomFormatDefinition {
  if (!app || format.app) return format;
  return { ...format, app };
}

/**
 * Three-way merge of the shared custom format definitions.
 *
 * `original` is the list the drawer opened with, `edited` is the user's list,
 * `latest` is the server's list now. Definitions another import added after
 * the drawer opened survive; the user's additions and edits win; only entries
 * the user removed from the original are deleted.
 */
export function mergeCustomFormats({
  original,
  edited,
  latest,
  app,
}: {
  original: CustomFormatDefinition[] | null | undefined;
  edited: CustomFormatDefinition[];
  latest: CustomFormatDefinition[] | null | undefined;
  /** Profile app, used to tag new definitions that have no app. */
  app?: "radarr" | "sonarr" | null;
}): CustomFormatDefinition[] {
  const originalByKey = new Map<string, CustomFormatDefinition>();
  for (const f of original ?? []) {
    const key = formatKey(f);
    if (key) originalByKey.set(key, f);
  }
  const editedByKey = new Map<string, CustomFormatDefinition>();
  for (const f of edited) {
    const key = formatKey(f);
    if (key) editedByKey.set(key, f);
  }
  const merged: CustomFormatDefinition[] = [];
  const seen = new Set<string>();
  for (const server of latest ?? []) {
    const key = formatKey(server);
    if (!key) {
      merged.push(server);
      continue;
    }
    seen.add(key);
    if (originalByKey.has(key)) {
      const edit = editedByKey.get(key);
      // Still in the user's list: apply their edit; absent means they removed it.
      if (edit) merged.push(preserveApp(edit, originalByKey.get(key), server));
      continue;
    }
    // Added on the server after the drawer opened; keep it.
    const edit = editedByKey.get(key);
    merged.push(edit ? preserveApp(edit, undefined, server) : server);
  }
  for (const f of edited) {
    const key = formatKey(f);
    if (key && (seen.has(key) || originalByKey.has(key))) continue;
    merged.push(tagNew(f, app));
  }
  return merged;
}

/** True when two definition lists serialize identically. */
export function sameFormats(
  a: CustomFormatDefinition[] | null | undefined,
  b: CustomFormatDefinition[] | null | undefined,
): boolean {
  return JSON.stringify(a ?? []) === JSON.stringify(b ?? []);
}

/** What the profile drawer shows: shared definitions, and this profile's scores. */
export function scoringText(resolver: ResolverPolicy | null, profile: QualityProfile) {
  const own = profile.customFormatScores;
  const scores = own && Object.keys(own).length > 0 ? own : (resolver?.customFormatScores ?? {});
  return {
    formats: JSON.stringify(resolver?.customFormats ?? [], null, 2),
    scores: JSON.stringify(scores, null, 2),
  };
}
