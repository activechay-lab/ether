import { Button, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { useState, useEffect } from "react";
import { useAsync } from "../../lib/hooks";
import { SettingsRow } from "../../components/SettingsRow";
import { t, tn } from "../../i18n";
import { errorMessage } from "../../lib/api";
import { formatBytes } from "../../lib/format";
import { librariesApi, type Library, type LibraryCache } from "./api";
const GIB = 1024 ** 3;
/** Quota and TTL are keyed by library ID, so these stay with the library. */
export function LibraryCacheCard({
  library,
  data,
  maxBytes,
  ttlSeconds,
  onChange,
  onReload,
}: {
  library: Library;
  data: LibraryCache;
  maxBytes: number;
  ttlSeconds: number;
  onChange: (bytes: number, seconds: number) => void;
  onReload: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const run = async (purge: boolean) => {
    if (
      purge &&
      !window.confirm(t("Delete every downloaded file for {name}?", { name: library.name }))
    )
      return;
    setBusy(true);
    try {
      await (purge ? librariesApi.purgeCache(library.id) : librariesApi.sweepCache(library.id));
      onReload();
      toast.success(t("Old files cleared"));
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <SettingsSection title={t("Downloaded files")}>
      <SettingsRow
        title={t("Space used")}
        description={tn(
          data.blobs,
          "{count} file kept for quick opening",
          "{count} files kept for quick opening",
        )}
        control={<span>{formatBytes(data.used)}</span>}
      />
      <SettingsRow
        title={t("Keep at most")}
        description={t("Oldest files go first when the library passes this size.")}
        control={
          <label className="flex items-center gap-2">
            <input
              className="w-24"
              type="number"
              min={0}
              step="0.5"
              value={maxBytes / GIB}
              aria-label={t("Keep at most")}
              onChange={(e) =>
                onChange(Math.round(Math.max(0, Number(e.target.value)) * GIB), ttlSeconds)
              }
            />
            <span>GiB</span>
          </label>
        }
      />
      <SettingsRow
        title={t("Forget files after")}
        description={t("Leave empty to keep files until space is needed.")}
        control={
          <label className="flex items-center gap-2">
            <input
              className="w-24"
              type="number"
              min={0}
              value={ttlSeconds ? ttlSeconds / 86400 : ""}
              aria-label={t("Forget files after")}
              onChange={(e) =>
                onChange(maxBytes, Math.round(Math.max(0, Number(e.target.value)) * 86400))
              }
            />
            <span>{t("days")}</span>
          </label>
        }
      />
      <SettingsRow
        title={t("Downloaded files")}
        control={
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="outline" disabled={busy} onClick={() => void run(false)}>
              {t("Clear old files")}
            </Button>
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => void run(true)}>
              {t("Delete all files")}
            </Button>
          </div>
        }
      />
    </SettingsSection>
  );
}

export function LibraryCaches() {
  const list = useAsync((signal) => librariesApi.list(signal), []);
  return (
    <div className="space-y-5">
      {list.data
        ?.filter((lib) => lib.source === "plugin")
        .map((library) => (
          <StandaloneCache key={library.id} library={library} />
        ))}
    </div>
  );
}
function StandaloneCache({ library }: { library: Library }) {
  const cache = useAsync((signal) => librariesApi.cache(library.id, signal), [library.id]);
  const [bytes, setBytes] = useState(0);
  const [ttl, setTTL] = useState(0);
  useEffect(() => {
    if (cache.data) {
      setBytes(cache.data.maxBytes);
      setTTL(cache.data.ttlSeconds);
    }
  }, [cache.data]);
  if (!cache.data) return null;
  return (
    <div className="space-y-3">
      <h3 className="font-semibold">{library.name}</h3>
      <LibraryCacheCard
        library={library}
        data={cache.data}
        maxBytes={bytes}
        ttlSeconds={ttl}
        onChange={(nextBytes, nextTTL) => {
          setBytes(nextBytes);
          setTTL(nextTTL);
        }}
        onReload={() => void cache.reload()}
      />
      <Button
        size="sm"
        disabled={bytes === cache.data.maxBytes && ttl === cache.data.ttlSeconds}
        onClick={() =>
          void librariesApi
            .setCache(library.id, bytes, ttl)
            .then(() => cache.reload())
            .catch((e) => toast.error(errorMessage(e)))
        }
      >
        {t("Save limits")}
      </Button>
    </div>
  );
}
