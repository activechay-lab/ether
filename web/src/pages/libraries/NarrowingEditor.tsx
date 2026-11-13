import { Button, SearchSelect } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Drawer } from "../../components/Drawer";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { librariesApi, type Library } from "./api";
import { CatalogFilterBar, compileQuery, readQuery } from "./browser/CatalogFilterBar";
export function NarrowingEditor({
  library,
  onClose,
  onChange,
}: {
  library: Library;
  onClose: () => void;
  onChange: (filter?: Record<string, unknown>, collectionId?: number) => void;
}) {
  const [params, setParams] = useState(new URLSearchParams());
  const [collectionId, setCollectionId] = useState(library.collectionId ?? 0);
  const viewers = useAsync(() => api.users(), []);
  const collections = useAsync(
    (signal) =>
      library.source === "collection"
        ? librariesApi.listCollectionSources(signal)
        : Promise.resolve([]),
    [library.source],
  );
  const type = String(library.mediaType ?? library.filter?.type ?? "movie");
  return (
    <Drawer
      open
      title={library.source === "collection" ? t("Collection") : t("Replace filter")}
      onClose={onClose}
      size="lg"
      footer={
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose}>
            {t("Cancel")}
          </Button>
          <Button
            disabled={library.source === "collection" && !collectionId}
            onClick={() =>
              library.source === "collection"
                ? onChange(undefined, collectionId)
                : onChange({
                    ...compileQuery(readQuery(params.get("filters")), params.get("viewer") ?? ""),
                    type,
                    ...(params.get("q") ? { query: params.get("q") } : {}),
                  })
            }
          >
            {t("Apply")}
          </Button>
        </div>
      }
    >
      {library.source === "collection" ? (
        <SearchSelect
          ariaLabel={t("Collection")}
          value={String(collectionId)}
          onChange={(value) => setCollectionId(Number(value))}
          options={(collections.data ?? [])
            .filter(
              (collection) => collection.mediaType === type || collection.mediaType === "mixed",
            )
            .map((collection) => ({ value: String(collection.id), label: collection.name }))}
        />
      ) : (
        <div className="space-y-4 [&_.catalog-query]:!px-0">
          <p className="text-sm text-fg-muted">
            {t("Choose new filters to replace this library's saved filter.")}
          </p>
          <CatalogFilterBar params={params} viewers={viewers.data ?? []} onChange={setParams} />
        </div>
      )}
    </Drawer>
  );
}
