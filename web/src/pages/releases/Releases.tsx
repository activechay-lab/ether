import { Fragment } from "react";
import { Link } from "react-router-dom";
import { Layers, ListFilter, MonitorCheck, ArrowRight } from "lucide-react";
import { WhoGetsWhat } from "./WhoGetsWhat";
import { localized, t } from "../../i18n";
import { ConfigSection } from "../../components/ConfigSection";
import { DomainPage, useRouteTab } from "../../components/layout";
import { RELEASE_SEARCH } from "../settings/schema";
import { StartSpeed } from "../playback/StartSpeed";
import { BlocklistTab } from "./Blocklist";
import { ClientRulesTab } from "./ClientRules";
import { HistoryTab } from "./History";
import { PinnedTab } from "./Pinned";
import { QualityTab } from "./Quality";

const TABS = localized(
  () =>
    [
      { key: "profiles", label: t("Profiles") },
      { key: "rules", label: t("Rules") },
      { key: "check", label: t("Check quality") },
      { key: "searching", label: t("Searching") },
      { key: "blocklist", label: t("Blocklist") },
      { key: "pinned", label: t("Pinned") },
      { key: "history", label: t("History") },
    ] as const,
);

/** Tabs that show the Profiles → Rules → Check quality step bar. */
const STEP_TABS = new Set(["profiles", "rules", "check"]);

/** Which release Ether picks for each play, and why. */
export default function Releases() {
  const tab = useRouteTab(TABS);
  return (
    <DomainPage title={t("Quality")} base="/quality" tabs={TABS} active={tab}>
      {STEP_TABS.has(tab) ? (
        <nav
          aria-label={t("How quality works")}
          className="flex flex-col items-stretch gap-3 sm:flex-row sm:items-center"
        >
          {[
            {
              key: "profiles",
              title: t("Profiles"),
              detail: t("Define quality tiers"),
              Icon: Layers,
            },
            {
              key: "rules",
              title: t("Rules"),
              detail: t("Pick a tier · first match wins"),
              Icon: ListFilter,
            },
            {
              key: "check",
              title: t("Check quality"),
              detail: t("See what a viewer gets"),
              Icon: MonitorCheck,
            },
          ].map(({ key, title, detail, Icon }, i) => {
            const content = (
              <>
                <Icon size={20} className="text-primary" />
                <span>
                  <span className="block text-sm font-medium">{title}</span>
                  <span className="text-xs text-fg-muted">{detail}</span>
                </span>
              </>
            );
            return (
              <Fragment key={key}>
                {i > 0 ? (
                  <ArrowRight
                    aria-hidden
                    size={18}
                    className="shrink-0 rotate-90 self-center text-fg-muted sm:rotate-0"
                  />
                ) : null}
                {tab === key ? (
                  <span
                    aria-current="step"
                    className="flex flex-1 items-center gap-3 rounded-xl border border-primary/40 bg-primary/5 p-4"
                  >
                    {content}
                  </span>
                ) : (
                  <Link
                    to={`/quality/${key}`}
                    className="flex flex-1 items-center gap-3 rounded-xl border border-border bg-surface p-4"
                  >
                    {content}
                  </Link>
                )}
              </Fragment>
            );
          })}
        </nav>
      ) : null}
      {tab === "check" ? <WhoGetsWhat /> : null}
      {tab === "profiles" ? <QualityTab /> : null}
      {tab === "rules" ? <ClientRulesTab /> : null}
      {tab === "searching" ? (
        <>
          <StartSpeed />
          <ConfigSection
            configKey="resolver"
            endpoint="resolver"
            groups={RELEASE_SEARCH}
            label={t("Searching")}
          />
        </>
      ) : null}
      {tab === "blocklist" ? <BlocklistTab /> : null}
      {tab === "pinned" ? <PinnedTab /> : null}
      {tab === "history" ? <HistoryTab /> : null}
    </DomainPage>
  );
}
