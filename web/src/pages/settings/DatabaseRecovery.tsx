import { Alert, AlertDescription, Button } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link } from "react-router-dom";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Section } from "../../components/layout";
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
  return (
    <Section
      title={t("Database check")}
      actions={
        db.clustered === false ? (
          <Button size="sm" variant="outline" disabled={checking} onClick={() => void check()}>
            {checking ? t("Checking…") : t("Check again")}
          </Button>
        ) : undefined
      }
    >
      {status.error ? (
        <ErrorPanel error={status.error} onRetry={() => void status.reload()} />
      ) : null}
      {status.loading ? (
        <LoadingRows rows={2} />
      ) : (
        <div className="space-y-3 text-sm">
          <Alert variant={health?.level === "error" ? "warning" : "info"} role="status">
            <AlertDescription>
              <p className="font-medium">{health?.summary ?? t("Not checked")}</p>
              {db.clustered === true ? (
                <>
                  {health?.details.map((line, i) => (
                    <p className="break-words" key={i}>
                      {line}
                    </p>
                  ))}
                  <Link to="/cluster" className="underline">
                    {t("Cluster")}
                  </Link>
                </>
              ) : (
                <>
                  {integrity.checked ? (
                    <p>
                      {t("Checked")}: {formatDateTime(integrity.checked)}
                    </p>
                  ) : null}
                  {integrity.checked ? (
                    <p>
                      {t(
                        "This is the last reported result, not a live diagnosis. A check that could not run may leave an older result here.",
                      )}
                    </p>
                  ) : null}
                </>
              )}
            </AlertDescription>
          </Alert>
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
        </div>
      )}
      {checkError ? <ErrorPanel error={checkError} onRetry={() => void check()} /> : null}
    </Section>
  );
}
