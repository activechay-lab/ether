import { SubtabNav } from "../../components/layout";
import { Button, SettingsSection, buttonVariants, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useApp } from "../../app-context";
import { ConfigSection } from "../../components/ConfigSection";
import { DomainPage, useRouteTab } from "../../components/layout";
import { RestartButton } from "../../components/RestartDrawer";
import { SettingsRow } from "../../components/SettingsRow";
import { localized, t } from "../../i18n";
import { api } from "../../lib/api";
import Logs from "../Logs";
import { BackupsAndImport } from "./Backups";
import { ClusterOverview, useClustered } from "./Cluster";
import { AppConnections } from "./AppConnections";
import { OutboundInternet } from "./OutboundInternet";
import { ServerProtection } from "./ServerProtection";
import { ConnectedServices, integrationTabs, type ServiceTab } from "./ConnectedServices";
import * as S from "./schema";
import { UpdatesStatus } from "./sections";

function ServerStatus() {
  const { boot } = useApp();
  const name = boot.serverName || "Ether";
  return (
    <p className="text-sm text-fg-muted">
      {boot.setupComplete
        ? t("{name} is ready for viewers.", { name })
        : t("{name} still needs setup.", { name })}
    </p>
  );
}

/** Names, how apps reach this computer, how it reaches the internet, and setup. */
export function GeneralPage() {
  const { reloadBoot } = useApp();
  const clustered = useClustered();
  return (
    <DomainPage title={t("General")} base="/server">
      <ConfigSection
        configKey="server"
        endpoint="server"
        groups={(clustered ? S.clusterServerGroups() : S.SERVER)
          .filter((group) => group.id !== "connect")
          .map((group) =>
            group.id === "node"
              ? { ...group, more: group.more?.filter((field) => field.key === "clusterWeight") }
              : group,
          )}
        label={t("General")}
        before={() => <ServerStatus />}
        onSaved={() => void reloadBoot()}
      />
      <AppConnections />
      <OutboundInternet />
      <RunSetupWizard />
    </DomainPage>
  );
}

function RunSetupWizard() {
  const navigate = useNavigate();
  const { boot, reloadBoot } = useApp();
  const [busy, setBusy] = useState(false);
  const reopen = async () => {
    setBusy(true);
    try {
      // Completed setup stays complete. Reopen does not wipe configuration;
      // clearing setupComplete would reopen tokenless LAN access.
      await api.setupReopen();
      await reloadBoot();
      navigate("/setup");
    } catch (e) {
      toast.error(t("Couldn't reopen setup"), { description: (e as Error).message });
    } finally {
      setBusy(false);
    }
  };
  return (
    <SettingsSection>
      <SettingsRow
        title={t("Setup")}
        description={
          boot.setupComplete
            ? t("Walk through setup again without erasing this server's settings.")
            : t("Finish choosing apps and configuring your server.")
        }
        control={
          boot.setupComplete ? (
            <Button variant="default" disabled={busy} onClick={() => void reopen()}>
              {t("Run setup wizard")}
            </Button>
          ) : (
            <Link to="/setup" className={buttonVariants({ variant: "default" })}>
              {t("Run setup wizard")}
            </Link>
          )
        }
      />
    </SettingsSection>
  );
}

export function ServicesPage() {
  const [enabled, setEnabled] = useState<Partial<Record<ServiceTab, boolean>>>({});
  const tabs = integrationTabs(enabled);
  const tab = useRouteTab(tabs);
  return (
    <DomainPage title={t("Integrations")} base="/services" tabs={tabs} active={tab}>
      <ConnectedServices tab={tab} onEnabled={setEnabled} />
    </DomainPage>
  );
}

const BACKUP_TABS = localized(
  () =>
    [
      { key: "backups", label: t("Backups") },
      { key: "import", label: t("Import & export") },
    ] as const,
);

const MAINTENANCE_TABS = localized(
  () =>
    [
      { key: "backups", label: t("Backups & import") },
      { key: "updates", label: t("Updates") },
      { key: "health", label: t("Health") },
      { key: "logs", label: t("Logs") },
    ] as const,
);

export function MaintenancePage() {
  const tab = useRouteTab(MAINTENANCE_TABS);
  return (
    <DomainPage
      title={t("Maintenance")}
      actions={<RestartButton />}
      base="/maintenance"
      tabs={MAINTENANCE_TABS}
      active={tab}
    >
      {tab === "backups" ? <BackupsSettings /> : null}
      {tab === "updates" ? (
        <ConfigSection
          configKey="updates"
          endpoint="updates"
          groups={S.UPDATES}
          label={t("Updates")}
          before={() => <UpdatesStatus />}
        />
      ) : null}
      {tab === "health" ? (
        <>
          <ServerProtection />
          <ConfigSection configKey="spike" endpoint="spike" groups={S.SPIKE} label={t("Health")} />
        </>
      ) : null}
      {tab === "logs" ? (
        <>
          <ConfigSection
            configKey="server"
            endpoint="server"
            groups={S.LOG_LEVEL}
            label={t("Logs")}
          />
          <Logs />
        </>
      ) : null}
    </DomainPage>
  );
}

function BackupsSettings() {
  const { backupTab } = useParams();
  const navigate = useNavigate();
  const tab = backupTab === "import" ? "import" : "backups";
  return (
    <>
      <SubtabNav
        ariaLabel={t("Backups & import sections")}
        tabs={[...BACKUP_TABS]}
        active={tab}
        onSelect={(key) => navigate(`/maintenance/backups/${key}`)}
      />
      <BackupsAndImport tab={tab} />
    </>
  );
}

export function ClusterPage() {
  return <ClusterOverview />;
}
