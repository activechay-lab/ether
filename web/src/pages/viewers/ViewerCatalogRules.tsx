import { Button, Checkbox, Select, SettingsSection } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Drawer } from "../../components/Drawer";
import { useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { RulesPanel, type VisibilityRule } from "../libraries/browser/LibraryVisibility";

type Policy = {
  mode: string;
  exclusions: VisibilityRule[];
  visibilityDefault: string;
  exemptions: string[];
};
type Response = {
  policy: Policy;
  globalRules: VisibilityRule[];
  globalDefault: string;
  globalIDs: string[];
};

export function ViewerCatalogRules({ id, region }: { id: number; region: string }) {
  const saved = useAsync(
    (signal) => apiFetch<Response>(`/viewers/${id}/catalog-rules`, { signal }),
    [id],
  );
  const draft = useDraft(saved.data?.policy);
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  if (saved.error) return <ErrorPanel error={saved.error} onRetry={() => void saved.reload()} />;
  const p = draft.draft;
  if (!p || !saved.data) return <LoadingRows rows={2} />;
  const data = saved.data;
  const patch = (next: Partial<Policy>) => draft.setDraft({ ...p, ...next });
  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      saved.setData(
        await apiFetch<Response>(`/viewers/${id}/catalog-rules`, {
          method: "PUT",
          body: JSON.stringify(p),
        }),
      );
      setOpen(false);
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };
  return (
    <SettingsSection
      title={t("Catalogue rules")}
      description={t(
        "Choose which catalogue rules this viewer uses. Parental controls always apply.",
      )}
    >
      <SettingsRow
        title={t("Catalogue rules")}
        control={
          <Button variant="outline" onClick={() => setOpen(true)}>
            {t("Edit")}
          </Button>
        }
      />
      <Drawer
        open={open}
        onClose={() => setOpen(false)}
        title={t("Catalogue rules")}
        size="xl"
        footer={
          <div className="flex justify-end gap-2">
            <Button
              variant="outline"
              disabled={saving}
              onClick={() => {
                draft.reset();
                setError(null);
                setOpen(false);
              }}
            >
              {t("Cancel")}
            </Button>
            <Button disabled={!draft.dirty || saving} onClick={() => void save()}>
              {t("Save changes")}
            </Button>
          </div>
        }
      >
        <p className="mb-4 text-sm text-fg-muted">
          {t("Choose which catalogue rules this viewer uses. Parental controls always apply.")}
        </p>
        <SettingsRow
          title={t("Catalogue rules")}
          control={
            <Select
              aria-label={t("Catalogue rules")}
              value={p.mode}
              onChange={(e) => {
                const mode = e.target.value;
                patch(
                  mode === "custom"
                    ? {
                        mode,
                        exclusions: structuredClone(data.globalRules).filter(
                          (_, i) => !p.exemptions.includes(data.globalIDs[i]!),
                        ),
                        visibilityDefault: data.globalDefault || "show",
                      }
                    : { mode },
                );
              }}
            >
              {[
                { value: "inherit", label: t("Use global rules") },
                { value: "custom", label: t("Own rule set") },
                { value: "exempt", label: t("Exempt from selected rules") },
              ].map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}{" "}
            </Select>
          }
        />
        {p.mode === "exempt" ? (
          <div className="space-y-3">
            {data.globalRules.map((rule, i) => (
              <label key={data.globalIDs[i]!} className="flex items-center gap-3 text-sm">
                <Checkbox
                  checked={p.exemptions.includes(data.globalIDs[i]!)}
                  onChange={(e) =>
                    patch({
                      exemptions: e.target.checked
                        ? [...p.exemptions, data.globalIDs[i]!]
                        : p.exemptions.filter((id) => id !== data.globalIDs[i]!),
                    })
                  }
                />
                {rule.name || t("Rule {number}", { number: i + 1 })}
              </label>
            ))}
          </div>
        ) : null}
        {p.mode === "custom" ? (
          <RulesPanel
            rules={p.exclusions}
            fallback={p.visibilityDefault}
            onRules={(exclusions) => patch({ exclusions })}
            onFallback={(visibilityDefault) => patch({ visibilityDefault })}
            region={region}
          />
        ) : null}
        {error ? <ErrorPanel error={error} /> : null}
      </Drawer>
    </SettingsSection>
  );
}
