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

export function SidecarPairing({ id }: { id: string }) {
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
              "Run the sidecar with this code. It exchanges the code once on your local network for a credential.",
            )}
          </p>
          <p className="font-mono break-all">
            {code.code}{" "}
            <Button type="button" size="sm" variant="ghost" onClick={() => void copy(code.code)}>
              {t("Copy code")}
            </Button>
          </p>
          <p>{t("Expires {time}", { time: formatDateTime(code.expiresAt) })}</p>
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
