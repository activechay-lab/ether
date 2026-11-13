import { sourceName } from "../../libraries/CatalogueSources";
import { Button, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { SettingsRow } from "../../../components/SettingsRow";
import { t } from "../../../i18n";
import { errorMessage } from "../../../lib/api";
import { formatDateTime } from "../../../lib/format";
import { useAsync } from "../../../lib/hooks";
import { librariesApi } from "../../libraries/api";
export function PluginCatalogueSettings({
  pluginId,
  onSetup,
}: {
  pluginId: string;
  onSetup: () => void;
}) {
  const libraries = useAsync(
    (signal) => librariesApi.setup(pluginId, signal).then((setup) => setup.libraries),
    [pluginId],
  );
  const sources = useAsync((signal) => librariesApi.catalogueSources(signal), [pluginId]);
  const [busy, setBusy] = useState(false);
  const sync = async (id: number, full: boolean) => {
    setBusy(true);
    try {
      await librariesApi.sync(id, full);
      void libraries.reload();
      toast.success(t("Settings saved"));
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  const owned = libraries.data?.filter((lib) => lib.pluginId === pluginId) ?? [];
  const catalogues =
    sources.data?.filter((source) => source.pluginId === pluginId && source.group === "stremio") ??
    [];
  if (!owned.length && !catalogues.length) return null;
  return (
    <SettingsSection title={t("Keeping it up to date")}>
      {owned.map((lib) => (
        <SettingsRow
          key={lib.id}
          title={lib.kind?.label ?? lib.name}
          description={
            lib.sync?.lastError ??
            (lib.sync?.lastOk ? formatDateTime(lib.sync.lastOk) : t("Not synced yet"))
          }
          control={
            <div className="flex flex-wrap gap-2">
              <Button
                size="sm"
                variant="outline"
                disabled={busy}
                onClick={() => void sync(lib.id, true)}
              >
                {t("Full re-sync")}
              </Button>
              <Button size="sm" disabled={busy} onClick={() => void sync(lib.id, false)}>
                {t("Sync now")}
              </Button>
            </div>
          }
        />
      ))}
      {catalogues.map((source) => (
        <SettingsRow
          key={source.id}
          title={sourceName(source)}
          description={
            source.health === "unknown"
              ? t("Not loaded yet")
              : source.health === "ok"
                ? t("Healthy")
                : source.lastError || t("Needs attention")
          }
          control={
            <Button
              size="sm"
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                try {
                  await librariesApi.syncCatalogueSource(source.id);
                  void sources.reload();
                  toast.success(t("Settings saved"));
                } catch (e) {
                  toast.error(errorMessage(e));
                } finally {
                  setBusy(false);
                }
              }}
            >
              {t("Sync now")}
            </Button>
          }
        />
      ))}
      {owned.length ? (
        <SettingsRow
          title={t("Setup")}
          description={t("Re-run the guided setup or check a step any time.")}
          control={
            <Button size="sm" variant="outline" onClick={onSetup}>
              {t("Open setup")}
            </Button>
          }
        />
      ) : null}
    </SettingsSection>
  );
}
