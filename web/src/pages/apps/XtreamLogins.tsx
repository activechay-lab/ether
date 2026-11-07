import { useEffect, useState } from "react";
import { Button, SettingsSection } from "@braedonsaunders/appkit-ui";
import { CopyButton } from "../../components/CopyButton";
import { loadViewers, viewerName } from "../../components/pickers";
import { apiFetch, errorMessage } from "../../lib/api";
import { t } from "../../i18n";

type Viewer = { id: number; title: string; username: string };
type Connection = { url: string; username: string; password: string };
type Grant = { hash: string; surface: string; lastUsed: number };

export function XtreamLogins() {
  const [viewers, setViewers] = useState<Viewer[]>([]);
  const [viewer, setViewer] = useState("");
  const [grants, setGrants] = useState<Grant[]>([]);
  const [connection, setConnection] = useState<Connection>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let live = true;
    void loadViewers()
      .then((v) => {
        if (live) setViewers(v);
      })
      .catch((e) => {
        if (live) setError(errorMessage(e));
      });
    return () => {
      live = false;
    };
  }, []);
  useEffect(() => {
    let live = true;
    setConnection(undefined);
    setGrants([]);
    setError("");
    if (viewer)
      void apiFetch<{ devices: Grant[] }>(`/users/${viewer}/devices`)
        .then((v) => {
          if (live) setGrants(v.devices.filter((g) => g.surface === "xtream"));
        })
        .catch((e) => {
          if (live) setError(errorMessage(e));
        });
    return () => {
      live = false;
    };
  }, [viewer]);
  async function create() {
    setBusy(true);
    setError("");
    setConnection(undefined);
    try {
      const result = await apiFetch<Connection>(`/viewers/${viewer}/apps/xtream`, {
        method: "POST",
      });
      setConnection(result);
      const list = await apiFetch<{ devices: Grant[] }>(`/users/${viewer}/devices`);
      setGrants(list.devices.filter((g) => g.surface === "xtream"));
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function revoke(hash: string) {
    setBusy(true);
    setError("");
    try {
      await apiFetch(`/users/${viewer}/devices/revoke`, {
        method: "POST",
        body: JSON.stringify({ hash }),
      });
      setGrants((old) => old.filter((g) => g.hash !== hash));
      setConnection(undefined);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <SettingsSection
      title={t("IPTV apps (Xtream)")}
      description={t("Live TV, movies and series in IPTV player apps.")}
    >
      <div className="space-y-4 px-5 py-4">
        <p className="text-sm text-fg-muted">
          {t("Create a separate login for each device. Each login allows one connection.")}
        </p>
        <label className="block space-y-2 text-sm">
          <span>{t("Viewer")}</span>
          <select
            className="block w-full rounded-lg border border-border bg-surface p-2"
            value={viewer}
            disabled={busy}
            onChange={(e) => setViewer(e.target.value)}
          >
            <option value="">{t("Choose a viewer")}</option>
            {viewers.map((v) => (
              <option key={v.id} value={v.id}>
                {viewerName(v)}
              </option>
            ))}
          </select>
        </label>
        <Button disabled={!viewer || busy} onClick={() => void create()}>
          {t("Create connection")}
        </Button>
        {connection && (
          <div className="space-y-3 rounded-lg border border-border p-4">
            <p className="text-sm text-fg-muted">{t("This secret is shown only once.")}</p>
            {(
              [
                [t("Server address"), connection.url],
                [t("Username"), connection.username],
                [t("Password"), connection.password],
              ] as const
            ).map(([label, value]) => (
              <div key={label} className="space-y-1">
                <div className="text-sm font-medium">{label}</div>
                <div className="flex items-center justify-between gap-3">
                  <code className="min-w-0 break-all text-sm select-all">{value}</code>
                  <CopyButton label={label} value={value} />
                </div>
              </div>
            ))}
          </div>
        )}
        {grants.map((g, index) => (
          <div
            key={g.hash}
            className="flex items-center justify-between gap-3 border-t border-border pt-3"
          >
            <span className="text-sm">{t("Login {number}", { number: index + 1 })}</span>
            <Button variant="outline" disabled={busy} onClick={() => void revoke(g.hash)}>
              {t("Revoke")}
            </Button>
          </div>
        ))}
        {error && (
          <p role="alert" className="text-sm text-danger">
            {error}
          </p>
        )}
      </div>
    </SettingsSection>
  );
}
