import { Button, toast } from "@braedonsaunders/appkit-ui";
import { Archive } from "lucide-react";
import { useState } from "react";
import { t } from "../../../i18n";
import { apiFetch, errorMessage } from "../../../lib/api";
import { useAsync, useInterval } from "../../../lib/hooks";
import { tierMigrationLine, type TierMigration } from "../../../lib/health";

/**
 * The one-off library tidy after the two-tier upgrade: an Off install's
 * long-tail rows move to search-only in the background. The owner can pause
 * and resume it; it is shown only while it has work or just finished.
 */
export function TierMigrationStatus() {
  const data = useAsync(
    () => apiFetch<{ value: { tierMigration?: TierMigration } }>("/status/catalog"),
    [],
  );
  const [busy, setBusy] = useState(false);
  const m = data.data?.value.tierMigration;
  useInterval(() => void data.reload(), m && m.state !== "done" ? 5000 : 60000);
  const line = tierMigrationLine(m);
  if (!m || !line) return null;
  const toggle = async () => {
    setBusy(true);
    try {
      await apiFetch("/catalog/tier-migration", {
        method: "POST",
        body: JSON.stringify({ paused: !m.paused }),
      });
      await data.reload();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <section
      className="rounded-xl border border-border bg-surface p-5"
      aria-label={t("Tidying the library")}
    >
      <h3 className="flex items-center gap-2 text-sm font-semibold">
        <Archive size={18} aria-hidden />
        {t("Tidying the library")}
      </h3>
      <p className="mt-3 text-sm">{line}</p>
      {m.state !== "done" ? (
        <div className="mt-4">
          <Button size="sm" variant="secondary" disabled={busy} onClick={() => void toggle()}>
            {m.paused ? t("Resume") : t("Pause")}
          </Button>
        </div>
      ) : null}
    </section>
  );
}
