import { Button, Input, SettingsSection, Spinner, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link } from "react-router-dom";
import { ErrorPanel } from "../../../components/feedback";
import { SettingsRow } from "../../../components/SettingsRow";
import { localized, t, tn, tx } from "../../../i18n";
import { apiFetch, errorMessage } from "../../../lib/api";
import { formatNumber } from "../../../lib/format";
import { useAsync } from "../../../lib/hooks";

type Status = {
  connected: boolean;
  needsReconnect?: boolean;
  lastSync?: string;
  warning?: string;
  queued?: number;
  backoffUntil?: number;
};

/** Human text for the sync warning codes the server reports. */
const WARNINGS = localized<Record<string, string>>(() => ({
  auth_rejected: t("MDBList rejected the stored API key. Paste a new one to reconnect."),
  queue_full: t("The delivery queue is full; some completions were not queued."),
  not_found: t("MDBList could not resolve a history item."),
}));

/**
 * One viewer's MDBList account, on their Limits tab. The viewer pastes their
 * own API key; the on/off switch lives on Integrations › MDBList.
 */
export function ViewerMDBList({ viewerId, ready }: { viewerId: number; ready: boolean }) {
  if (!ready) {
    return (
      <SettingsSection title="MDBList">
        <p className="px-5 py-4 text-sm text-fg-muted">
          {tx("Turn on MDBList in {link} to connect this viewer's account.", {
            link: (
              <Link to="/services/mdblist" className="font-medium text-primary hover:underline">
                {t("Integrations")}
              </Link>
            ),
          })}
        </p>
      </SettingsSection>
    );
  }
  return <MDBListAccount viewerId={viewerId} />;
}

function MDBListAccount({ viewerId }: { viewerId: number }) {
  const status = useAsync(
    (signal) => apiFetch<Status>(`/mdblist/${viewerId}/status`, { signal }),
    [viewerId],
  );
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);

  const post = <T,>(path: string, body?: unknown) =>
    apiFetch<T>(`/mdblist/${viewerId}/${path}`, {
      method: "POST",
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  const refresh = async () => status.setData(await apiFetch<Status>(`/mdblist/${viewerId}/status`));
  const act = async (fn: () => Promise<void>) => {
    setBusy(true);
    try {
      await fn();
    } catch (e) {
      toast.error(t("MDBList was not updated"), { description: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  if (status.error) return <ErrorPanel error={status.error} onRetry={() => void status.reload()} />;
  if (!status.data) {
    return (
      <div className="py-6 text-center">
        <Spinner size={18} label={t("Loading MDBList")} />
      </div>
    );
  }
  const st = status.data;
  const connected = st.connected && !st.needsReconnect;
  return (
    <SettingsSection
      title="MDBList"
      description={t("Imports this viewer's watched history and sends what they finish.")}
    >
      <SettingsRow
        title={t("Connection")}
        description={
          st.connected
            ? st.needsReconnect
              ? t("Reconnect required")
              : t("Connected")
            : t("Not connected")
        }
        control={
          st.connected ? (
            <Button
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  await post("disconnect");
                  await refresh();
                  toast.success(t("MDBList disconnected."));
                })
              }
            >
              {t("Disconnect")}
            </Button>
          ) : null
        }
      />
      {!connected ? (
        <SettingsRow
          title={t("API key")}
          description={t(
            "Paste this viewer's MDBList API key from MDBList Preferences. It is stored encrypted and sent only to MDBList.",
          )}
          control={
            <div className="flex flex-wrap items-center gap-2">
              <Input
                type="password"
                value={key}
                autoComplete="off"
                placeholder={t("API key")}
                onChange={(e) => setKey(e.target.value)}
              />
              <Button
                disabled={busy || !key.trim()}
                onClick={() =>
                  void act(async () => {
                    await post("connect", { apiKey: key.trim() });
                    setKey("");
                    await refresh();
                    toast.success(t("MDBList account connected."));
                  })
                }
              >
                {t("Connect")}
              </Button>
            </div>
          }
        />
      ) : null}
      {connected ? (
        <SettingsRow
          title={t("Sync history")}
          description={t(
            "Imports existing watched history once. Ongoing sync sends what this viewer finishes and never removes watched items.",
          )}
          control={
            <Button
              disabled={busy}
              onClick={() =>
                void act(async () => {
                  const r = await post<{ imported: number; skipped: number }>("sync");
                  await refresh();
                  toast.success(
                    tn(
                      r.imported,
                      "Processed {count} history entry; skipped {skipped}.",
                      "Processed {count} history entries; skipped {skipped}.",
                      { skipped: formatNumber(r.skipped) },
                    ),
                  );
                })
              }
            >
              {t("Sync history")}
            </Button>
          }
        />
      ) : null}
      {st.queued ? (
        <p className="py-2 text-sm">
          {t("Queued history entries: {count}", { count: formatNumber(st.queued) })}
        </p>
      ) : null}
      {st.lastSync ? (
        <p className="py-2 text-sm text-fg-muted">
          {t("Last imported cursor: {when}", { when: st.lastSync })}
        </p>
      ) : null}
      {st.backoffUntil && st.backoffUntil > Date.now() ? (
        <p className="py-2 text-sm text-fg-muted">
          {t("Sync paused until {when}.", {
            when: new Date(st.backoffUntil).toLocaleString(),
          })}
        </p>
      ) : null}
      {st.warning ? (
        <p className="py-2 text-sm text-fg-muted">
          {WARNINGS[st.warning] ?? t("A history item could not be sent.")}
        </p>
      ) : null}
    </SettingsSection>
  );
}
