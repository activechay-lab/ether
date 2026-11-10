import {
  Alert,
  AlertDescription,
  Button,
  SettingsSection,
  Spinner,
  toast,
} from "@braedonsaunders/appkit-ui";
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { CopyButton } from "../../../components/CopyButton";
import { Drawer } from "../../../components/Drawer";
import { ErrorPanel } from "../../../components/feedback";
import { SettingsRow } from "../../../components/SettingsRow";
import { t, tx } from "../../../i18n";
import { apiFetch, errorMessage } from "../../../lib/api";
import { useAsync } from "../../../lib/hooks";

const DEFAULT_ACTIVATE_URL = "https://trakt.tv/activate";

export type TraktDevice = {
  userCode: string;
  verificationUrl: string;
  expiresAt: string;
  interval: number;
};

export type TraktStatus = {
  configured: boolean;
  connected: boolean;
  username?: string;
  status?: string;
  device?: TraktDevice;
  portalUrl?: string;
};

/** Only allow HTTPS links on trakt.tv (or a subdomain); fall back to Trakt's activate page. */
export function safeActivateUrl(raw?: string): string {
  if (!raw) return DEFAULT_ACTIVATE_URL;
  try {
    const u = new URL(raw);
    const host = u.hostname.toLowerCase();
    if (
      u.protocol === "https:" &&
      !u.username &&
      !u.password &&
      (host === "trakt.tv" || host.endsWith(".trakt.tv"))
    ) {
      return u.href;
    }
  } catch {
    // Fall back to Trakt's standard activation page.
  }
  return DEFAULT_ACTIVATE_URL;
}

function UnconfiguredTrakt() {
  return (
    <SettingsSection title="Trakt">
      <SettingsRow
        title="Trakt"
        description={tx("Set up Trakt in {link} before connecting an account.", {
          link: (
            <Link to="/services/trakt" className="font-medium text-primary hover:underline">
              {t("Integrations › Trakt")}
            </Link>
          ),
        })}
        control={
          <Button variant="outline" disabled>
            {t("Connect")}
          </Button>
        }
      />
    </SettingsSection>
  );
}

/**
 * One viewer's Trakt account on their Limits tab. Connect opens a drawer that
 * runs the OAuth device-code flow with server-paced live polling; app
 * credentials live on Integrations › Trakt.
 */
export function ViewerTrakt({ viewerId, ready }: { viewerId: number; ready: boolean }) {
  if (!ready) return <UnconfiguredTrakt />;
  return <TraktAccount viewerId={viewerId} />;
}

