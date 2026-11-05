import { Badge, Button, Input } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { ErrorPanel } from "../../components/feedback";

export function CatalogTags({ titleKey }: { titleKey: string }) {
  const path = `/catalog/title/${encodeURIComponent(titleKey)}/tags`;
  const state = useAsync((signal) => apiFetch<{ tags: string[] }>(path, { signal }), [titleKey]);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  return (
    <section>
      <h3>{t("Tags")}</h3>
      {error ? <ErrorPanel error={error} onRetry={() => setError(null)} /> : null}
      {editing ? (
        <form
          className="flex flex-wrap gap-2"
          onSubmit={async (event) => {
            event.preventDefault();
            setSaving(true);
            setError(null);
            try {
              await apiFetch(path, {
                method: "PUT",
                body: JSON.stringify({
                  tags: [
                    ...new Set(
                      draft
                        .split(",")
                        .map((tag) => tag.trim())
                        .filter(Boolean),
                    ),
                  ],
                }),
              });
              await state.reload();
              setEditing(false);
            } catch (error) {
              setError(error);
            } finally {
              setSaving(false);
            }
          }}
        >
          <Input
            aria-label={t("Tags")}
            value={draft}
            onChange={(event) => setDraft(event.currentTarget.value)}
            autoFocus
            maxLength={1040}
          />
          <Button size="sm" disabled={saving}>
            {t("Save")}
          </Button>
          <Button size="sm" variant="ghost" type="button" onClick={() => setEditing(false)}>
            {t("Cancel")}
          </Button>
        </form>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          {state.data?.tags?.map((tag) => (
            <Badge key={tag} variant="outline">
              {tag}
            </Badge>
          ))}
          <Button
            size="sm"
            variant="ghost"
            disabled={state.loading || !!state.error}
            onClick={() => {
              setDraft((state.data?.tags || []).join(", "));
              setEditing(true);
            }}
          >
            {t("Edit")}
          </Button>
        </div>
      )}
    </section>
  );
}
