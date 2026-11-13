import { Button } from "@braedonsaunders/appkit-ui";
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
  return [{key: "", label: t("Libraries")}, {key: "sources", label: t("Catalogue sources")}];
}
export function AddCatalogueSource({onClose, onAdded}: {onClose: () => void; onAdded: () => void}) {
  const [addon, setAddon] = useState(false);
  if (addon) return <AddAddonDrawer open role="list_source" onClose={onClose} onAdded={() => {onAdded(); onClose();}} />;
  return <Drawer open onClose={onClose} title={t("Add catalogue source")}>
    <div className="flex flex-col gap-4">
      <Button onClick={() => setAddon(true)}>{t("Stremio add-on")}</Button>
      <p className="text-sm text-fg-muted">{t("Paste a manifest link to add its catalogues.")}</p>
      <Link className="rounded-lg border border-border p-4 text-primary" to="/plugins/marketplace" onClick={onClose}>{t("Plugin")}</Link>
    </div>
  </Drawer>;
}
export function CatalogueSources() {
  const sources = useAsync(signal => librariesApi.catalogueSources(signal), []);
  const [adding, setAdding] = useState(false);
  useStreamEvent("plugins.changed", () => {void sources.reload();});
  return <DomainPage title={t("Libraries")} base="/libraries" tabs={libraryTabs()} active="sources" actions={<Button onClick={() => setAdding(true)}>{t("Add catalogue source")}</Button>}>
    {sources.error ? <ErrorPanel error={sources.error} onRetry={() => void sources.reload()} /> : null}
    {sources.loading && !sources.data ? <LoadingRows /> : null}
    <div className="libraries-grid">{sources.data?.map(source => <section className="library-card" key={source.id}>
      <h2 className="font-medium text-fg">{source.name}</h2>
      <p className="text-sm text-fg-muted">{t("From {plugin}", {plugin: source.owner})}</p>
      <p className="text-sm">{source.mediaTypes.map(type => type === "movie" ? t("Movies") : type === "show" ? t("TV shows") : type).join(", ")}</p>
      {source.itemCount !== undefined ? <p>{tn(source.itemCount, "{count} title", "{count} titles")}</p> : null}
      <p className="text-sm text-fg-muted">{source.health === "ok" ? t("Healthy") : t("Needs attention")}</p>
      <div className="flex flex-wrap gap-2">{source.libraries.map(lib => <Link className="text-primary underline" key={lib.id} to={`/libraries/${lib.id}`}>{lib.name}</Link>)}</div>
      {source.pluginId ? <Link className="text-primary underline" to={`/plugins/installed?plugin=${encodeURIComponent(source.pluginId)}`}>{t("Configure")}</Link> : null}
    </section>)}</div>
    {adding ? <AddCatalogueSource onClose={() => setAdding(false)} onAdded={() => void sources.reload()} /> : null}
  </DomainPage>;
}