function TraktAccount({ viewerId }: { viewerId: number }) {
  const status = useAsync(
    (signal) => apiFetch<TraktStatus>(`/viewers/${viewerId}/trakt`, { signal }),
    [viewerId],
  );
  const [open, setOpen] = useState(false);
  const [device, setDevice] = useState<TraktDevice | null>(null);
  const [starting, setStarting] = useState(false);
  const [startError, setStartError] = useState<unknown>(null);
  const [outcome, setOutcome] = useState<"expired" | "denied" | null>(null);
  const [busy, setBusy] = useState(false);

  const requestCode = async () => {
    setStarting(true);
    setStartError(null);
    setOutcome(null);
    try {
      const next = await apiFetch<TraktStatus>(`/viewers/${viewerId}/trakt/device`, {
        method: "POST",
      });
      status.setData(next);
      setDevice(next.device ?? null);
    } catch (e) {
      setDevice(null);
      setStartError(e);
    } finally {
      setStarting(false);
    }
  };

  const openConnect = () => {
    setOpen(true);
    void requestCode();
  };

  const disconnect = async () => {
    setBusy(true);
    try {
      const next = await apiFetch<TraktStatus>(`/viewers/${viewerId}/trakt/disconnect`, {
        method: "POST",
      });
      setDevice(null);
      setOutcome(null);
      setOpen(false);
      status.setData(next);
      toast.success(t("Trakt disconnected."));
    } catch (e) {
      toast.error(t("Trakt was not updated"), { description: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    if (!open || !device) return;
    let cancelled = false;
    let timer: number | undefined;
    const waitMs = Math.max(device.interval || 5, 1) * 1000;

    const tick = async () => {
      try {
        const next = await apiFetch<TraktStatus>(`/viewers/${viewerId}/trakt/poll`, {
          method: "POST",
        });
        if (cancelled) return;
        if (next.status === "authorized" || (next.connected && !next.device)) {
          status.setData(next);
          setDevice(null);
          setOutcome(null);
          setOpen(false);
          toast.success(t("Trakt account connected."));
          return;
        }
        if (next.status === "expired") {
          setDevice(null);
          setOutcome("expired");
          return;
        }
        if (next.status === "denied") {
          setDevice(null);
          setOutcome("denied");
          return;
        }
        if (
          next.device &&
          (next.device.interval !== device.interval ||
            next.device.userCode !== device.userCode ||
            next.device.expiresAt !== device.expiresAt)
        ) {
          setDevice(next.device);
          return;
        }
      } catch {
        if (cancelled) return;
      }
      if (!cancelled) {
        timer = window.setTimeout(() => void tick(), waitMs);
      }
    };

    timer = window.setTimeout(() => void tick(), waitMs);
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [open, device, viewerId]);

  if (status.error) return <ErrorPanel error={status.error} onRetry={() => void status.reload()} />;
  if (!status.data) {
    return (
      <div className="py-6 text-center">
        <Spinner size={18} label={t("Loading Trakt")} />
      </div>
    );
  }
  const st = status.data;
  if (!st.configured) return <UnconfiguredTrakt />;

  const activateUrl = safeActivateUrl(device?.verificationUrl);
  const portalHint = st.portalUrl || "/ether/me";

  return (
    <>
      <SettingsSection
        title="Trakt"
        description={t("Syncs this viewer's watch history, ratings and watchlist with Trakt.")}
      >
        <SettingsRow
          title="Trakt"
          description={
            st.connected
              ? st.username
                ? t("Connected as {username}", { username: st.username })
                : t("Connected")
              : t("Not connected")
          }
          control={
            st.connected ? (
              <Button variant="outline" disabled={busy} onClick={() => void disconnect()}>
                {t("Disconnect")}
              </Button>
            ) : (
              <Button variant="outline" disabled={busy} onClick={openConnect}>
                {t("Connect")}
              </Button>
            )
          }
        />
      </SettingsSection>

      <Drawer
        open={open}
        onClose={() => setOpen(false)}
        title={t("Connect Trakt")}
        description={t("Link this viewer's Trakt account.")}
      >
        <div className="space-y-5">
          {starting && !device ? (
            <div className="py-8 text-center">
              <Spinner size={20} label={t("Starting Trakt connection")} />
            </div>
          ) : startError ? (
            <ErrorPanel error={startError} onRetry={() => void requestCode()} />
          ) : outcome ? (
            <div className="space-y-4">
              <Alert variant="destructive" role="alert">
                <AlertDescription>
                  {outcome === "expired"
                    ? t("This code expired. Generate a new code to continue.")
                    : t("Approval was declined on Trakt. Generate a new code to try again.")}
                </AlertDescription>
              </Alert>
              <Button onClick={() => void requestCode()} disabled={starting}>
                {t("Get a new code")}
              </Button>
            </div>
          ) : device ? (
            <>
              <div className="space-y-3 rounded-xl border border-border bg-bg-subtle p-5">
                <p className="text-xs font-medium tracking-wide text-fg-muted uppercase">
                  {t("Activation code")}
                </p>
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <code className="font-mono text-3xl font-semibold tracking-[0.25em] text-fg">
                    {device.userCode}
                  </code>
                  <CopyButton value={device.userCode} label={t("Copy Trakt code")} />
                </div>
                <p className="text-sm text-fg">
                  {tx("Enter this code at {link}.", {
                    link: (
                      <a
                        href={activateUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="font-medium text-primary hover:underline"
                      >
                        {activateUrl}
                      </a>
                    ),
                  })}
                </p>
              </div>

              <div className="space-y-2 text-sm text-fg-muted">
                <p>
                  {t(
                    "Open https://trakt.tv/activate while signed in to this viewer's Trakt account, or send them {portal} so they can connect it themselves.",
                    { portal: portalHint },
                  )}
                </p>
                {st.portalUrl ? (
                  <div>
                    <CopyButton value={st.portalUrl} label={t("Copy viewer portal link")} />
                  </div>
                ) : null}
              </div>

              <div className="flex items-center gap-2 text-sm text-fg-muted" role="status">
                <Spinner size={14} label={t("Waiting for approval on Trakt…")} />
                <span>{t("Waiting for approval on Trakt…")}</span>
              </div>
            </>
          ) : null}
        </div>
      </Drawer>
    </>
  );
}
