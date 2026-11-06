import { Button, Switch, toast } from "@braedonsaunders/appkit-ui";
import { ChevronDown } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { TestButton, type TestState } from "../../../components/fields";
import { Section } from "../../../components/layout";
import { useViewers } from "../../../components/pickers";
import { SettingsRow } from "../../../components/SettingsRow";
import { t } from "../../../i18n";
import { useLocale } from "../../../i18n/react";
import { ApiError, apiFetch, errorMessage } from "../../../lib/api";
import { SchemaGroups } from "../../settings/SchemaForm";
import { FolderGrantsPicker, ViewerGrantsPicker } from "../PermissionSheet";
import { SearchSetting } from "./SearchSetting";
import { AddonSettings } from "./AddonSettings";
import {
  buildGrantItems,
  mapApiFieldErrors,
  schemaToFieldGroups,
  selectFieldsNeedingOptions,
  type PluginDetail,
  type PluginGrants,
} from "../types";

/**
 * How the plugin is set up: its own settings, who and what it may use,
 * and, out of the way, auto-update and the sites it may reach.
 */
export function PluginSetup({
  detail,
  onDetail,
  onChanged,
  onError,
}: {
  detail: PluginDetail;
  /** Reload the plugin after a change made here. */
  onDetail: () => Promise<void>;
  onChanged: () => void;
  onError: (e: unknown) => void;
}) {
  const { viewers } = useViewers();
  const { locale } = useLocale();
  const [draft, setDraft] = useState<Record<string, unknown>>({});
  const [schema, setSchema] = useState<Record<string, unknown>>({});
  const [options, setOptions] = useState<Record<string, { value: string; label: string }[]>>({});
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [test, setTest] = useState<TestState>({ status: "idle" });
  const [more, setMore] = useState(false);
  const id = detail.id;

  const load = useCallback(async () => {
    try {
      const res = await apiFetch<{
        settings: Record<string, unknown>;
        schema: Record<string, unknown>;
      }>(`/plugins/${encodeURIComponent(id)}/settings?locale=${encodeURIComponent(locale)}`);
      setDraft(res.settings ?? {});
      const sch = res.schema ?? {};
      setSchema(sch);
      const fields = selectFieldsNeedingOptions(sch);
      const entries = await Promise.all(
        fields.map(async (field) => {
          try {
            const r = await apiFetch<{ options: { value: string; label: string }[] }>(
              `/plugins/${encodeURIComponent(id)}/options/${encodeURIComponent(field)}`,
            );
            return [field, r.options ?? []] as const;
          } catch {
            return [field, []] as const;
          }
        }),
      );
      setOptions(Object.fromEntries(entries));
    } catch {
      // A plugin that cannot answer keeps an empty form.
    } finally {
      setLoaded(true);
    }
  }, [id, locale]);
  useEffect(() => {
    setLoaded(false);
    setFieldErrors({});
    setTest({ status: "idle" });
    void load();
  }, [load]);

  const save = async () => {
    setSaving(true);
    setFieldErrors({});
    onError(null);
    try {
      await apiFetch(`/plugins/${encodeURIComponent(id)}/settings`, {
        method: "PUT",
        body: JSON.stringify({ settings: draft }),
      });
      toast.success(t("Settings saved"));
      await load();
      onChanged();
    } catch (e) {
      onError(e);
      if (e instanceof ApiError && e.fields)
        setFieldErrors(mapApiFieldErrors(e.fields, "settings"));
    } finally {
      setSaving(false);
    }
  };

  const runTest = async () => {
    setTest({ status: "running" });
    const started = Date.now();
    try {
      const res = await apiFetch<{ ok: boolean; message?: string }>(
        `/plugins/${encodeURIComponent(id)}/test`,
        { method: "POST" },
      );
      setTest(
        res.ok
          ? { status: "ok", durationMs: Date.now() - started }
          : { status: "fail", message: res.message || t("Test failed") },
      );
    } catch (e) {
      setTest({ status: "fail", message: errorMessage(e) });
    }
  };

  const updateGrants = async (next: PluginGrants) => {
    onError(null);
    try {
      await apiFetch(`/plugins/${encodeURIComponent(id)}/grants`, {
        method: "PUT",
        body: JSON.stringify(next),
      });
      await onDetail();
      toast.success(t("Access updated"));
      onChanged();
    } catch (e) {
      onError(e);
    }
  };

  const toggleAutoUpdate = async (next: boolean) => {
    try {
      await apiFetch(`/plugins/${encodeURIComponent(id)}/auto-update`, {
        method: "PUT",
        body: JSON.stringify({ autoUpdate: next }),
      });
      await onDetail();
      onChanged();
    } catch (e) {
      onError(e);
    }
  };

  const searchFields: { key: string; label: string; placeholder?: string }[] = [];
  const plainSchema = JSON.parse(JSON.stringify(schema), (_key, value) => {
    if (Array.isArray(value))
      return value.filter((f) => {
        if (f?.kind === "select" && f.search) {
          searchFields.push(f);
          return false;
        }
        return true;
      });
    return value;
  });
  const groups = schemaToFieldGroups(plainSchema, draft, options, viewers);
  const hasSettings = groups.length > 0 || searchFields.length > 0;
  const grants = detail.permissions.grants ?? {};
  const revoked = new Set(grants.revoked ?? []);
  const grantItems = buildGrantItems(detail).filter((g) => g.revocable);
  const access = detail.permissions.needsViewers || detail.permissions.needsFolders;
  // Updates come from where the plugin was installed; Studio builds and
  // add-ons that always follow their link have nothing to auto-update.
  const updatable = detail.sourceKind !== "studio" && !detail.stremioOnly;

  return (
    <div className="space-y-5">
      {searchFields.map((field) => (
        <SearchSetting
          key={field.key}
          plugin={id}
          field={field}
          value={draft[field.key]}
          onChange={(value) => setDraft((prev) => ({ ...prev, [field.key]: value }))}
        />
      ))}
      {detail.host ? (
        <AddonSettings
          detail={detail}
          showEmpty={loaded && !hasSettings}
          onSaved={() => {
            void onDetail();
            onChanged();
          }}
        />
      ) : null}
      {!loaded || (detail.host && !hasSettings) ? null : hasSettings && !groups.length ? (
        <Button size="sm" disabled={saving} onClick={() => void save()}>
          {saving ? t("Saving…") : t("Save settings")}
        </Button>
      ) : hasSettings ? (
        <SchemaGroups
          groups={groups}
          value={draft}
          onChange={setDraft}
          errors={fieldErrors}
          prefix="settings"
          footer={
            <div className="flex w-full flex-wrap items-center justify-between gap-2">
              <TestButton state={test} onTest={() => void runTest()} />
              <Button type="button" size="sm" disabled={saving} onClick={() => void save()}>
                {saving ? t("Saving…") : t("Save settings")}
              </Button>
            </div>
          }
        />
      ) : (
        <Section
          title={t("Settings")}
          footer={<TestButton state={test} onTest={() => void runTest()} />}
        >
          <p className="text-sm text-fg-muted">{t("Nothing to set up. It works as installed.")}</p>
        </Section>
      )}

      {access ? (
        <div className="space-y-4">
          {detail.permissions.needsViewers ? (
            <ViewerGrantsPicker
              value={grants.viewers ?? []}
              onChange={(v) => void updateGrants({ ...grants, viewers: v })}
            />
          ) : null}
          {detail.permissions.needsFolders ? (
            <FolderGrantsPicker
              value={grants.folders ?? []}
              onChange={(f) => void updateGrants({ ...grants, folders: f })}
            />
          ) : null}
        </div>
      ) : null}

      {updatable || grantItems.length ? (
        <section className="overflow-hidden rounded-xl border border-border bg-surface">
          <button
            type="button"
            aria-expanded={more}
            onClick={() => setMore((m) => !m)}
            className="flex w-full items-center justify-between px-5 py-3.5 text-left text-sm font-medium text-fg hover:bg-surface-hover"
          >
            {t("More options")}
            <ChevronDown
              size={16}
              className={more ? "rotate-180 transition-transform" : "transition-transform"}
              aria-hidden
            />
          </button>
          {more ? (
            <div className="divide-y divide-border border-t border-border">
              {updatable ? (
                <SettingsRow
                  title={<label htmlFor="plugin-auto-update">{t("Auto-update")}</label>}
                  description={t("Install new versions that ask for no new access.")}
                  control={
                    <Switch
                      id="plugin-auto-update"
                      checked={detail.autoUpdate}
                      onChange={(e) => void toggleAutoUpdate(e.currentTarget.checked)}
                      aria-label={t("Auto-update")}
                    />
                  }
                />
              ) : null}
              {grantItems.map((g) => {
                const allowed = !revoked.has(g.key);
                return (
                  <SettingsRow
                    key={g.key}
                    title={g.label}
                    description={allowed ? undefined : t("Blocked. Calls that need this fail.")}
                    control={
                      <Switch
                        checked={allowed}
                        aria-label={t("Allow: {permission}", { permission: g.label })}
                        onChange={(e) => {
                          const next = new Set(revoked);
                          if (e.currentTarget.checked) next.delete(g.key);
                          else next.add(g.key);
                          void updateGrants({ ...grants, revoked: [...next] });
                        }}
                      />
                    }
                  />
                );
              })}
            </div>
          ) : null}
        </section>
      ) : null}
    </div>
  );
}
