import { Button, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { useEffect, useState } from "react";
import { SettingsRow } from "../../components/SettingsRow";
import { t, tn } from "../../i18n";
import { errorMessage } from "../../lib/api";
import { formatBytes } from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import { librariesApi, type Library } from "./api";

const GIB = 1024 ** 3;

/** One plugin library's blob cache: usage, quota, sweep and purge (§11, §15.5). */
export function LibraryCacheCard({ library }: { library: Library }) {
  const cache = useAsync((signal) => librariesApi.cache(library.id, signal), [library.id]);
  const [quotaGiB, setQuotaGiB] = useState("");
  const [ttlDays, setTtlDays] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (cache.data) {
      setQuotaGiB(String(Math.round((cache.data.maxBytes / GIB) * 10) / 10));
      setTtlDays(cache.data.ttlSeconds ? String(Math.round(cache.data.ttlSeconds / 86400)) : "");
    }
  }, [cache.data]);

  const run = async (fn: () => Promise<unknown>, done: string) => {
    setBusy(true);
    try {
      await fn();
      await cache.reload();
      toast.success(done);
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const data = cache.data;
  return (
    <SettingsSection title={t("Downloaded files")}>
      <SettingsRow
        title={t("Space used")}
        description={
          data
            ? tn(
                data.blobs,
                "{count} file kept for quick opening",
                "{count} files kept for quick opening",
              )
            : t("Loading…")
        }
        control={<span>{data ? formatBytes(data.used) : "—"}</span>}
      />
      <SettingsRow
        title={t("Keep at most")}
        description={t("Oldest files go first when the library passes this size.")}
        control={
          <span className="library-cache-input">
            <input
              type="number"
              min={0}
              step="0.5"
              value={quotaGiB}
              onChange={(e) => setQuotaGiB(e.target.value)}
              aria-label={t("Keep at most")}
            />
            <span>GiB</span>
          </span>
        }
      />
      <SettingsRow
        title={t("Forget files after")}
        description={t("Leave empty to keep files until space is needed.")}
        control={
          <span className="library-cache-input">
            <input
              type="number"
              min={0}
              value={ttlDays}
              onChange={(e) => setTtlDays(e.target.value)}
              aria-label={t("Forget files after")}
            />
            <span>{t("days")}</span>
          </span>
        }
      />
      <div className="flex flex-wrap gap-2 pt-2">
        <Button
          size="sm"
          disabled={busy || !data}
          onClick={() =>
            void run(
              () =>
                librariesApi.setCache(
                  library.id,
                  Math.max(0, Math.round(Number(quotaGiB || 0) * GIB)),
                  Math.max(0, Math.round(Number(ttlDays || 0) * 86400)),
                ),
              t("File limits saved"),
            )
          }
        >
          {t("Save limits")}
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={busy || !data}
          onClick={() =>
            void run(() => librariesApi.sweepCache(library.id), t("Old files cleared"))
          }
        >
          {t("Clear old files")}
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={busy || !data}
          onClick={() => {
            if (
              window.confirm(t("Delete every downloaded file for {name}?", { name: library.name }))
            )
              void run(() => librariesApi.purgeCache(library.id), t("Files deleted"));
          }}
        >
          {t("Delete all files")}
        </Button>
      </div>
    </SettingsSection>
  );
}

/** Cache page section: every plugin library's blob cache in one place. */
export function LibraryCaches() {
  const list = useAsync((signal) => librariesApi.list(signal), []);
  const pluginLibs = (list.data ?? []).filter((l) => l.source === "plugin");
  if (list.loading || pluginLibs.length === 0) return null;
  return (
    <div className="library-caches">
      {pluginLibs.map((lib) => (
        <div key={lib.id}>
          <h3 className="library-caches-name">{lib.name}</h3>
          <LibraryCacheCard library={lib} />
        </div>
      ))}
    </div>
  );
}
