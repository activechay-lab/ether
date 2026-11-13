import {
  Button,
  Dialog,
  Popover,
  SubtabNav,
  Switch,
  Tooltip,
  confirmDialog,
  toast,
} from "@braedonsaunders/appkit-ui";
import {
  ArrowUpCircle,
  Code2,
  ExternalLink,
  FlaskConical,
  Info,
  MoreHorizontal,
  PowerOff,
  RefreshCw,
  RotateCcw,
  Trash2,
  Undo2,
} from "lucide-react";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows, MutationError } from "../../components/feedback";
import { StatusDot } from "../../components/status";
import { t, tn } from "../../i18n";
import { ApiError, apiFetch } from "../../lib/api";
import { invalidateRegistry } from "../../lib/extend";
import { PluginActivity } from "./drawer/PluginActivity";
import { usePluginCalls } from "./drawer/calls";
import { healthSummary } from "./drawer/health";
import { contributionTab, primaryContribution } from "./drawer/kinds";
import { PluginContributions } from "./drawer/PluginContributions";
import { PluginOverview } from "./drawer/PluginOverview";
import { PluginSetup } from "./drawer/PluginSetup";
import { PluginCatalogueSettings } from "./drawer/PluginCatalogueSettings";
import { SetupWizard } from "../libraries/SetupWizard";
import { MenuItem } from "./MenuItem";
import { openInStudio } from "./openInStudio";
import {
  isAddon,
  kindLabel,
  type JournalEntry,
  type PluginDetail,
  type PluginSummary,
} from "./types";

type DrawerTab = "overview" | "adds" | "setup" | "activity";

/**
 * One installed plugin. The header carries what you reach for every time
 * (health, on/off, Studio, and the ⋯ menu for updates, undo and removal);
 * the tabs are Overview, what it adds, Setup and Activity.
 */
