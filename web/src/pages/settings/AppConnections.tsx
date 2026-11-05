import { copyText } from "../../components/CopyButton";
import { Button, Input, SettingsSection, Spinner } from "@braedonsaunders/appkit-ui";
import { StremioMark } from "../sources/marks";
import { Copy } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import {
  PlexMark,
  JellyfinMark,
  EmbyMark,
  SiloMark,
  householdIcon,
} from "../../components/integration-brands";
import { ErrorPanel } from "../../components/feedback";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { MoreOptions } from "../../components/ConfigSection";
import { SchemaField } from "./SchemaForm";
import { SERVER } from "./schema";
import { t } from "../../i18n";
import { api, apiFetch } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { usableAddresses, type ReachabilityStatus } from "../apps/reachability";

type Frontend = {
  descriptor: { id: string; name: string; defaultPort?: number };
  enabled: boolean;
  status: ReachabilityStatus;
};
function addressPort(address?: string): string {
  if (!address) return "";
  try {
    const url = new URL(address.includes("://") ? address : `http://${address}`);
    return url.port || (url.protocol === "https:" ? "443" : "80");
  } catch {
    return "";
  }
}
function localAddress(address: string): boolean {
  try {
    const host = new URL(address.includes("://") ? address : `http://${address}`).hostname;
    return (
      /^(192[.-]168[.-]|10[.-]|172[.-](1[6-9]|2\d|3[01])[.-]|\[?[fF][cCdD]|\[?[fF][eE]80)/.test(
        host,
      ) || host.endsWith(".local")
    );
  } catch {
    return false;
  }
}
export function AppConnections() {
  const state = useAsync(() => apiFetch<Frontend[]>("/frontends"), []);
  return (
    <SettingsSection>
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border px-5 py-4">
        <div>
          <h2 data-setting="how-apps-connect" className="text-sm font-medium">
            {t("How apps connect")}
          </h2>
          <p className="mt-1 text-sm text-fg-muted">
            {t("Use the address for your network. Port changes need a restart.")}
          </p>
        </div>
        <Link className="text-sm text-primary hover:underline" to="/apps">
          {t("Turn on more apps")}
        </Link>
      </div>
      {state.error ? (
        <ErrorPanel error={state.error} onRetry={() => void state.reload()} />
      ) : !state.data ? (
        <Spinner />
      ) : (
        <div className="grid gap-4 p-4 xl:grid-cols-2">
          {state.data
            .filter((app) => app.enabled)
            .map((app) => (
              <Connection key={app.descriptor.id} app={app} />
            ))}
        </div>
      )}
      <NetworkOptions
        plexEnabled={!!state.data?.some((app) => app.enabled && app.descriptor.id === "plex")}
      />
    </SettingsSection>
  );
}
function Connection({ app }: { app: Frontend }) {
  const { id, name, defaultPort } = app.descriptor;
  const state = useAsync(
    async () =>
      id === "plex"
        ? (await api.getConfig()).server
        : apiFetch<Record<string, unknown>>(`/frontends/${id}/settings`),
    [id],
  );
  const { draft, setDraft, dirty, reset } = useDraft(state.data);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>();
  const [copied, setCopied] = useState("");
  const logos = {
    plex: <PlexMark />,
    jellyfin: <JellyfinMark />,
    emby: <EmbyMark />,
    silo: <SiloMark />,
    stremio: <StremioMark />,
  };
  const addresses = [...new Set([...usableAddresses(app.status)])];
  return (
    <div className="min-w-0 space-y-4 rounded-xl border border-border bg-surface p-4">
      <div className="flex flex-wrap items-center gap-3">
        {logos[id as keyof typeof logos] ?? householdIcon(id)}
        <h3 className="flex-1 font-medium">{name}</h3>
        {["plex", "jellyfin", "emby", "silo"].includes(id) ? (
          <label className="flex items-center gap-2 text-sm text-fg-muted">
            <span className="hidden sm:inline" data-setting="port">
              {t("Port")}
            </span>
            <Input
              className="w-20"
              aria-label={t("{app} port", { app: name })}
              type="number"
              min={1}
              max={65535}
              value={String(draft?.port ?? defaultPort ?? addressPort(addresses[0]))}
              disabled={!draft}
              onChange={(e) => draft && setDraft({ ...draft, port: Number(e.currentTarget.value) })}
            />
          </label>
        ) : (
          <span className="text-xs text-fg-muted">
            {t("Uses Ether’s port {port}", { port: addressPort(addresses[0]) })}
          </span>
        )}
      </div>
      {id === "plex" ? (
        <p className="text-xs text-fg-muted">{t("Also used by Stremio, DLNA, Files and M3U")}</p>
      ) : null}
      {state.error ? <ErrorPanel error={state.error} onRetry={() => void state.reload()} /> : null}
      {addresses.map((address) => (
        <div key={address} className="flex min-w-0 items-center gap-2">
          <div className="min-w-0 flex-1">
            <p
              data-setting={localAddress(address) ? "local-url" : "remote-url"}
              className="text-xs text-fg-muted"
            >
              {localAddress(address) ? t("Local URL") : t("Remote URL")}
            </p>
            <p className="break-all text-sm">{address}</p>
          </div>
          <Button
            size="sm"
            variant="ghost"
            aria-label={t("Copy {address}", { address })}
            onClick={async () => {
              try {
                if (await copyText(address)) setCopied(address);
              } catch (e) {
                setError(e);
              }
            }}
          >
            <Copy size={16} />
            {copied === address ? t("Copied") : null}
          </Button>
        </div>
      ))}
      {!addresses.some((address) => !localAddress(address)) ? (
        <p className="text-xs text-fg-muted">{t("Remote URL not reported")}</p>
      ) : null}
      {!addresses.length ? (
        <p className="text-sm text-fg-muted">{t("No connection address reported yet")}</p>
      ) : null}
      <SaveBar
        dirty={dirty}
        saving={saving}
        error={error}
        onDiscard={reset}
        onSave={async () => {
          if (!draft) return;
          setSaving(true);
          setError(undefined);
          try {
            if (id === "plex") await api.putSection("server", { port: draft.port });
            else
              await apiFetch(`/frontends/${id}/settings`, {
                method: "PUT",
                body: JSON.stringify(draft),
              });
            await state.reload();
          } catch (e) {
            setError(e);
          } finally {
            setSaving(false);
          }
        }}
      />
    </div>
  );
}

function NetworkOptions({ plexEnabled }: { plexEnabled: boolean }) {
  const state = useAsync(async () => (await api.getConfig()).server, []);
  const { draft, setDraft, dirty, reset } = useDraft(state.data);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>();
  const fields = SERVER.find((group) => group.id === "connect")?.more ?? [];
  return (
    <MoreOptions setting="connect" label={t("More options: How apps connect")}>
      {!plexEnabled && draft ? (
        <label className="flex items-center justify-between gap-4 p-4 text-sm">
          {t("Ether port")}
          <Input
            aria-label={t("Ether port")}
            type="number"
            min={1}
            max={65535}
            value={draft.port}
            onChange={(e) => setDraft({ ...draft, port: Number(e.currentTarget.value) })}
          />
        </label>
      ) : null}
      {draft
        ? fields.map((field) => (
            <SchemaField
              key={field.key}
              def={field}
              value={(draft as unknown as Record<string, unknown>)[field.key]}
              path={`server.${field.key}`}
              errors={{}}
              onChange={(value) => setDraft({ ...draft, [field.key]: value })}
            />
          ))
        : null}
      <SaveBar
        dirty={dirty}
        saving={saving}
        error={error ?? state.error}
        onDiscard={reset}
        onSave={async () => {
          if (!draft) return;
          setSaving(true);
          try {
            const values = draft as unknown as Record<string, unknown>;
            await api.putSection(
              "server",
              Object.fromEntries([
                ...fields.map((f) => [f.key, values[f.key]]),
                ...(!plexEnabled ? [["port", draft.port]] : []),
              ]),
            );
            await state.reload();
          } catch (e) {
            setError(e);
          } finally {
            setSaving(false);
          }
        }}
      />
    </MoreOptions>
  );
}
