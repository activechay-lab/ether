import { Badge, Button, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useAppOptional } from "../app-context";
import { t } from "../i18n";
import { apiFetch, errorMessage } from "../lib/api";
import { LoadingRows } from "./feedback";
import { SettingsRow } from "./SettingsRow";

function diagnosticText(value: string) {
  switch (value) {
    case "Plex Media Server and Ether both want port 32400":
      return t("Plex Media Server and Ether both want port 32400");
    case "UPnP mapped the old port":
      return t("UPnP mapped the old port");
    case "Choose another listening port in Apps › Plex.":
      return t("Choose another listening port in Apps › Plex.");
    case "Turn automatic port mapping off and on, or update the router forward to Ether's current listening port.":
      return t(
        "Turn automatic port mapping off and on, or update the router forward to Ether's current listening port.",
      );
    case "Listening port":
      return t("Listening port");
    default:
      return value;
  }
}

type Data = {
  steps: { name: string; state: string; detail: string; action: string }[];
  compatibilityVersion: string;
  latestPublicVersion: string;
};

function StepBadge({ state }: { state: string }) {
  if (state === "ok") return <Badge variant="success">{t("Ready")}</Badge>;
  if (state === "unknown") return <Badge variant="secondary">{t("Not verified")}</Badge>;
  return <Badge variant="warning">{t("Check settings")}</Badge>;
}

/**
 * Step-by-step checks for how Plex apps find and reach this server: discovery,
 * account access, port forwarding and secure connections.
 */
export function RemoteDiagnostics() {
  const app = useAppOptional();
  const [data, setData] = useState<Data | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const load = useCallback(
    () =>
      apiFetch<Data>("/safety/diagnostics/").then(
        (d) => {
          setData(d);
          setError(null);
        },
        (e) => setError(e),
      ),
    [],
  );
  useEffect(() => {
    void load();
  }, [load]);
  const refresh = async () => {
    setBusy(true);
    try {
      await apiFetch("/safety/diagnostics/refresh", { method: "POST", body: "{}" });
      await load();
      void app?.refreshDashboard();
      toast.success(t("Remote checks scheduled"));
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <SettingsSection
      title={t("Connection checks")}
      description={t(
        "How Plex apps find and reach this server. Outside reachability uses Plex's own connectivity result.",
      )}
      footer={
        <Button variant="outline" size="sm" disabled={busy} onClick={() => void refresh()}>
          <RefreshCw size={14} /> {busy ? t("Checking…") : t("Run checks again")}
        </Button>
      }
    >
      {error && !data ? (
        <p className="px-5 py-4 text-sm text-fg-muted">
          {t("Connection checks aren't available right now: {error}", {
            error: errorMessage(error),
          })}
        </p>
      ) : !data ? (
        <div className="px-5 py-4">
          <LoadingRows rows={3} />
        </div>
      ) : data.steps.length === 0 ? (
        <p className="px-5 py-4 text-sm text-fg-muted">{t("No checks have run yet.")}</p>
      ) : (
        data.steps.map((step) => (
          <SettingsRow
            key={step.name}
            title={diagnosticText(step.name)}
            description={
              <>
                {diagnosticText(step.detail)}
                {step.state !== "ok" && step.action ? (
                  <span className="mt-1 block text-fg">{diagnosticText(step.action)}</span>
                ) : null}
              </>
            }
            control={<StepBadge state={step.state} />}
          />
        ))
      )}
    </SettingsSection>
  );
}
