import { Badge, Button, SettingsSection } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Drawer } from "../../components/Drawer";
import { IntegrationAppCard } from "../../components/IntegrationAppCard";
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
  // Fetch only when somebody uses this integration.
  const data = useAsync(
    async () =>
      open
        ? {
            posters: normalizePosters((await api.getConfig()).posters),
            status: await apiFetch<Status>("/posters/status"),
          }
        : undefined,
    [open],
  );
  return (
    <>
      <IntegrationAppCard
        identity={{ id: "posterflow", name: "PosterFlow" }}
        description={t("PosterFlow uploads and posters in this folder take priority.")}
        status={<Badge variant="secondary">{t("Custom posters")}</Badge>}
        action={
          <Button size="sm" variant="outline" disabled={locked} onClick={() => setOpen(true)}>
            {t("Configure")}
          </Button>
        }
      />
      <Drawer
        open={open}
        onClose={() => {
          if (!busy) {
            setOpen(false);
            setConfirm(false);
          }
        }}
        title="PosterFlow"
        description={t("Custom posters")}
      >
        <SettingsSection title={undefined}>
          <div className="space-y-4 p-5 text-sm text-fg-muted">
            <p>
              {t("Add Ether as a Plex or Jellyfin server in PosterFlow, then use Asset Upload.")}
            </p>
            <p>
              {t("Name folder posters {id}.jpg or Title (Year)/poster.jpg.", { id: "{tmdb_id}" })}
            </p>
            {error || data.error ? <MutationError error={error || data.error} /> : null}
            {data.data && (
              <>
                <p>
                  {t("Uploaded posters: {count}", { count: data.data.status.uploaded })}
                  {data.data.status.limited ? "+" : ""}
                </p>
                <p>
                  {t("Last upload: {time}", {
                    time: data.data.status.lastUpload
                      ? new Date(data.data.status.lastUpload).toLocaleString()
                      : t("Never"),
                  })}
                </p>
                <p>
                  {t("Found {count} posters", { count: data.data.status.found })}
                  {data.data.status.folderLimited ? "+" : ""}
                </p>
                <PosterFolder
                  disabled={locked || busy}
                  value={data.data.posters.assetFolder ?? ""}
                  save={async (assetFolder) => {
                    const latest = normalizePosters((await api.getConfig()).posters);
                    await apiFetch("/config/posters", {
                      method: "PUT",
                      body: JSON.stringify(posterPayload({ ...latest, assetFolder })),
                    });
                    await data.reload();
                  }}
                />
                <p>{t("Use {app} posters in Metadata › Artwork.", { app: "PosterFlow" })}</p>
                {confirm ? (
                  <div className="space-y-3">
                    <p>{t("Clear uploaded artwork? Folder files stay in place.")}</p>
                    <Button
                      disabled={busy || locked}
                      onClick={async () => {
                        setBusy(true);
                        setError(null);
                        try {
                          let more: boolean;
                          do {
                            ({ more } = await apiFetch<{ more: boolean }>("/posters/clear", {
                              method: "POST",
                            }));
                          } while (more);
                          setConfirm(false);
                          await data.reload();
                        } catch (e) {
                          setError(e);
                        } finally {
                          setBusy(false);
                        }
                      }}
                    >
                      {t("Clear")}
                    </Button>
                    <Button disabled={busy} onClick={() => setConfirm(false)}>
                      {t("Cancel")}
                    </Button>
                  </div>
                ) : (
                  <Button
                    variant="outline"
                    disabled={busy || locked}
                    onClick={() => setConfirm(true)}
                  >
                    {t("Clear uploaded artwork")}
                  </Button>
                )}
              </>
            )}
          </div>
        </SettingsSection>
      </Drawer>
    </>
  );
}
