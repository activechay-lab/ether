import { Badge, Button, SettingsSection } from "@braedonsaunders/appkit-ui";
import { ArrowRight, Image, Clock, Folder, Trash2 } from "lucide-react";
import { useState } from "react";
import { Drawer } from "../../components/Drawer";
import { IntegrationAppCard } from "../../components/IntegrationAppCard";
import { Kpi } from "../../components/Kpi";
import { MutationError } from "../../components/feedback";
import { t } from "../../i18n";
import { api, apiFetch } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { PosterFolder } from "../metadata/PosterFolder";
import { normalizePosters, posterPayload } from "./PosterAccounts";

type Status = {
  uploaded: number;
  lastUpload: string | null;
  limited: boolean;
  found: number;
  folderLimited: boolean;
};
export function OwnerPosters({ locked }: { locked: boolean }) {
  const [open, setOpen] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const config = useAsync(async () => normalizePosters((await api.getConfig()).posters), []);
  const status = useAsync(
    async () => (open ? apiFetch<Status>("/posters/status") : undefined),
    [open],
  );
  const enabled = config.data?.ownerEnabled !== false;
  async function toggle(on: boolean) {
    setBusy(true);
    setError(null);
    try {
      const latest = normalizePosters((await api.getConfig()).posters);
      const result = await apiFetch<{ value: typeof latest }>("/config/posters", {
        method: "PUT",
        body: JSON.stringify(posterPayload({ ...latest, ownerEnabled: on })),
      });
      config.setData(normalizePosters(result.value));
      return true;
    } catch (e) {
      setError(e);
      return false;
    } finally {
      setBusy(false);
    }
  }
  async function clear() {
    setBusy(true);
    setError(null);
    try {
      let more: boolean;
      do {
        ({ more } = await apiFetch<{ more: boolean }>("/posters/clear", { method: "POST" }));
      } while (more);
      setConfirm(false);
      await status.reload();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  const close = () => {
    if (!busy) {
      setOpen(false);
      setConfirm(false);
    }
  };
  return (
    <>
      <IntegrationAppCard
        identity={{ id: "posterflow", name: "PosterFlow" }}
        description={t("Upload posters from PosterFlow, or use a poster folder")}
        status={
          <Badge variant={enabled ? "success" : "secondary"}>{enabled ? t("On") : t("Off")}</Badge>
        }
        enabled={enabled}
        disabled={locked || busy || !config.data}
        onToggle={(on) => {
          if (on) setOpen(true);
          else void toggle(false);
        }}
        action={
          <Button
            size="sm"
            variant="outline"
            disabled={locked || busy || !config.data}
            onClick={() => setOpen(true)}
          >
            {enabled
              ? t("Manage {app}", { app: "PosterFlow" })
              : t("Set up {app}", { app: "PosterFlow" })}
            <ArrowRight size={14} />
          </Button>
        }
      />
      {!open && (error || config.error) ? <MutationError error={error || config.error} /> : null}
      <Drawer
        open={open}
        onClose={close}
        title="PosterFlow"
        description={t("Upload posters from PosterFlow, or use a poster folder")}
        footer={
          <div className="flex w-full flex-wrap items-center justify-between gap-3">
            {confirm ? (
              <div className="w-full space-y-3">
                <p className="text-sm text-fg-muted">
                  {t("Clear uploaded artwork? Folder files stay in place.")}
                </p>
                <div className="flex justify-end gap-2">
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={busy}
                    onClick={() => setConfirm(false)}
                  >
                    {t("Cancel")}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="text-danger"
                    disabled={locked || busy}
                    onClick={() => void clear()}
                  >
                    <Trash2 size={14} />
                    {t("Clear uploaded artwork")}
                  </Button>
                </div>
              </div>
            ) : (
              <>
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-danger"
                  disabled={locked || busy || !status.data}
                  onClick={() => setConfirm(true)}
                >
                  <Trash2 size={14} />
                  {t("Clear uploaded artwork")}
                </Button>
                <Button
                  size="sm"
                  disabled={locked || busy}
                  onClick={
                    enabled
                      ? close
                      : async () => {
                          if (await toggle(true)) {
                            setOpen(false);
                            setConfirm(false);
                          }
                        }
                  }
                >
                  {enabled ? t("Done") : t("Save and enable")}
                </Button>
              </>
            )}
          </div>
        }
      >
        <div className="space-y-6">
          {error || config.error || status.error ? (
            <MutationError error={error || config.error || status.error} />
          ) : null}
          <SettingsSection title={t("How it works")}>
            <ol className="list-decimal space-y-3 text-pretty py-4 pl-9 pr-5 text-sm leading-relaxed text-fg-muted">
              <li>
                {t("Add Ether as a Plex or Jellyfin server in PosterFlow, then use Asset Upload.")}
              </li>
              <li>
                {t("Name folder posters {id}.jpg or Title (Year)/poster.jpg.", { id: "{tmdb_id}" })}
              </li>
              <li>{t("Use {app} posters in Metadata › Artwork.", { app: "PosterFlow" })}</li>
            </ol>
          </SettingsSection>
          <section className="space-y-3" aria-label={t("Status")}>
            <h3 className="text-sm font-semibold text-fg">{t("Status")}</h3>
            <div className="grid gap-3 sm:grid-cols-2">
              <Kpi
                label={t("Uploaded posters")}
                value={
                  status.data ? `${status.data.uploaded}${status.data.limited ? "+" : ""}` : "—"
                }
                icon={<Image size={18} />}
              />
              <Kpi
                label={t("Posters in folder")}
                value={
                  status.data ? `${status.data.found}${status.data.folderLimited ? "+" : ""}` : "—"
                }
                icon={<Folder size={18} />}
              />
              <Kpi
                className="sm:col-span-2"
                label={t("Last upload")}
                value={
                  status.data?.lastUpload
                    ? new Date(status.data.lastUpload).toLocaleDateString()
                    : t("Never")
                }
                detail={
                  status.data?.lastUpload
                    ? new Date(status.data.lastUpload).toLocaleTimeString()
                    : undefined
                }
                icon={<Clock size={18} />}
              />
            </div>
          </section>
          <SettingsSection title={t("Poster folder")}>
            <div className="p-5">
              {config.data && (
                <PosterFolder
                  disabled={locked || busy}
                  value={config.data.assetFolder ?? ""}
                  save={async (assetFolder) => {
                    const latest = normalizePosters((await api.getConfig()).posters);
                    const result = await apiFetch<{ value: typeof latest }>("/config/posters", {
                      method: "PUT",
                      body: JSON.stringify(posterPayload({ ...latest, assetFolder })),
                    });
                    config.setData(normalizePosters(result.value));
                    await status.reload();
                  }}
                />
              )}
            </div>
          </SettingsSection>
        </div>
      </Drawer>
    </>
  );
}
