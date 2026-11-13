import {
  Badge,
  Button,
  Checkbox,
  SettingsSection,
  buttonVariants,
  toast,
} from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link } from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { SettingsRow } from "../../components/SettingsRow";
import { ErrorPanel } from "../../components/feedback";
import { t } from "../../i18n";
import { errorMessage } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { librariesApi, type Library } from "./api";
import { SourceIcon, sourceOwner } from "./CatalogueSources";
export function TitlesComeFrom({ library, onSaved }: { library: Library; onSaved: () => void }) {
  const registry = useAsync((signal) => librariesApi.catalogueSources(signal), []);
  const [draft, setDraft] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const ids =
    library.sources ??
    registry.data
      ?.filter((source) => source.libraries.some((lib) => lib.id === library.id))
      .map((source) => source.id) ??
    [];
  const mediaType = String(library.mediaType ?? library.filter?.type ?? "movie");
  const save = async () => {
    if (!draft) return;
    setBusy(true);
    try {
      await librariesApi.patch(library.id, { sources: draft });
      setDraft(null);
      onSaved();
      void registry.reload();
      toast.success(t("Saved"));
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <SettingsSection title={t("Titles come from")}>
        <SettingsRow
          title={t("Catalogue sources")}
          description={t("Choose sources for this library.")}
          control={
            <div className="flex flex-wrap items-center justify-end gap-2">
              {ids.map((id) => (
                <Badge key={id} variant="secondary">
                  {registry.data?.find((source) => source.id === id)?.name ?? t("Unavailable")}
                </Badge>
              ))}
              {library.source !== "builtin" && library.source !== "plugin" ? (
                <Button size="sm" variant="outline" onClick={() => setDraft([...ids])}>
                  {t("Change")}
                </Button>
              ) : null}
            </div>
          }
        />
        <SettingsRow
          title={t("Integrations")}
          control={
            <Link className={buttonVariants({ variant: "outline", size: "sm" })} to="/services">
              {t("Manage in Integrations")}
            </Link>
          }
        />
      </SettingsSection>
      {draft ? (
        <Drawer
          open
          onClose={() => setDraft(null)}
          title={t("Titles come from")}
          footer={
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setDraft(null)}>
                {t("Cancel")}
              </Button>
              <Button disabled={busy || !draft.length} onClick={() => void save()}>
                {t("Save")}
              </Button>
            </div>
          }
        >
          <div className="space-y-3">
            {registry.error ? (
              <ErrorPanel error={registry.error} onRetry={() => void registry.reload()} />
            ) : null}
            {registry.data
              ?.filter(
                (source) => source.group !== "plugin" && source.mediaTypes.includes(mediaType),
              )
              .map((source) => (
                <label
                  key={source.id}
                  className="flex cursor-pointer items-center gap-3 rounded-xl border border-border p-4 hover:bg-surface-hover"
                >
                  <SourceIcon source={source} />
                  <Checkbox
                    disabled={busy}
                    checked={draft.includes(source.id)}
                    onChange={(e) =>
                      setDraft(
                        e.target.checked
                          ? [...draft, source.id]
                          : draft.filter((id) => id !== source.id),
                      )
                    }
                  />
                  <span>
                    <span className="block font-medium">{source.name}</span>
                    <span className="block text-sm text-fg-muted">{sourceOwner(source)}</span>
                  </span>
                </label>
              ))}
            {registry.data
              ? draft
                  .filter((id) => !registry.data?.some((source) => source.id === id))
                  .map((id) => (
                    <label
                      key={id}
                      className="flex items-center gap-3 rounded-xl border border-border p-4"
                    >
                      <Checkbox
                        checked
                        disabled={busy}
                        onChange={() => setDraft(draft.filter((value) => value !== id))}
                      />
                      {t("Unavailable")}
                    </label>
                  ))
              : null}
          </div>
        </Drawer>
      ) : null}
    </>
  );
}
