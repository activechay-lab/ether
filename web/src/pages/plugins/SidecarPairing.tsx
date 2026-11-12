import { copyText } from "../../components/CopyButton";
import { Button } from "@braedonsaunders/appkit-ui";
import { useCallback, useEffect, useState } from "react";
import { MutationError } from "../../components/feedback";
import { Section } from "../../components/layout";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { formatDateTime } from "../../lib/format";

type Pairing = { id: string; created: number; lastUsed: number; expires: number };
type Code = { code: string; expiresAt: string; tunnelUrl: string; exchangePath: string };
type Sidecar = { docker?: string; download?: string; command?: string; docs?: string };

/** The server's http(s) base, recovered from the pairing response's tunnel URL. */
export function serverBaseFromTunnel(tunnelUrl: string): string {
  const trimmed = tunnelUrl.replace(/\/ether\/plugins\/tunnel\/?$/, "");
  if (trimmed.startsWith("wss://")) return "https://" + trimmed.slice("wss://".length);
  if (trimmed.startsWith("ws://")) return "http://" + trimmed.slice("ws://".length);
  return trimmed;
}

/** A name for the container, volume and program, safe for a shell. */
export function shortId(id: string): string {
  const tail = id.split(".").pop() ?? id;
  const slug = tail
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 24);
  return slug || "sidecar";
}

/** The ready-to-run command for a sidecar, or null when it has none. */
export function sidecarCommand(
  sidecar: Sidecar | undefined,
  id: string,
  code: string,
  tunnelUrl: string,
): { kind: "docker" | "download"; line: string } | null {
  if (!sidecar) return null;
  const base = serverBaseFromTunnel(tunnelUrl);
  const name = `ether-${shortId(id)}`;
  if (sidecar.docker) {
    return {
      kind: "docker",
      line: `docker run -d --name ${name} --restart unless-stopped -e ETHER_URL=${base} -e ETHER_PAIRING_CODE=${code} -v ${name}:/data ${sidecar.docker}`,
    };
  }
  if (sidecar.download && sidecar.command) {
    return {
      kind: "download",
      line: `ETHER_URL=${base} ETHER_PAIRING_CODE=${code} ${sidecar.command}`,
    };
  }
  return null;
}

export function SidecarPairing({ id, sidecar }: { id: string; sidecar?: Sidecar }) {
  const [pairings, setPairings] = useState<Pairing[]>([]);
  const [code, setCode] = useState<Code | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const base = `/plugins/${encodeURIComponent(id)}`;
  const load = useCallback(async () => {
    try {
      const result = await apiFetch<{ pairings: Pairing[] }>(`${base}/pairings`);
      setPairings(result.pairings ?? []);
      setError(null);
    } catch (e) {
      setError(e);
    }
  }, [base]);
  useEffect(() => {
    setCode(null);
    setPairings([]);
    void load();
  }, [load]);
  const pair = async () => {
    setBusy(true);
    setError(null);
    try {
      setCode(await apiFetch<Code>(`${base}/pair`, { method: "POST" }));
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  const revoke = async (pairing: string) => {
    setBusy(true);
    setError(null);
    try {
      await apiFetch(`${base}/pairings/${encodeURIComponent(pairing)}`, { method: "DELETE" });
      await load();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  const copy = async (value: string) => {
    try {
      await copyText(value);
    } catch (e) {
      setError(e);
    }
  };
  const command = code ? sidecarCommand(sidecar, id, code.code, code.tunnelUrl) : null;
  return (
    <Section title={t("Sidecars")}>
      <MutationError error={error} />
      <p className="text-sm text-fg-muted">
        {t("Connect a sidecar to run this plugin on another machine.")}
      </p>
      <Button type="button" size="sm" disabled={busy} onClick={() => void pair()}>
        {t("Connect a sidecar")}
      </Button>
      {code ? (
        <div className="space-y-2 rounded-lg border border-border p-3 text-sm">
          <p>
            {t(
              "This code works once within 5 minutes. The program saves its credential, so a restart never needs a new code.",
            )}
          </p>
          {command ? (
            <>
              <p className="font-medium text-fg">{t("Run it")}</p>
              <p className="font-mono break-all rounded bg-surface p-2">{command.line}</p>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                onClick={() => void copy(command.line)}
              >
                {t("Copy command")}
              </Button>
            </>
          ) : (
            <>
              {sidecar?.download ? (
                <p>
                  <a
                    className="text-primary hover:underline"
                    href={sidecar.download}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {t("Download the program")}
                  </a>
                </p>
              ) : null}
              <p className="font-mono break-all">
                {code.code}{" "}
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => void copy(code.code)}
                >
                  {t("Copy code")}
                </Button>
              </p>
              <p className="font-mono break-all">
                {code.tunnelUrl}{" "}
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => void copy(code.tunnelUrl)}
                >
                  {t("Copy tunnel address")}
                </Button>
              </p>
              <p className="font-mono break-all">
                {code.exchangePath}{" "}
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => void copy(code.exchangePath)}
                >
                  {t("Copy exchange path")}
                </Button>
              </p>
            </>
          )}
          {sidecar?.docs ? (
            <p>
              <a
                className="text-primary hover:underline"
                href={sidecar.docs}
                target="_blank"
                rel="noreferrer"
              >
                {t("Setup guide")}
              </a>
            </p>
          ) : null}
          <p>{t("Expires {time}", { time: formatDateTime(code.expiresAt) })}</p>
        </div>
      ) : null}
      {pairings.length ? (
        <ul className="space-y-2">
          {pairings.map((p) => (
            <li key={p.id} className="rounded-lg border border-border p-3 text-sm">
              <p className="font-mono break-all">{p.id}</p>
              <p>
                {t("Created {time}", { time: formatDateTime(new Date(p.created).toISOString()) })}
              </p>
              <p>
                {p.lastUsed
                  ? t("Last used {time}", {
                      time: formatDateTime(new Date(p.lastUsed).toISOString()),
                    })
                  : t("Not used yet")}
              </p>
              <p>
                {t("Expires {time}", { time: formatDateTime(new Date(p.expires).toISOString()) })}
              </p>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => void revoke(p.id)}
              >
                {t("Revoke")}
              </Button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-fg-muted">{t("No sidecars connected.")}</p>
      )}
    </Section>
  );
}
