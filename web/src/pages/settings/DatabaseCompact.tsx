import { Alert, AlertDescription, Button } from "@braedonsaunders/appkit-ui";
import { useEffect, useState } from "react";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Section } from "../../components/layout";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { formatBytes, formatDateTime } from "../../lib/format";
import { useAsync } from "../../lib/hooks";

type CompactStatus = {
  supported: boolean;
  incremental: boolean;
  databaseBytes: number;
  reclaimableBytes: number;
  needBytes: number;
  diskFreeBytes: number;
  tempNeedBytes: number;
  tempFreeBytes: number;
  estimateSeconds: number;
  ready: boolean;
  blocked?: "incremental" | "disk" | "temp" | "playing";
  running: boolean;
  started?: string;
  last?: { finished: string; tookSeconds: number; beforeBytes: number; afterBytes: number; error?: string };
};

function minutes(seconds: number): string {
  return seconds < 90
    ? t("about {value} seconds", { value: Math.max(5, Math.round(seconds / 5) * 5) })
    : t("about {value} minutes", { value: Math.ceil(seconds / 60) });
}

/**
 * Databases created before Ether returned free space on its own keep their
 * size after old data is removed. One compaction fixes that for good; it is
 * never run automatically because it rewrites the whole file.
 */
export function DatabaseCompact() {
  const status = useAsync(
    (signal) => apiFetch<{ value: CompactStatus }>("/status/database/compact", { signal }),
    [],
  );
  const [confirming, setConfirming] = useState(false);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<unknown>(null);
  const s = status.data?.value;
  const running = s?.running === true;

  useEffect(() => {
    if (!running) return;
    const id = window.setInterval(() => void status.reload(), 3000);
    return () => window.clearInterval(id);
  }, [running, status.reload]);

  if (s && !s.supported) return null;

  const start = async () => {
    setStarting(true);
    setStartError(null);
    try {
      status.setData(
        await apiFetch<{ value: CompactStatus }>("/status/database/compact", { method: "POST" }),
      );
      setConfirming(false);
    } catch (error) {
      setStartError(error);
    } finally {
      setStarting(false);
    }
  };

  const blockedText = (b: CompactStatus["blocked"]) => {
    switch (b) {
      case "disk":
        return t("Not enough free disk space: {need} is needed beside the database, {free} is free.", {
          need: formatBytes(s?.needBytes),
          free: formatBytes(s?.diskFreeBytes),
        });
      case "temp":
        return t("Not enough free space in the temporary folder: {need} is needed, {free} is free.", {
          need: formatBytes(s?.tempNeedBytes),
          free: formatBytes(s?.tempFreeBytes),
        });
      case "playing":
        return t("Someone is watching. Compacting pauses saving changes while it runs, so start it when nothing is playing.");
      default:
        return null;
    }
  };

  return (
    <Section
      title={t("Database space")}
      actions={
        s && !s.incremental && !running && !confirming ? (
          <Button size="sm" variant="outline" onClick={() => setConfirming(true)}>
            {t("Compact database")}
          </Button>
        ) : undefined
      }
    >
      {status.error ? <ErrorPanel error={status.error} onRetry={() => void status.reload()} /> : null}
      {status.loading && !s ? (
        <LoadingRows rows={2} />
      ) : s ? (
        <div className="space-y-3 text-sm">
          <p>
            {t("Database size")}: {formatBytes(s.databaseBytes)}
            {s.reclaimableBytes > 0 ? (
              <span className="text-fg-muted">
                {" "}
                · {t("{value} unused", { value: formatBytes(s.reclaimableBytes) })}
              </span>
            ) : null}
          </p>
          {s.incremental ? (
            <p className="text-fg-muted">
              {t("Space freed by removing old data is returned to the disk automatically every day.")}
            </p>
          ) : (
            <p className="text-fg-muted">
              {t(
                "This database was created before Ether returned unused space on its own, so it never shrinks. Compacting it once rewrites the file and turns that on for good.",
              )}
            </p>
          )}
          {running ? (
            <Alert variant="info" role="status">
              <AlertDescription>
                <p className="font-medium">{t("Compacting the database…")}</p>
                <p>
                  {t("Started")}: {formatDateTime(s.started)} · {minutes(s.estimateSeconds)}
                </p>
                <p>{t("Browsing keeps working. Changes made meanwhile may wait or fail and need repeating.")}</p>
              </AlertDescription>
            </Alert>
          ) : null}
          {confirming && !running ? (
            <Alert variant="warning" role="status">
              <AlertDescription className="space-y-2">
                <p className="font-medium">{t("Compact the database now?")}</p>
                <p>
                  {t("It takes {time} and needs {need} of free disk while it runs ({free} free).", {
                    time: minutes(s.estimateSeconds),
                    need: formatBytes(s.needBytes + s.tempNeedBytes),
                    free: formatBytes(s.diskFreeBytes),
                  })}
                </p>
                <p>
                  {t(
                    "Browsing and playback keep working. Saving changes pauses until it finishes: watch progress is kept and saved afterwards, other changes made meanwhile may fail and need repeating.",
                  )}
                </p>
                {blockedText(s.blocked) ? <p className="font-medium">{blockedText(s.blocked)}</p> : null}
                <div className="flex gap-2">
                  <Button size="sm" disabled={!s.ready || starting} onClick={() => void start()}>
                    {starting ? t("Starting…") : t("Start compacting")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setConfirming(false)}>
                    {t("Cancel")}
                  </Button>
                </div>
              </AlertDescription>
            </Alert>
          ) : null}
          {s.last && !running ? (
            s.last.error ? (
              <p className="text-danger">
                {t("The last compaction failed: {error}", { error: s.last.error })}
              </p>
            ) : (
              <p className="text-fg-muted">
                {t("Compacted {when}: {before} → {after}.", {
                  when: formatDateTime(s.last.finished),
                  before: formatBytes(s.last.beforeBytes),
                  after: formatBytes(s.last.afterBytes),
                })}
              </p>
            )
          ) : null}
        </div>
      ) : null}
      {startError ? <ErrorPanel error={startError} onRetry={() => void start()} /> : null}
    </Section>
  );
}