export function InstalledPluginDrawer({
  pluginId,
  logRevision = 0,
  liveLogLines = [],
  onClose,
  onChanged,
  onRemoved,
}: {
  pluginId: string | null;
  logRevision?: number;
  liveLogLines?: string[];
  onClose: () => void;
  onChanged: () => void;
  onRemoved: () => void;
}) {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [tab, setTab] = useState<DrawerTab>("overview");
  const [setupWizard, setSetupWizard] = useState(false);
  const [detail, setDetail] = useState<PluginDetail | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [mutationError, setMutationError] = useState<unknown>(null);
  const [journal, setJournal] = useState<JournalEntry[]>([]);
  const [pendingPerms, setPendingPerms] = useState<string[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const [menu, setMenu] = useState(false);
  const [removeOpen, setRemoveOpen] = useState(false);
  const [undoFirst, setUndoFirst] = useState(true);
  const { calls, reload: reloadCalls } = usePluginCalls(pluginId, logRevision);

  useEffect(() => setTab(params.get("tab") === "setup" ? "setup" : "overview"), [pluginId, params]);
  useEffect(() => setBusy(null), [pluginId]);

  const loadDetail = useCallback(async (id: string, quiet = false) => {
    if (!quiet) setLoading(true);
    setLoadError(null);
    try {
      const d = await apiFetch<PluginDetail>(`/plugins/${encodeURIComponent(id)}`);
      setDetail(d);
      setPendingPerms(d.addedPermissions ?? []);
    } catch (e) {
      setLoadError(e);
    } finally {
      if (!quiet) setLoading(false);
    }
  }, []);

  const loadJournal = useCallback(async (id: string) => {
    try {
      const r = await apiFetch<{ entries: JournalEntry[] }>(
        `/plugins/${encodeURIComponent(id)}/journal`,
      );
      setJournal((r.entries ?? []).filter((e) => !e.shadow));
    } catch {
      setJournal([]);
    }
  }, []);

  useEffect(() => {
    if (!pluginId) {
      setDetail(null);
      return;
    }
    setMutationError(null);
    void loadDetail(pluginId);
    void loadJournal(pluginId);
  }, [pluginId, loadDetail, loadJournal]);

  useEffect(() => {
    if (pluginId && logRevision) void loadDetail(pluginId, true);
  }, [pluginId, logRevision, loadDetail]);

  const run = async (key: string, fn: () => Promise<void>) => {
    setBusy(key);
    setMutationError(null);
    try {
      await fn();
    } catch (e) {
      setMutationError(e);
    } finally {
      setBusy(null);
    }
  };

  const reload = async () => {
    if (!detail) return;
    invalidateRegistry();
    await loadDetail(detail.id, true);
    onChanged();
  };

  const toggleEnabled = (next: boolean) =>
    run("enable", async () => {
      await apiFetch(`/plugins/${encodeURIComponent(detail!.id)}/${next ? "enable" : "disable"}`, {
        method: "POST",
      });
      await reload();
    });

  const retry = () =>
    run("retry", async () => {
      await apiFetch(`/plugins/${encodeURIComponent(detail!.id)}/test`, { method: "POST" });
      await reload();
      await reloadCalls();
    });

  const makeLive = () =>
    run("live", async () => {
      await apiFetch(`/plugins/${encodeURIComponent(detail!.id)}/shadow`, {
        method: "POST",
        body: JSON.stringify({ shadow: false }),
      });
      await reload();
      toast.success(t("{name} is live", { name: detail!.name }));
    });

  const checkForUpdates = () =>
    run("check", async () => {
      const res = await apiFetch<{
        updateAvailable: boolean;
        version: string;
        addedPermissions?: string[];
      }>(`/plugins/${encodeURIComponent(detail!.id)}/update/check`, { method: "POST" });
      if (res.addedPermissions) setPendingPerms(res.addedPermissions);
      await loadDetail(detail!.id, true);
      toast.success(
        res.updateAvailable
          ? t("Version {version} is available", { version: res.version })
          : t("{name} is up to date", { name: detail!.name }),
      );
    });

  const applyUpdate = async () => {
    if (!detail) return;
    if (pendingPerms.length) {
      const ok = await confirmDialog({
        title: t("Update {name}?", { name: detail.name }),
        message: t("Version {version} asks for new access: {access}", {
          version: detail.updateVersion ?? "",
          access: pendingPerms.map((p) => t(p)).join(", "),
        }),
        confirmLabel: t("Allow and update"),
      });
      if (!ok) return;
    }
    await run("update", async () => {
      try {
        await apiFetch<PluginSummary>(`/plugins/${encodeURIComponent(detail.id)}/update`, {
          method: "POST",
          body: JSON.stringify({ acceptAddedPermissions: pendingPerms.length > 0 }),
        });
      } catch (e) {
        if (e instanceof ApiError && e.code === "permission_increase" && e.fields) {
          setPendingPerms(e.fields.map((f) => f.message));
          return;
        }
        throw e;
      }
      setPendingPerms([]);
      await reload();
      toast.success(t("Updated {name}", { name: detail.name }));
    });
  };

  const rollBack = () =>
    run("rollback", async () => {
      await apiFetch<PluginSummary>(`/plugins/${encodeURIComponent(detail!.id)}/rollback`, {
        method: "POST",
      });
      await reload();
      toast.success(t("Rolled back {name}", { name: detail!.name }));
    });

  const undoAll = async () => {
    if (!detail) return;
    const ok = await confirmDialog({
      title: t("Undo everything this plugin did?"),
      message: t("This reverses every collection and watch-state change recorded for {name}.", {
        name: detail.name,
      }),
      confirmLabel: t("Undo changes"),
      tone: "danger",
    });
    if (!ok) return;
    await run("undo", async () => {
      const res = await apiFetch<{ undone: number }>(
        `/plugins/${encodeURIComponent(detail.id)}/undo`,
        { method: "POST" },
      );
      toast.success(tn(res.undone, "Undid {count} change", "Undid {count} changes"));
      await loadJournal(detail.id);
      await reload();
    });
  };

  const remove = () =>
    run("remove", async () => {
      const query = undoFirst && applied > 0 ? "?undo=1" : "";
      await apiFetch(`/plugins/${encodeURIComponent(detail!.id)}${query}`, { method: "DELETE" });
      invalidateRegistry();
      setRemoveOpen(false);
      toast.success(t("Removed {name}", { name: detail!.name }));
      onRemoved();
    });

  const applied = journal.filter((j) => !j.undone).length;
  const primary = detail ? primaryContribution(detail) : undefined;
  const tabs: { key: DrawerTab; label: string }[] = [
    { key: "overview", label: t("Overview") },
    ...(primary ? [{ key: "adds" as const, label: contributionTab(primary) }] : []),
    { key: "setup", label: t("Settings") },
    { key: "activity", label: t("Activity") },
  ];
  const active = tabs.some((x) => x.key === tab) ? tab : "overview";

  if (setupWizard && detail)
    return <SetupWizard pluginId={detail.id} onClose={() => setSetupWizard(false)} />;
  return (
    <Drawer
      open={Boolean(pluginId)}
      onClose={onClose}
      size="xl"
      title={detail?.name ?? t("Plugin")}
      description={detail ? <HeaderLine detail={detail} /> : undefined}
      headerActions={
        detail ? (
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              aria-label={t("Open in Studio")}
              disabled={busy === "studio"}
              onClick={() => {
                setBusy("studio");
                void openInStudio(detail.id)
                  .then((to) => navigate(to))
                  .catch((e) => {
                    setMutationError(e);
                    setBusy(null);
                  });
              }}
            >
              <Code2 size={14} />
              <span className="hidden sm:inline">
                {busy === "studio" ? t("Opening…") : t("Open in Studio")}
              </span>
            </Button>
            <label
              className="ml-1 hidden text-xs font-medium text-fg-muted sm:inline"
              htmlFor="drawer-plugin-enabled"
            >
              {detail.enabled ? t("On") : t("Off")}
            </label>
            <Switch
              id="drawer-plugin-enabled"
              checked={detail.enabled}
              disabled={busy === "enable"}
              onChange={(e) => void toggleEnabled(e.currentTarget.checked)}
              aria-label={t("Enable {name}", { name: detail.name })}
            />
            <Popover
              open={menu}
              onOpenChange={setMenu}
              align="end"
              className="w-64 p-1"
              trigger={
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t("More actions")}
                  aria-haspopup="menu"
                  aria-expanded={menu}
                  onClick={() => setMenu((m) => !m)}
                >
                  <MoreHorizontal size={16} />
                </Button>
              }
            >
              <div role="menu" aria-label={t("More actions")}>
                {detail.sourceKind !== "studio" && !detail.stremioOnly ? (
                  <MenuItem
                    icon={<RefreshCw size={14} />}
                    label={busy === "check" ? t("Checking…") : t("Check for updates")}
                    onClick={() => {
                      setMenu(false);
                      void checkForUpdates();
                    }}
                  />
                ) : null}
                {detail.prevVersion ? (
                  <MenuItem
                    icon={<RotateCcw size={14} />}
                    label={t("Roll back to v{version}", { version: detail.prevVersion })}
                    onClick={() => {
                      setMenu(false);
                      void rollBack();
                    }}
                  />
                ) : null}
                <MenuItem
                  icon={<Undo2 size={14} />}
                  label={t("Undo everything it changed")}
                  disabled={applied === 0}
                  hint={applied === 0 ? t("It hasn't changed anything") : undefined}
                  onClick={() => {
                    setMenu(false);
                    void undoAll();
                  }}
                />
                <div className="my-1 border-t border-border" />
                <MenuItem
                  icon={<Trash2 size={14} />}
                  label={t("Remove…")}
                  danger
                  onClick={() => {
                    setMenu(false);
                    setUndoFirst(true);
                    setRemoveOpen(true);
                  }}
                />
              </div>
            </Popover>
          </div>
        ) : undefined
      }
      subtabs={
        detail ? (
          <SubtabNav
            ariaLabel={t("Plugin sections")}
            tabs={tabs}
            active={active}
            onSelect={(k) => {
              setMutationError(null);
              setTab(k as DrawerTab);
            }}
          />
        ) : undefined
      }
    >
      {loading && !detail ? (
        <LoadingRows rows={5} />
      ) : loadError ? (
        <ErrorPanel error={loadError} onRetry={() => pluginId && void loadDetail(pluginId)} />
      ) : detail ? (
        <div className="space-y-5">
          {detail.updateVersion ? (
            <Banner
              icon={<ArrowUpCircle size={16} className="text-primary" />}
              text={
                pendingPerms.length
                  ? t("Version {version} is available and asks for new access.", {
                      version: detail.updateVersion,
                    })
                  : t("Version {version} is available.", { version: detail.updateVersion })
              }
              action={
                <Button size="sm" disabled={busy === "update"} onClick={() => void applyUpdate()}>
                  {busy === "update" ? t("Updating…") : t("Update")}
                </Button>
              }
            />
          ) : null}
          {detail.manifest.ether?.contributes?.mediaKinds?.length ? (
            <Banner
              icon={<Info size={16} className="text-primary" />}
              text={t("Adds libraries: {names}", {
                names: detail.manifest.ether.contributes.mediaKinds
                  .map((k) => k.library.name)
                  .join(", "),
              })}
              action={
                <span className="flex gap-2">
                  {detail.manifest.ether?.contributes?.setup ? (
                    <Button size="sm" variant="outline" onClick={() => setSetupWizard(true)}>
                      {t("Run setup")}
                    </Button>
                  ) : null}
                  <Button size="sm" onClick={() => navigate("/libraries")}>
                    {t("Open Libraries")}
                  </Button>
                </span>
              }
            />
          ) : null}
          {!detail.enabled ? (
            <Banner
              icon={<PowerOff size={16} className="text-fg-subtle" />}
              text={t("It's off, so Ether doesn't use it.")}
              action={
                <Button
                  size="sm"
                  disabled={busy === "enable"}
                  onClick={() => void toggleEnabled(true)}
                >
                  {t("Turn on")}
                </Button>
              }
            />
          ) : null}
          {detail.shadow ? (
            <Banner
              icon={<FlaskConical size={16} className="text-info" />}
              text={t("Trying out: its changes are recorded, not applied.")}
              action={
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy === "live"}
                  onClick={() => void makeLive()}
                >
                  {t("Make it live")}
                </Button>
              }
            />
          ) : null}
          <MutationError error={mutationError} />

          {active === "overview" ? (
            <PluginOverview
              detail={detail}
              calls={calls}
              onClose={onClose}
              onRetry={retry}
              onFresh={() => void loadDetail(detail.id, true)}
              onActivity={() => setTab("activity")}
            />
          ) : active === "adds" ? (
            <PluginContributions
              detail={detail}
              calls={calls}
              onClose={onClose}
              onChanged={() => void reload()}
            />
          ) : active === "setup" ? (
            <>
              <PluginCatalogueSettings pluginId={detail.id} onSetup={() => setSetupWizard(true)} />{" "}
              <PluginSetup
                detail={detail}
                onDetail={() => loadDetail(detail.id, true)}
                onChanged={onChanged}
                onError={setMutationError}
              />
            </>
          ) : (
            <PluginActivity
              detail={detail}
              calls={calls}
              onReloadCalls={reloadCalls}
              journal={journal}
              onReloadJournal={async () => {
                await loadJournal(detail.id);
                await loadDetail(detail.id, true);
              }}
              liveLogLines={liveLogLines}
              onError={setMutationError}
              onChanged={onChanged}
            />
          )}

          <Dialog
            open={removeOpen}
            onClose={() => setRemoveOpen(false)}
            title={t("Remove {name}?", { name: detail.name })}
            size="sm"
            footer={
              <>
                <Button variant="ghost" onClick={() => setRemoveOpen(false)}>
                  {t("Cancel")}
                </Button>
                <Button
                  variant="destructive"
                  disabled={busy === "remove"}
                  onClick={() => void remove()}
                >
                  <Trash2 size={14} /> {busy === "remove" ? t("Removing…") : t("Remove")}
                </Button>
              </>
            }
          >
            <div className="space-y-4">
              <p className="text-sm text-fg-muted">
                {t("Its settings, storage and logs are deleted.")}
              </p>
              {applied > 0 ? (
                <label className="flex items-center justify-between gap-3 rounded-lg border border-border p-3 text-sm">
                  <span>
                    {t("Undo its changes first")}
                    <span className="block text-xs text-fg-muted">
                      {tn(
                        applied,
                        "{count} change to collections or watch state",
                        "{count} changes to collections or watch state",
                      )}
                    </span>
                  </span>
                  <Switch
                    checked={undoFirst}
                    onChange={(e) => setUndoFirst(e.currentTarget.checked)}
                    aria-label={t("Undo its changes first")}
                  />
                </label>
              ) : null}
            </div>
          </Dialog>
        </div>
      ) : null}
    </Drawer>
  );
}

