import { Button, Popover, confirmDialog, toast } from "@braedonsaunders/appkit-ui";
import {
  ChevronDown,
  Download,
  ExternalLink,
  Power,
  PowerOff,
  PackageMinus,
  Trash2,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { t } from "../../i18n";
import { ApiError, apiFetch, errorMessage } from "../../lib/api";
import { invalidateRegistry } from "../../lib/extend";
import { MenuItem } from "./MenuItem";
import type { StudioProject } from "./studio-types";

interface InstalledState {
  id: string;
  name: string;
  enabled: boolean;
}

/**
 * Studio's lifecycle menu, far right of the header: install or update the
 * open draft, reach or switch off the installed plugin, uninstall it, or
 * delete the draft.
 */
export function StudioActions({
  project,
  dirty,
  onInstall,
  onGone,
}: {
  project: StudioProject;
  dirty: boolean;
  /** Opens the install dialog (permissions, update in place or copy). */
  onInstall: () => void;
  /** The draft no longer exists (deleted, or its plugin was uninstalled). */
  onGone: () => void;
}) {
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [installed, setInstalled] = useState<InstalledState | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const r = await apiFetch<InstalledState>(`/plugins/${encodeURIComponent(project.pluginId)}`);
      setInstalled(r);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setInstalled(null);
    }
  }, [project.pluginId]);
  useEffect(() => {
    void load();
  }, [load, project.updatedAt]);

  const run = async (fn: () => Promise<void>) => {
    setOpen(false);
    setBusy(true);
    try {
      await fn();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const problems = (project.diagnostics ?? []).length > 0;
  const installHint = dirty
    ? t("Save your changes first")
    : problems
      ? t("Fix the problems first")
      : undefined;

  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      align="end"
      className="w-64 p-1"
      trigger={
        <Button
          size="sm"
          disabled={busy}
          aria-haspopup="menu"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          {t("Actions")} <ChevronDown size={14} />
        </Button>
      }
    >
      <div role="menu" aria-label={t("Plugin actions")}>
        <MenuItem
          icon={<Download size={14} />}
          label={installed ? t("Update {name}…", { name: installed.name }) : t("Install…")}
          hint={installHint}
          disabled={Boolean(installHint)}
          onClick={() => {
            setOpen(false);
            onInstall();
          }}
        />
        {installed ? (
          <>
            <MenuItem
              icon={<ExternalLink size={14} />}
              label={t("Open in Plugins")}
              onClick={() => {
                setOpen(false);
                navigate(`/plugins/installed?plugin=${encodeURIComponent(installed.id)}`);
              }}
            />
            <MenuItem
              icon={installed.enabled ? <PowerOff size={14} /> : <Power size={14} />}
              label={installed.enabled ? t("Turn off") : t("Turn on")}
              onClick={() =>
                void run(async () => {
                  await apiFetch(
                    `/plugins/${encodeURIComponent(installed.id)}/${installed.enabled ? "disable" : "enable"}`,
                    { method: "POST" },
                  );
                  invalidateRegistry();
                  await load();
                })
              }
            />
          </>
        ) : null}
        <div className="my-1 border-t border-border" />
        {installed ? (
          <MenuItem
            icon={<PackageMinus size={14} />}
            label={t("Uninstall…")}
            danger
            onClick={() =>
              void run(async () => {
                const ok = await confirmDialog({
                  title: t("Uninstall {name}?", { name: installed.name }),
                  message: t(
                    "It is removed from this server, with its settings and storage. Its draft in Studio goes too.",
                  ),
                  confirmLabel: t("Uninstall"),
                  tone: "danger",
                });
                if (!ok) return;
                await apiFetch(`/plugins/${encodeURIComponent(installed.id)}`, {
                  method: "DELETE",
                });
                invalidateRegistry();
                toast.success(t("Uninstalled {name}", { name: installed.name }));
                onGone();
              })
            }
          />
        ) : null}
        <MenuItem
          icon={<Trash2 size={14} />}
          label={installed ? t("Discard draft…") : t("Delete draft…")}
          hint={installed ? t("The installed plugin stays as it is") : undefined}
          danger
          onClick={() =>
            void run(async () => {
              const ok = await confirmDialog({
                title: installed
                  ? t("Discard this draft?")
                  : t("Delete {name}?", { name: project.name }),
                message: installed
                  ? t(
                      "Your changes since the last install are lost. Opening the plugin again starts from what is installed.",
                    )
                  : t("The draft and its code are deleted. Nothing installed is affected."),
                confirmLabel: installed ? t("Discard") : t("Delete"),
                tone: "danger",
              });
              if (!ok) return;
              await apiFetch(`/plugins/studio/projects/${encodeURIComponent(project.id)}`, {
                method: "DELETE",
              });
              onGone();
            })
          }
        />
      </div>
    </Popover>
  );
}
