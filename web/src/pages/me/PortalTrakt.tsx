import {
  Alert,
  AlertDescription,
  Button,
  SettingsSection,
  Spinner,
} from "@braedonsaunders/appkit-ui";
import { useCallback, useEffect, useState } from "react";
import { CopyButton } from "../../components/CopyButton";
import { SettingsRow } from "../../components/SettingsRow";
import { t, tx } from "../../i18n";
import {
  safeActivateUrl,
  type TraktDevice,
  type TraktStatus,
} from "../viewers/sections/ViewerTrakt";

export function PortalTrakt({ token }: { token: string }) {
  return <PortalTraktSession key={token} token={token} />;
}

function PortalTraktSession({ token }: { token: string }) {
  const [status, setStatus] = useState<TraktStatus | null>(null);
  const [device, setDevice] = useState<TraktDevice | null>(null);
  const [outcome, setOutcome] = useState<"expired" | "denied" | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const call = useCallback(
    async (subpath: string, method: "GET" | "POST"): Promise<TraktStatus> => {
      const res = await fetch(`/ether/api/me/trakt${subpath}`, {
        method,
        headers: {
          Authorization: `Bearer ${token}`,
          ...(method === "POST" ? { "X-Ether-Request": "1" } : {}),
        },
      });
      if (!res.ok) {
        if (res.status === 429) {
          throw new Error(t("Too many tries. Wait a moment, then try again."));
        }
        throw new Error(t("The request could not be completed. Try again."));
      }
      return (await res.json()) as TraktStatus;
    },
    [token],
  );

  useEffect(() => {
    let active = true;
    void call("", "GET")
      .then((next) => {
        if (!active) return;
        setStatus(next);
        if (next.device && !next.connected) setDevice(next.device);
      })
      .catch(() => {
        // Keep the portal clean if Trakt status cannot be loaded.
      });
    return () => {
      active = false;
    };
  }, [call]);

  const start = async () => {
    setBusy(true);
    setError("");
    setOutcome(null);
    try {
      const next = await call("/device", "POST");
      setStatus(next);
      setDevice(next.device ?? null);
    } catch (e) {
      setError(
        e instanceof Error ? e.message : t("The request could not be completed. Try again."),
      );
    } finally {
      setBusy(false);
    }
  };

  const disconnect = async () => {
    setBusy(true);
    setError("");
    setOutcome(null);
    try {
      const next = await call("/disconnect", "POST");
      setDevice(null);
      setStatus(next);
    } catch (e) {
      setError(
        e instanceof Error ? e.message : t("The request could not be completed. Try again."),
      );
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    if (!device) return;
    let cancelled = false;
    let timer: number | undefined;
    const waitMs = Math.max(device.interval || 5, 1) * 1000;

    const tick = async () => {
      try {
        const next = await call("/poll", "POST");
        if (cancelled) return;
        if (next.status === "authorized" || (next.connected && !next.device)) {
          setStatus(next);
          setDevice(null);
          setOutcome(null);
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
  }, [call, device]);

  if (!status || status.configured !== true) return null;

  const activateUrl = safeActivateUrl(device?.verificationUrl);

  return (
    <SettingsSection
      title="Trakt"
      description={t("Sync your watch history, ratings and watchlist with Trakt.")}
    >
      <SettingsRow
        title="Trakt"
        description={
          status.connected
            ? status.username
              ? t("Connected as {username}", { username: status.username })
              : t("Connected")
            : t("Not connected")
        }
        control={
          status.connected ? (
            <Button variant="outline" disabled={busy} onClick={() => void disconnect()}>
              {t("Disconnect")}
            </Button>
          ) : device ? (
            <Button variant="outline" disabled={busy} onClick={() => void disconnect()}>
              {t("Cancel")}
            </Button>
          ) : (
            <Button disabled={busy} onClick={() => void start()}>
              {t("Connect")}
            </Button>
          )
        }
      />

      {error ? (
        <div className="px-5 py-3">
          <Alert variant="destructive" role="alert">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        </div>
      ) : null}

      {outcome ? (
        <div className="space-y-3 px-5 py-4">
          <Alert variant="destructive" role="alert">
            <AlertDescription>
              {outcome === "expired"
                ? t("This code expired. Generate a new code to continue.")
                : t("Approval was declined on Trakt. Generate a new code to try again.")}
            </AlertDescription>
          </Alert>
          <Button onClick={() => void start()} disabled={busy}>
            {t("Get a new code")}
          </Button>
        </div>
      ) : null}

      {device ? (
        <div className="space-y-3 px-5 py-4">
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-bg-subtle px-4 py-3">
            <code className="font-mono text-2xl font-semibold tracking-[0.25em] text-fg">
              {device.userCode}
            </code>
            <CopyButton value={device.userCode} label={t("Copy Trakt code")} />
          </div>
          <p className="text-sm text-fg">
            {tx("Open {link} and enter this code to connect your Trakt account.", {
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
          <div className="flex items-center gap-2 text-sm text-fg-muted" role="status">
            <Spinner size={14} label={t("Waiting for approval on Trakt…")} />
            <span>{t("Waiting for approval on Trakt…")}</span>
          </div>
        </div>
      ) : null}
    </SettingsSection>
  );
}
