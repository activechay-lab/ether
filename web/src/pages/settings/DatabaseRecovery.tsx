import { Alert, AlertDescription, Badge, Button } from "@braedonsaunders/appkit-ui";
import { Check } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { ErrorPanel } from "../../components/feedback";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { formatDateTime, isPlainObject } from "../../lib/format";
import { databaseHealth } from "../../lib/health";
import { useAsync } from "../../lib/hooks";

type Status = { value: unknown };

/** Independent of navigation state: direct Backups links get the same diagnosis. */
export function DatabaseRecovery() {
  const status = useAsync((signal) => apiFetch<Status>("/status/database", { signal }), []);
  const [checking, setChecking] = useState(false);
  const [checkError, setCheckError] = useState<unknown>(null);
  const value = status.data?.value;
  const db = isPlainObject(value) ? value : {};
  const integrity = isPlainObject(db.integrity) ? db.integrity : {};
  const health = databaseHealth(value);
  const issue = db.clustered !== true && integrity.skipped !== true && integrity.ok === false;
  const findings = Array.isArray(integrity.findings)
    ? integrity.findings.filter((line): line is string => typeof line === "string")
    : [];
  const check = async () => {
    setChecking(true);
    setCheckError(null);
    try {
      status.setData(await apiFetch<Status>("/status/database/check", { method: "POST" }));
    } catch (error) {
      setCheckError(error);
    } finally {
      setChecking(false);
    }
  };
  const healthy = health?.level === "ok" && !status.error && !checkError;
  const needsAttention = issue || health?.level === "error" || health?.level === "warn";
  const label =
    checking || status.loading
      ? t("Checking…")
      : status.error || checkError || needsAttention
        ? t("Needs attention")
        : healthy && db.clustered !== true
          ? t("Healthy")
          : (health?.summary ?? t("Not checked"));
  const checkedTitle = integrity.checked
    ? `${t("Checked")}: ${formatDateTime(integrity.checked)}`
    : undefined;
  const showDetails = status.error || checkError || needsAttention || db.clustered === true;
  return (
    <section
      className="rounded-xl border border-border bg-surface"
      aria-label={t("Database check")}
    >
      <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
        <h3 className="text-sm font-semibold">{t("Database check")}</h3>
        <div className="flex flex-wrap items-center gap-3">
          <span role="status" title={checkedTitle}>
            <Badge
              variant={
                checking || status.loading
                  ? "secondary"
                  : healthy
                    ? "success"
                    : needsAttention || status.error || checkError
                      ? "warning"
                      : "secondary"
              }
            >
              {healthy && !checking ? <Check size={13} aria-hidden /> : null}
              {label}
            </Badge>
          </span>
          {db.clustered === false ? (
            <Button size="sm" variant="ghost" disabled={checking} onClick={() => void check()}>
              {checking ? t("Checking…") : t("Check again")}
            </Button>
          ) : null}
        </div>
      </div>
      {showDetails ? (
        <div className="space-y-3 border-t border-border p-5 text-sm">
          {status.error ? (
            <ErrorPanel error={status.error} onRetry={() => void status.reload()} />
          ) : null}
          {needsAttention ? (
            <Alert variant="warning">
              <AlertDescription>
                <p className="font-medium">{health?.summary}</p>
                {db.clustered === true
                  ? health?.details.map((line, i) => (
                      <p className="break-words" key={i}>
                        {line}
                      </p>
                    ))
                  : null}
              </AlertDescription>
            </Alert>
          ) : null}
          {db.clustered === true ? (
            <Link to="/cluster" className="underline">
              {t("Cluster")}
            </Link>
          ) : null}
          {issue ? (
            <>
              <ul className="list-disc space-y-1 pl-5 break-words font-mono text-xs">
                {findings.map((line, i) => (
                  <li key={i}>{line}</li>
                ))}
              </ul>
              <p>
                {t(
                  "Check again before choosing recovery. If the issue persists, preserve your data folder and use a known-good backup from before the issue. Restoring replaces current data; review the restore plan first.",
                )}
              </p>
              <p className="text-fg-muted">
                {t(
                  "Creating a snapshot does not repair the database. Without a known-good backup, preserve the data folder and seek help with the findings before attempting recovery.",
                )}
              </p>
            </>
          ) : null}
          {checkError ? <ErrorPanel error={checkError} onRetry={() => void check()} /> : null}
        </div>
      ) : null}
    </section>
  );
}