/** Type, version and author, then health in one word, then About behind ⓘ. */
function HeaderLine({ detail }: { detail: PluginDetail }) {
  const health = healthSummary(detail);
  const ether = detail.manifest.ether;
  const author = ether?.author?.name;
  const edited = detail.sourceKind === "studio";
  // Who it comes from: an add-on names its server, never the owner as its
  // author; a Studio copy of an add-on says it was edited here.
  const origin = detail.host
    ? [edited ? t("Edited by you") : "", t("from {host}", { host: detail.host })]
    : edited
      ? [t("Made by you")]
      : [author ? t("by {author}", { author }) : ""];
  return (
    <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
      <span>
        {[kindLabel(detail.kind, isAddon(detail)), `v${detail.version}`, ...origin]
          .filter(Boolean)
          .join(" · ")}
      </span>
      <span className="inline-flex items-center gap-1.5 text-fg">
        <StatusDot level={health.level} pulse />
        {health.word}
      </span>
      <Tooltip
        content={
          <span className="block space-y-1 text-xs">
            <span className="block">
              {detail.summary || ether?.summary || detail.manifest.description}
            </span>
            <span className="block font-mono">{detail.id}</span>
            {ether?.license ? (
              <span className="block">{t("Licence: {licence}", { licence: ether.license })}</span>
            ) : null}
            {detail.host ? (
              <span className="block">{t("Plugin at {host}", { host: detail.host })}</span>
            ) : null}
            {!detail.verified ? <span className="block">{t("Not verified by Ether")}</span> : null}
          </span>
        }
      >
        <span className="inline-flex items-center gap-1 text-fg-subtle">
          <Info size={13} aria-label={t("About this plugin")} />
          {ether?.repo ? (
            <a
              href={ether.repo}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-0.5 text-xs text-primary hover:underline"
            >
              {t("Source")} <ExternalLink size={11} aria-hidden />
            </a>
          ) : null}
        </span>
      </Tooltip>
    </span>
  );
}

function Banner({ icon, text, action }: { icon: ReactNode; text: string; action: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-xl border border-border bg-bg-subtle px-4 py-3">
      {icon}
      <p className="min-w-0 flex-1 text-sm text-fg">{text}</p>
      {action}
    </div>
  );
}
