import { Button } from "@braedonsaunders/appkit-ui";
import { Film, Tv, Plug, Link2 } from "lucide-react";
import { LevelBadge } from "../../components/status";
import type { CatalogueSource } from "./api";
import { useState } from "react";
import { Link } from "react-router-dom";
import { AddAddonDrawer } from "../../components/addons/AddAddonDrawer";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { DomainPage } from "../../components/layout";
import { t, tn } from "../../i18n";
import { useAsync } from "../../lib/hooks";
import { useStreamEvent } from "../../lib/stream";
import { librariesApi } from "./api";

export function libraryTabs() {
  return [
    { key: "", label: t("Libraries") },
    { key: "sources", label: t("Catalogue sources") },
  ];
}
export function AddCatalogueSource({
  onClose,
  onAdded,
}: {
  onClose: () => void;
  onAdded: () => void;
}) {
  const [addon, setAddon] = useState(false);
  if (addon)
    return (
      <AddAddonDrawer
        open
        role="list_source"
        onClose={onClose}
        onAdded={() => {
          onAdded();
          onClose();
        }}
      />
    );
  return (
    <Drawer open onClose={onClose} title={t("Add catalogue source")}>
      <div className="flex flex-col gap-3">
        <button
          type="button"
          className="flex items-center gap-4 rounded-xl border border-border p-4 text-left hover:bg-surface-hover"
          onClick={() => setAddon(true)}
        >
          <Link2 size={22} />
          <span>
            <span className="block font-medium">{t("Stremio add-on")}</span>
            <span className="block text-sm text-fg-muted">
              {t("Paste a manifest link to add its catalogues.")}
            </span>
          </span>
        </button>
        <Link
          className="flex items-center gap-4 rounded-xl border border-border p-4 text-left hover:bg-surface-hover"
          to="/plugins/marketplace"
          onClick={onClose}
        >
          <Plug size={22} />
          <span>
            <span className="block font-medium">{t("Plugin")}</span>
            <span className="block text-sm text-fg-muted">
              {t("Install from the plugin directory or build one in Studio.")}
            </span>
          </span>
        </Link>
      </div>
    </Drawer>
  );
}
export function SourceIcon({ source }: { source: CatalogueSource }) {
  return source.group === "plugin" ? (
    <Plug size={18} />
  ) : source.mediaTypes[0] === "show" ? (
    <Tv size={18} />
  ) : (
    <Film size={18} />
  );
}
export function sourceOwner(source: CatalogueSource) {
  return source.group === "builtin"
    ? t("Included with Ether")
    : t("From {plugin}", { plugin: source.owner });
}

export function CatalogueSources() {
  const sources = useAsync((signal) => librariesApi.catalogueSources(signal), []);
  const [adding, setAdding] = useState(false);
  useStreamEvent("plugins.changed", () => {
    void sources.reload();
  });
  return (
    <DomainPage
      title={t("Libraries")}
      description={t("Everything your viewers can open, in one place.")}
      base="/libraries"
      tabs={libraryTabs()}
      active="sources"
      actions={<Button onClick={() => setAdding(true)}>{t("Add catalogue source")}</Button>}
    >
      {sources.error ? (
        <ErrorPanel error={sources.error} onRetry={() => void sources.reload()} />
      ) : null}
      {sources.loading && !sources.data ? <LoadingRows /> : null}
      <p className="mb-4 text-sm text-fg-muted">
        {t("Catalogue sources supply the titles you can choose for your libraries.")}
      </p>
      <div className="libraries-grid">
        {sources.data?.map((source) => (
          <section className="library-card" key={source.id}>
            <div className="library-card-head">
              <span className="library-card-icon">
                <SourceIcon source={source} />
              </span>
              <div className="min-w-0 flex-1">
                <h2 className="library-card-name">{source.name}</h2>
                <p className="library-card-meta">{sourceOwner(source)}</p>
              </div>
              <LevelBadge
                level={source.health === "ok" ? "ok" : "warn"}
                label={source.health === "ok" ? t("Healthy") : t("Needs attention")}
              />
            </div>
            <p className="library-card-meta">
              {source.group === "plugin"
                ? source.name
                : source.mediaTypes
                    .map((type) => (type === "movie" ? t("Movies") : t("TV shows")))
                    .join(", ")}
              {source.itemCount !== undefined
                ? ` · ${tn(source.itemCount, "{count} title", "{count} titles")}`
                : ""}
            </p>
            <p className="text-sm text-fg-muted">
              {source.libraries.length
                ? t("Used by: {libraries}", {
                    libraries: source.libraries.map((lib) => lib.name).join(", "),
                  })
                : t("No libraries use this source yet.")}
            </p>
            <div className="library-card-actions">
              {source.libraries.map((lib) => (
                <Link
                  className="rounded-lg border border-border px-4 py-2 text-sm font-medium hover:bg-surface-hover"
                  key={lib.id}
                  to={`/libraries/${lib.id}`}
                >
                  {t("Open {name}", { name: lib.name })}
                </Link>
              ))}
              {source.pluginId ? (
                <Link
                  className="rounded-lg border border-border px-4 py-2 text-sm font-medium hover:bg-surface-hover"
                  to={`/plugins/installed?plugin=${encodeURIComponent(source.pluginId)}`}
                >
                  {t("Configure")}
                </Link>
              ) : null}
            </div>
          </section>
        ))}
      </div>
      {adding ? (
        <AddCatalogueSource
          onClose={() => setAdding(false)}
          onAdded={() => void sources.reload()}
        />
      ) : null}
    </DomainPage>
  );
}
