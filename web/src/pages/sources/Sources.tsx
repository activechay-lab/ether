import { localized, t } from "../../i18n";
import { Button } from "@braedonsaunders/appkit-ui";
import { Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { DomainPage } from "../../components/layout";
import { ProwlarrImport } from "../../components/ProwlarrImport";
import { IPTVProviders } from "./IPTV";
import { DebridSources } from "./Debrid";
import { HealthStrip } from "./form";
import { IndexerList } from "./Indexers";
import { NewReleaseBudgetSection } from "../catalog/NewReleasesSettings";
import { UsenetProviders } from "./Usenet";
import { SourceStatistics, StatisticsPeriod } from "./statistics/Statistics";
import { AddSourceGallery, type GalleryChoice } from "./cards";
import type { DebridKind } from "../../lib/types";
import { debridHealth, iptvHealth } from "../../lib/health";
import { useApp } from "../../app-context";
import { tv, type Source as TVSource } from "../livetv/model";

const TABS = localized(
  () =>
    [
      { key: "debrid", label: t("Debrid") },
      { key: "usenet", label: t("Usenet") },
      { key: "iptv", label: t("IPTV") },
      { key: "indexers", label: t("Indexers") },
      { key: "statistics", label: t("Statistics") },
      { key: "usage", label: t("Usage") },
    ] as const,
);

type AddDebrid = { nonce: number; kind: DebridKind };
type AddIndexer = { nonce: number; type: "site" | "newznab" | "stremio" };

/**
 * Where playback comes from: one tab per source type, plus Statistics and Usage.
 * An unknown address opens Debrid.
 * Adding one starts from a gallery, then a drawer.
 */
export default function Sources() {
  const { tab: raw } = useParams();
  const navigate = useNavigate();
  const top = TABS.find((item) => item.key === raw)?.key ?? "debrid";
  const stats = top === "statistics" || top === "usage";
  const [params] = useSearchParams();
  const period = stats ? params.get("days") : null;
  const [importing, setImporting] = useState(false);
  const [gallery, setGallery] = useState(false);
  const [version, setVersion] = useState(0);
  const [debridAdd, setDebridAdd] = useState<AddDebrid | null>(null);
  const [usenetAdd, setUsenetAdd] = useState<number | null>(null);
  const [indexerAdd, setIndexerAdd] = useState<AddIndexer | null>(null);

  function pick(choice: GalleryChoice) {
    const nonce = Date.now();
    setGallery(false);
    if (choice.type === "iptv") {
      navigate(`/sources/iptv?new=${choice.kind}`);
    } else if (choice.type === "debrid") {
      navigate("/sources/debrid");
      setDebridAdd({ nonce, kind: choice.kind });
    } else if (choice.type === "usenet") {
      navigate("/sources/usenet");
      setUsenetAdd(nonce);
    } else if (choice.type === "prowlarr") {
      navigate("/sources/indexers");
      setImporting(true);
    } else {
      navigate("/sources/indexers");
      setIndexerAdd({ nonce, type: choice.type });
    }
  }

  return (
    <DomainPage
      title={t("Sources")}
      base="/sources"
      tabs={TABS}
      active={top}
      status={<SourcesHealth />}
      tabActions={stats ? <StatisticsPeriod /> : undefined}
      // Statistics and Usage share one period, so switching between them keeps it.
      tabHref={(key) =>
        period && (key === "statistics" || key === "usage")
          ? `/sources/${key}?days=${period}`
          : `/sources/${key}`
      }
      actions={
        !stats ? (
          <>
            <Button size="sm" onClick={() => setGallery(true)}>
              <Plus size={14} /> {t("Add a source")}
            </Button>
          </>
        ) : null
      }
    >
      {!stats && top !== "iptv" ? (
        <p className="text-sm text-fg-muted">
          {t("Drag a card to choose which account Ether tries first.")}
        </p>
      ) : null}
      {top === "debrid" ? <DebridSources addKind={debridAdd} /> : null}
      {top === "iptv" ? <IPTVProviders /> : null}
      {top === "usenet" ? <UsenetProviders addNonce={usenetAdd} /> : null}
      {top === "indexers" ? (
        <>
          <IndexerList version={version} addRequest={indexerAdd} />
          <NewReleaseBudgetSection />
        </>
      ) : null}
      {stats ? <SourceStatistics view={top} /> : null}
      <AddSourceGallery open={gallery} onClose={() => setGallery(false)} onPick={pick} />
      <Drawer
        open={importing}
        onClose={() => setImporting(false)}
        title={t("Import from Prowlarr")}
        description={t("Sync indexers from Prowlarr.")}
        size="lg"
      >
        <ProwlarrImport onImported={() => setVersion((v) => v + 1)} />
      </Drawer>
    </DomainPage>
  );
}

/** Debrid, Usenet, IPTV and indexer health above every Sources tab. */
function SourcesHealth() {
  const [iptv, setIptv] = useState<TVSource[] | null>(null);
  useEffect(() => {
    let live = true;
    tv.sources()
      .then((list) => live && setIptv(list))
      .catch(() => live && setIptv(null));
    return () => {
      live = false;
    };
  }, []);
  const { dashboard } = useApp();
  // Debrid health shows here only; it does not count toward server health.
  const extra = [debridHealth(dashboard?.sections?.debrid), iptvHealth(iptv)].filter(
    (h) => h !== null,
  );
  return <HealthStrip ids={["debrid", "usenet", "iptv", "indexers"]} extra={extra} />;
}
