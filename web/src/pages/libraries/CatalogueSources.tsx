import { Film, Tv, Plug, Link2 } from "lucide-react";
import type { CatalogueSource } from "./api";
import { useState } from "react";
import { Link } from "react-router-dom";
import { AddAddonDrawer } from "../../components/addons/AddAddonDrawer";
import { Drawer } from "../../components/Drawer";
import { t } from "../../i18n";

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
        title={t("Add a Stremio add-on")}
        submitLabel={t("Add add-on")}
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

export function sourceName(source: CatalogueSource) {
  if (source.group !== "stremio") return source.name;
  return source.mediaTypes[0] === "show"
    ? t("{name} TV shows", { name: source.name })
    : t("{name} movies", { name: source.name });
}
export function sourceLabel(source: CatalogueSource) {
  return source.group === "builtin"
    ? sourceName(source)
    : `${source.owner} · ${sourceName(source)}`;
}

export function catalogueGroups(sources: CatalogueSource[]) {
  const groups = new Map<string, CatalogueSource[]>();
  for (const source of sources) {
    const key = source.pluginId ?? source.id;
    groups.set(key, [...(groups.get(key) ?? []), source]);
  }
  return [...groups.values()].map((entries) => {
    const first = entries[0]!;
    return {
      ...first,
      id: first.pluginId ?? first.id,
      name: first.owner,
      entries,
      health: entries.some((s) => s.health === "error" || s.health === "attention")
        ? "error"
        : entries.every((s) => s.health === "unknown")
          ? "unknown"
          : "ok",
      lastError: entries.find((s) => s.lastError)?.lastError,
      libraries: [
        ...new Map(entries.flatMap((s) => s.libraries).map((lib) => [lib.id, lib])).values(),
      ],
    };
  });
}
