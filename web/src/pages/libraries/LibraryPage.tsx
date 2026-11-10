import { Button, EmptyState } from "@braedonsaunders/appkit-ui";
import { ArrowLeft, ChevronRight, Layers, Plus } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useApp } from "../../app-context";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { SubtabNav } from "../../components/layout";
import { LevelBadge } from "../../components/status";
import { Workspace } from "../../components/workspace";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { librariesApi, type Library } from "./api";
import { CatalogBrowser } from "./browser/Catalog";
import { CatalogSettings } from "./browser/CatalogSettings";
import { FilterLibraryEditor } from "./FilterLibraryEditor";
import { LibrarySettings } from "./LibrarySettings";
import { PluginBrowse } from "./PluginBrowse";

/** Collections that apply to this library, deep-linking to the Studio drawer. */
function LibraryCollections({ library }: { library: Library }) {
  const collections = useAsync(() => api.collections(), []);
  const type = library.mediaType || library.filter?.type;
  const applicable = (collections.data ?? []).filter(
    (c) => !type || c.mediaType === "mixed" || c.mediaType === type,
  );
  if (collections.loading) return <LoadingRows />;
  if (!applicable.length)
    return (
      <EmptyState
        icon={<Layers size={22} aria-hidden />}
        title={t("No collections for this library yet")}
        description={t("Collections gather matching titles into rows and library tabs.")}
        action={
          <Link to="/collections/new">
            <Button size="sm">
              <Plus size={14} /> {t("New collection")}
            </Button>
          </Link>
        }
      />
    );
  return (
    <ul className="divide-y divide-border overflow-hidden rounded-xl border border-border bg-surface">
      {applicable.map((c) => (
        <li key={c.id ?? c.name}>
          <Link
            to={c.id ? `/collections/${c.id}` : "/collections"}
            className="flex items-center gap-3 px-5 py-3.5 transition-colors hover:bg-surface-hover"
          >
            <Layers size={16} className="shrink-0 text-fg-muted" aria-hidden />
            <span className="min-w-0 flex-1">
              <span className="block truncate font-medium text-fg">{c.name}</span>
              {c.description ? (
                <span className="block truncate text-sm text-fg-muted">{c.description}</span>
              ) : null}
            </span>
            <ChevronRight size={16} className="shrink-0 text-fg-muted" aria-hidden />
          </Link>
        </li>
      ))}
    </ul>
  );
}

/** One library: Browse · Collections · Rules · Settings (spec §11). */
export function LibraryPage() {
  const { id: rawId, tab: rawTab } = useParams();
  const navigate = useNavigate();
  const { health, refreshDashboard } = useApp();
  const [params, setParams] = useSearchParams();
  const [savingAsLibrary, setSavingAsLibrary] = useState(false);
  const id = Number(rawId);
  const library = useAsync((signal) => librariesApi.get(id, signal), [id]);
  const lib = library.data;
  const catalogHealth = health.find((h) => h.id === "catalog");

  if (!Number.isSafeInteger(id) || id <= 0)
    return (
      <Workspace title={t("Libraries")}>
        <ErrorPanel error={new Error(t("That library does not exist"))} />
      </Workspace>
    );
  if (library.loading && !lib)
    return (
      <Workspace title={t("Libraries")}>
        <LoadingRows />
      </Workspace>
    );
  if (!lib)
    return (
      <Workspace title={t("Libraries")}>
        <ErrorPanel error={library.error} onRetry={() => void library.reload()} />
      </Workspace>
    );

  const canCollections = lib.source !== "plugin";
  const tabs = [
    { key: "browse", label: t("Browse") },
    ...(canCollections ? [{ key: "collections", label: t("Collections") }] : []),
    ...(canCollections ? [{ key: "rules", label: t("Rules") }] : []),
    { key: "settings", label: t("Settings") },
  ];
  const tab = tabs.some((x) => x.key === rawTab) ? rawTab! : "browse";
  const catalogType =
    lib.mediaType || (typeof lib.filter?.type === "string" ? lib.filter.type : "");
  const scopeFilter = lib.filter ? { ...lib.filter, type: undefined } : undefined;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <Workspace
        leading={
          <Button size="sm" variant="ghost" onClick={() => navigate("/libraries")}>
            <ArrowLeft size={14} /> {t("Libraries")}
          </Button>
        }
        title={lib.name}
        actions={
          <>
            {lib.source === "builtin" && catalogHealth ? (
              <LevelBadge
                level={catalogHealth.level}
                label={catalogHealth.level === "ok" ? t("Healthy") : catalogHealth.summary}
              />
            ) : lib.sync?.lastError ? (
              <LevelBadge level="error" label={t("Sync failed")} />
            ) : null}
            {tab === "browse" && lib.source !== "plugin" ? (
              <Button size="sm" variant="outline" onClick={() => setSavingAsLibrary(true)}>
                <Plus size={14} /> {t("Save view as library")}
              </Button>
            ) : null}
          </>
        }
      >
        <SubtabNav
          ariaLabel={t("Library sections")}
          active={tab}
          onSelect={(key) =>
            navigate(key === "browse" ? `/libraries/${id}` : `/libraries/${id}/${key}`)
          }
          tabs={tabs}
        />
        {tab === "browse" ? (
          lib.source === "plugin" ? (
            <PluginBrowse library={lib} />
          ) : (
            <CatalogBrowser
              params={params}
              setParams={setParams}
              refreshHealth={refreshDashboard}
              libraryScope={{
                type: catalogType,
                filter: scopeFilter as Record<string, unknown> | undefined,
              }}
            />
          )
        ) : tab === "rules" ? (
          <CatalogSettings embedded />
        ) : (
          <div className="app-scroll min-h-0 flex-1 overflow-y-auto">
            <div className="mx-auto max-w-5xl space-y-5 px-5 py-5 lg:px-7">
              {tab === "collections" ? (
                <LibraryCollections library={lib} />
              ) : (
                <LibrarySettings library={lib} onSaved={() => void library.reload()} />
              )}
            </div>
          </div>
        )}
      </Workspace>
      {savingAsLibrary ? (
        <FilterLibraryEditor
          seed={new URLSearchParams(params.toString())}
          onClose={() => setSavingAsLibrary(false)}
          onCreated={(created) => {
            setSavingAsLibrary(false);
            navigate(`/libraries/${created.id}`);
          }}
        />
      ) : null}
    </div>
  );
}
