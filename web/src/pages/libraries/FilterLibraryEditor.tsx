import { Button, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Drawer } from "../../components/Drawer";
import { Segmented } from "../../components/fields";
import { t } from "../../i18n";
import { api, errorMessage } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { librariesApi, type Library } from "./api";
import { CatalogFilterBar, compileQuery, readQuery } from "./browser/CatalogFilterBar";

/**
 * Create (or preview) a saved-filter library. The filter itself is
 * built with the same CatalogFilterBar the browser uses, seeded from
 * the current browse state when the owner asks from a library page.
 * New libraries are console-only until "Show in apps" is turned on
 * (§15.3). A saved filter cannot be edited after creation yet — the
 * libraries service has no SetFilter — so this drawer is create-only
 * and says so before saving.
 */
export function FilterLibraryEditor({
  seed,
  onClose,
  onCreated,
}: {
  seed?: URLSearchParams;
  onClose: () => void;
  onCreated: (lib: Library) => void;
}) {
  const viewers = useAsync(() => api.users(), []);
  const [name, setName] = useState("");
  const [type, setType] = useState(seed?.get("type") === "show" ? "show" : "movie");
  const [params, setParams] = useState<URLSearchParams>(
    () => new URLSearchParams(seed?.toString() ?? ""),
  );
  const [saving, setSaving] = useState(false);

  const buildFilter = (): Record<string, unknown> => {
    const compiled = compileQuery(
      readQuery(params.get("filters") || ""),
      params.get("viewer") || "",
    ) as Record<string, unknown>;
    const filter: Record<string, unknown> = { ...compiled, type };
    const genre = params.get("genre");
    if (genre) filter.genres = [genre];
    const decade = Number(params.get("decade"));
    if (decade) {
      filter.yearFrom = decade;
      filter.yearTo = decade + 9;
    }
    const q = (params.get("q") || "").trim();
    if (q) filter.query = q;
    return filter;
  };

  const save = async () => {
    setSaving(true);
    try {
      const lib = await librariesApi.createFilter(name.trim(), buildFilter());
      toast.success(t("Library created"));
      onCreated(lib);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Drawer
      open
      onClose={onClose}
      title={t("New library from a filter")}
      size="lg"
      footer={
        <div className="flex items-center justify-between gap-3">
          <span className="text-xs text-fg-muted">
            {t("Starts console-only. Turn on “Show in apps” in its Settings when ready.")}
          </span>
          <span className="flex gap-2">
            <Button variant="ghost" onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button disabled={saving || !name.trim()} onClick={() => void save()}>
              {t("Create library")}
            </Button>
          </span>
        </div>
      }
    >
      <div className="flex flex-col gap-4">
        <label className="library-field">
          <span>{t("Library name")}</span>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t("Comedies the kids can watch")}
            aria-label={t("Library name")}
          />
        </label>
        <div className="library-field">
          <span>{t("What goes in it")}</span>
          <Segmented
            ariaLabel={t("What goes in it")}
            value={type}
            onChange={setType}
            options={[
              { value: "movie", label: t("Movies") },
              { value: "show", label: t("TV shows") },
            ]}
          />
        </div>
        <CatalogFilterBar
          params={params}
          viewers={viewers.data || []}
          onChange={(next) => setParams(new URLSearchParams(next.toString()))}
        />
        <p className="text-sm text-fg-muted">
          {t(
            "Pick filters above; every matching title joins the library and keeps its place in Movies or TV Shows too. The name can change later, but the filter itself cannot yet — you would delete and recreate the library.",
          )}
        </p>
      </div>
    </Drawer>
  );
}
