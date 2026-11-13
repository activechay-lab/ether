import { CopyButton } from "../../components/CopyButton";
import { useEffect, useId, useState } from "react";
import { Button, Dialog, Input, Label, buttonVariants } from "@braedonsaunders/appkit-ui";
import {
  PlexMark,
  JellyfinMark,
  EmbyMark,
  SiloMark,
  householdIcon,
} from "../../components/integration-brands";
import { StremioMark } from "../sources/marks";
import { t } from "../../i18n";

type Connection = {
  surface: string;
  url: string;
  username?: string;
  password?: string;
  qr: string;
};
const names: Record<string, string> = {
  plex: "Plex",
  jellyfin: "Jellyfin",
  emby: "Emby",
  silo: "Silo",
  stremio: "Stremio",
  webdav: "WebDAV",
  m3u: "M3U",
  xtream: "Xtream",
};

export function ConnectApps(props: { base: string; token: string }) {
  // A secret belongs to one viewer, even if a mint finishes after navigation.
  return <ConnectAppsSession key={`${props.base}\u0000${props.token}`} {...props} />;
}

function ConnectAppsSession({ base, token }: { base: string; token: string }) {
  const titleId = useId();
  const [apps, setApps] = useState<string[]>([]);
  const [connection, setConnection] = useState<Connection>();

  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let active = true;
    void fetch(`/ether/api${base}/apps`, { headers: { Authorization: `Bearer ${token}` } })
      .then((response) =>
        response.ok ? (response.json() as Promise<{ apps: string[] }>) : Promise.reject(),
      )
      .then((result) => {
        if (active) setApps(Array.isArray(result.apps) ? result.apps : []);
      })
      .catch(() => {
        if (active) setError(t("The request could not be completed. Try again."));
      });
    return () => {
      active = false;
    };
  }, [base, token]);
  async function create(surface: string) {
    setConnection(undefined);
    setError("");
    setBusy(true);
    try {
      const response = await fetch(`/ether/api${base}/apps/${surface}`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "X-Ether-Request": "1" },
      });
      if (!response.ok) {
        const body = (await response.json().catch(() => ({}))) as {
          error?: { code?: string; message?: string };
        };
        if (body.error?.code === "stremio_https_required") {
          setError(
            t(
              "Stremio needs HTTPS. Ask the server owner to give this server a secure (HTTPS) address, then try again.",
            ),
          );
          return;
        }
        if (
          body.error?.code === "address_unavailable" ||
          body.error?.code === "address_unavailable"
        ) {
          setError(
            t(
              "Server address unavailable. Ask the server owner to set a reachable server address, then try again.",
            ),
          );
          return;
        }
        throw new Error();
      }
      setConnection((await response.json()) as Connection);
    } catch {
      setError(t("The request could not be completed. Try again."));
    } finally {
      setBusy(false);
    }
  }
  if (!apps.length && !error) return null;
  return (
    <section className="space-y-3">
      <div className="space-y-1">
        <h2 className="text-lg font-semibold">{t("Connect apps")}</h2>
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-3">
        {apps
          .filter((id) => names[id])
          .map((id) => (
            <div
              key={id}
              className="flex min-w-0 flex-col items-start gap-4 rounded-xl border border-border bg-surface p-4"
            >
              <div className="flex min-w-0 items-center gap-3 font-semibold">
                {id === "plex" ? (
                  <PlexMark />
                ) : id === "jellyfin" ? (
                  <JellyfinMark />
                ) : id === "emby" ? (
                  <EmbyMark />
                ) : id === "silo" ? (
                  <SiloMark />
                ) : id === "stremio" ? (
                  <StremioMark />
                ) : (
                  householdIcon(id)
                )}
                {id === "xtream" ? t("IPTV apps (Xtream)") : names[id]}
              </div>
              {id === "plex" ? (
                <a
                  className={buttonVariants({ variant: "default", size: "sm" })}
                  href="https://app.plex.tv"
                  target="_blank"
                  rel="noreferrer"
                >
                  {t("Open Plex")}
                </a>
              ) : (
                <Button
                  className="max-w-full whitespace-normal"
                  size="sm"
                  disabled={busy}
                  onClick={() => void create(id)}
                >
                  {t("Create connection")}
                </Button>
              )}
            </div>
          ))}
      </div>
      <Dialog
        open={!!connection}
        onClose={() => setConnection(undefined)}
        title={
          <span
            id={titleId}
            ref={(node) => {
              node?.closest('[role="dialog"]')?.setAttribute("aria-labelledby", titleId);
            }}
          >
            {t("Connection created")}
          </span>
        }
        description={connection ? names[connection.surface] : undefined}
        closeLabel={t("Close")}
        size="lg"
        footer={<Button onClick={() => setConnection(undefined)}>{t("Done")}</Button>}
      >
        {connection && (
          <div className="min-w-0 max-h-[calc(100dvh-14rem)] space-y-5 overflow-y-auto">
            <p className="text-sm text-fg-muted">{t("This secret is shown only once.")}</p>
            <ConnectionField label={t("Server URL")} value={connection.url} />
            {connection.username && (
              <ConnectionField label={t("Username")} value={connection.username} />
            )}
            {connection.password && (
              <ConnectionField label={t("Password")} value={connection.password} />
            )}
            {connection.qr && (
              <div className="flex flex-col items-center gap-3 rounded-xl border border-border bg-white p-4">
                <img
                  src={connection.qr}
                  alt={t("QR code for the server address")}
                  width={192}
                  height={192}
                  className="max-w-full"
                />
              </div>
            )}
            {connection.surface === "stremio" && (
              <a
                className={buttonVariants({ variant: "outline" })}
                href={connection.url.replace(/^https?:\/\//, "stremio://")}
              >
                {t("Open in Stremio")}
              </a>
            )}
          </div>
        )}
      </Dialog>
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

function ConnectionField({ label, value }: { label: string; value: string }) {
  const id = useId();
  return (
    <div className="min-w-0 space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <div className="flex min-w-0 items-center gap-2">
        <Input
          id={id}
          readOnly
          value={value}
          className="min-w-0 flex-1 font-mono text-sm"
          onFocus={(event) => event.currentTarget.select()}
        />
        <CopyButton label={`${t("Copy")}: ${label}`} value={value} />
      </div>
    </div>
  );
}
