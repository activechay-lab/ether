import { Badge, Button, Input, Switch, toast } from "@braedonsaunders/appkit-ui";
import { AlertTriangle, ArrowLeft, Link2, Loader2, Puzzle } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Artwork } from "../Artwork";
import { Drawer } from "../Drawer";
import { MutationError } from "../feedback";
import { Field } from "../fields";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { invalidateRegistry } from "../../lib/extend";
import { AddonSettingsPage } from "../../pages/plugins/drawer/AddonSettings";
import { LiveFields, useLiveForm } from "../../pages/plugins/drawer/liveForm";
import {
  ADDON_ROLE_LABEL,
  addonUrlHasDebridKey,
  type AddonPreview,
  type AddonRole,
  type AddonSummary,
} from "./addons";

/**
 * The one way to add a Stremio add-on, from any page: paste its install
 * link, see what it can do, and choose which of those roles to switch on.
 * The page's own role starts on; a link already installed is reused and
 * only gains the roles switched on here.
 */
export function AddAddonDrawer({
  open,
  role,
  title,
  submitLabel,
  onClose,
  onAdded,
}: {
  open: boolean;
  /** The role the page is adding the add-on for; it starts switched on. */
  role?: AddonRole;
  title?: string;
  submitLabel?: string;
  onClose: () => void;
  onAdded: (addon: AddonSummary, preview: AddonPreview) => void;
}) {
  const linkId = useId();
  const [url, setUrl] = useState("");
  const [preview, setPreview] = useState<AddonPreview | null>(null);
  const [on, setOn] = useState<Record<string, boolean>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [settingUp, setSettingUp] = useState(false);

  useEffect(() => {
    if (!open) {
      setUrl("");
      setPreview(null);
      setOn({});
      setError(null);
    }
  }, [open]);

  const [pendingRead, setPendingRead] = useState("");
  useEffect(() => {
    if (!pendingRead) return;
    setPendingRead("");
    void read(pendingRead);
    // read is recreated each render; this runs once per configured link.
  }, [pendingRead]);

  const read = async (link = url) => {
    setBusy(true);
    setError(null);
    try {
      const p = await apiFetch<AddonPreview>("/plugins/addons/preview", {
        method: "POST",
        body: JSON.stringify({ url: link.trim() }),
      });
      setPreview(p);
      const next: Record<string, boolean> = {};
      for (const r of p.roles ?? []) {
        // A new add-on starts with the page's role (or everything, from
        // Plugins); an installed one shows what it already does plus it.
        next[r.role] = p.alreadyInstalled ? r.on || r.role === role : role ? r.role === role : true;
      }
      setOn(next);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const add = async () => {
    if (!preview) return;
    setBusy(true);
    setError(null);
    try {
      const roles = Object.entries(on)
        .filter(([, v]) => v)
        .map(([k]) => k);
      const added = await apiFetch<AddonSummary>("/plugins/addons", {
        method: "POST",
        body: JSON.stringify({ url: url.trim(), roles }),
      });
      if (preview.alreadyInstalled) {
        // Roles switched off here are switched off on the add-on too.
        const off = (preview.roles ?? []).map((r) => r.role).filter((r) => !on[r]);
        await apiFetch(`/plugins/${encodeURIComponent(added.id)}/roles`, {
          method: "PUT",
          body: JSON.stringify({ off }),
        });
      }
      invalidateRegistry();
      toast.success(
        preview.alreadyInstalled
          ? t("{name} updated", { name: added.name })
          : t("{name} added", { name: added.name }),
      );
      onAdded(added, preview);
      onClose();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const roleMissing = !!role && !!preview && !(preview.roles ?? []).some((r) => r.role === role);
  const nothingOn = !!preview && !Object.values(on).some(Boolean);

  const openConfigure = () =>
    apiFetch<{ page: string }>("/plugins/addons/configure-page", {
      method: "POST",
      body: JSON.stringify({ url: url.trim() }),
    }).then((r) => r.page);
  // A plugin that needs setting up first: its own settings page, read and
  // shown here as Ether fields.
  const live = useLiveForm(openConfigure, open && preview?.configureFirst ? 1 : 0);
  const [touched, setTouched] = useState<Record<string, unknown>>({});
  const useLink = (link: string) => {
    // The configured link is the plugin to add; read it again.
    setSettingUp(false);
    setTouched({});
    setUrl(link);
    setPreview(null);
    setPendingRead(link);
  };
  const continueSetup = async () => {
    setBusy(true);
    setError(null);
    try {
      const link = await live.commit();
      if (link) useLink(link);
      else
        setError(
          new Error(
            t("{host} didn't give a link for these settings yet.", { host: preview?.host ?? "" }),
          ),
        );
    } finally {
      setBusy(false);
    }
  };

  const settingsPage = preview ? (
    <AddonSettingsPage
      open={settingUp}
      name={preview.name}
      host={preview.host}
      useLabel={t("Continue")}
      openPage={openConfigure}
      onUse={async (link) => useLink(link)}
      onClose={() => setSettingUp(false)}
    />
  ) : null;

  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={title ?? t("Add a plugin from Stremio")}
      size="md"
      footer={
        preview ? (
          <div className="flex w-full items-center justify-between gap-3">
            <Button variant="ghost" onClick={() => setPreview(null)} disabled={busy}>
              <ArrowLeft size={14} aria-hidden />
              {t("Back")}
            </Button>
            {preview.configureFirst ? (
              <Button
                onClick={() => void continueSetup()}
                disabled={busy || live.status !== "ready"}
              >
                {t("Continue")}
              </Button>
            ) : (
              <Button onClick={() => void add()} disabled={busy || nothingOn || roleMissing}>
                {preview.alreadyInstalled ? t("Save") : (submitLabel ?? t("Add plugin"))}
              </Button>
            )}
          </div>
        ) : (
          <div className="flex w-full justify-end">
            <Button onClick={() => void read()} disabled={busy || !url.trim()}>
              {t("Continue")}
            </Button>
          </div>
        )
      }
    >
      {!preview ? (
        <div className="space-y-4">
          <p className="text-sm text-fg-muted">
            {t(
              "Paste the plugin's install link from its Stremio settings page. Ether keeps it private: it may contain your debrid key.",
            )}
          </p>
          <Field label={t("Install link")} htmlFor={linkId}>
            <Input
              id={linkId}
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://… /manifest.json"
              autoFocus
              onKeyDown={(e) => {
                if (e.key === "Enter" && url.trim()) void read();
              }}
            />
          </Field>
          {addonUrlHasDebridKey(url) ? (
            <p role="status" className="rounded-lg bg-warning-subtle p-3 text-sm">
              {t(
                "This link includes a debrid API key. Ether checks your debrid accounts itself, so the plugin doesn't need it: a key only adds cached hints, and it is then also stored in this link on this server.",
              )}
            </p>
          ) : null}
          <p className="flex items-start gap-2 text-xs text-fg-subtle">
            <Link2 size={13} className="mt-0.5 shrink-0" aria-hidden />
            {t(
              "stremio:// links work too. One plugin can give streams, catalogs, Live TV channels and subtitles; you choose which.",
            )}
          </p>
          {error ? <MutationError error={error} /> : null}
        </div>
      ) : (
        <div className="space-y-5">
          <div className="flex items-center gap-3">
            <Artwork
              src={preview.logo}
              alt=""
              icon={<Puzzle size={18} />}
              className="size-12 shrink-0 rounded-lg"
            />
            <div className="min-w-0">
              <p className="truncate font-semibold">{preview.name}</p>
              <p className="truncate text-xs text-fg-muted">
                {preview.host}
                {preview.version ? ` · ${preview.version}` : ""}
              </p>
            </div>
            {preview.alreadyInstalled ? (
              <Badge variant="secondary" className="ml-auto">
                {t("Already added")}
              </Badge>
            ) : null}
          </div>
          {preview.description ? (
            <p className="text-sm text-fg-muted">{preview.description}</p>
          ) : null}
          {preview.configureFirst ? (
            <div className="space-y-3">
              <p className="flex items-start gap-2 rounded-lg bg-warning-subtle p-3 text-sm">
                <AlertTriangle size={15} className="mt-0.5 shrink-0 text-warning" aria-hidden />
                {t("{name} needs setting up before Ether can use it.", { name: preview.name })}
              </p>
              {live.status === "ready" ? (
                <LiveFields
                  form={live}
                  title={t("Plugin settings")}
                  description={
                    live.partial
                      ? t(
                          "Read from {host}'s own settings page. A few of its settings are only on that page.",
                          { host: preview.host },
                        )
                      : t("Read from {host}'s own settings page.", { host: preview.host })
                  }
                  touched={touched}
                  onTouched={setTouched}
                />
              ) : live.status === "loading" ? (
                <p className="flex items-center gap-2 text-sm text-fg-muted" role="status">
                  <Loader2 size={14} className="animate-spin" aria-hidden />
                  {t("Reading {host}'s settings…", { host: preview.host })}
                </p>
              ) : null}
              <Button
                size="sm"
                variant={live.status === "ready" ? "ghost" : "default"}
                onClick={() => setSettingUp(true)}
              >
                {live.status === "ready"
                  ? t("Open {host}'s own page", { host: preview.host })
                  : t("Set it up here")}
              </Button>
              {live.frame}
            </div>
          ) : (
            <section className="space-y-1">
              <h3 className="text-sm font-semibold">{t("What it does in Ether")}</h3>
              {(preview.roles ?? []).length === 0 ? (
                <p className="text-sm text-fg-muted">
                  {t("This plugin offers nothing Ether can use yet.")}
                </p>
              ) : (
                (preview.roles ?? []).map((r) => (
                  <label
                    key={r.role}
                    className="flex items-center justify-between gap-3 rounded-lg px-1 py-2"
                  >
                    <span className="text-sm">
                      {ADDON_ROLE_LABEL[r.role as AddonRole] ?? r.role}
                    </span>
                    <Switch
                      checked={!!on[r.role]}
                      onChange={(e) => setOn({ ...on, [r.role]: e.currentTarget.checked })}
                      aria-label={ADDON_ROLE_LABEL[r.role as AddonRole] ?? r.role}
                    />
                  </label>
                ))
              )}
              {roleMissing ? (
                <p className="text-sm text-danger">
                  {t("This plugin does not offer {role}.", {
                    role: ADDON_ROLE_LABEL[role!].toLowerCase(),
                  })}
                </p>
              ) : null}
            </section>
          )}
          {error ? <MutationError error={error} /> : null}
        </div>
      )}
      {settingsPage}
    </Drawer>
  );
}
