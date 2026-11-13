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
