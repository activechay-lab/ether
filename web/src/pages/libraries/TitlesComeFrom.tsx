import { Button, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link } from "react-router-dom";
import { t } from "../../i18n";
import { errorMessage } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { librariesApi, type Library } from "./api";
export function TitlesComeFrom({ library, onSaved }: { library: Library; onSaved: () => void }) {
  const registry = useAsync((signal) => librariesApi.catalogueSources(signal), []);
  const [selected, setSelected] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const current =
    selected ??
    library.sources ??
    registry.data
      ?.filter((source) => source.libraries.some((lib) => lib.id === library.id))
      .map((source) => source.id) ??
    [];
  const mediaType = library.mediaType ?? library.filter?.type ?? "movie";
  return (
    <SettingsSection title={t("Titles come from")}>
      <div className="flex flex-col gap-3">
        {registry.data
          ?.filter((source) =>
            library.source === "plugin"
              ? current.includes(source.id)
              : source.group !== "plugin" && source.mediaTypes.includes(String(mediaType)),
          )
          .map((source) => (
            <label
              key={source.id}
              className="flex items-center gap-3 rounded-lg border border-border p-3"
            >
              <input
                type="checkbox"
                disabled={library.source === "plugin" || library.source === "builtin" || busy}
                checked={current.includes(source.id)}
                onChange={(e) =>
                  setSelected(
                    e.target.checked
                      ? [...current, source.id]
                      : current.filter((id) => id !== source.id),
                  )
                }
              />
              {source.name}
            </label>
          ))}
        {selected ? (
          <Button
            disabled={busy || !selected.length}
            onClick={async () => {
              setBusy(true);
              try {
                await librariesApi.patch(library.id, { sources: selected });
                toast.success(t("Saved"));
                onSaved();
              } catch (e) {
                toast.error(errorMessage(e));
              } finally {
                setBusy(false);
              }
            }}
          >
            {t("Save")}
          </Button>
        ) : null}
        <Link className="text-primary underline" to="/libraries/sources">
          {t("Catalogue sources")}
        </Link>
      </div>
    </SettingsSection>
  );
}
