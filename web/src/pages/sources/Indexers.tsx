import {
  Alert,
  AlertDescription,
  Badge,
  EmptyState,
  SearchSelect,
  SettingsSection,
  confirmDialog,
  toast,
} from "@braedonsaunders/appkit-ui";
import { Globe, Link2, Plus, Puzzle, Search, Server } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useApp } from "../../app-context";
import { localized, t, tn } from "../../i18n";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { useLocale } from "../../i18n/react";
import { ActionMenu } from "../../components/ActionMenu";
import { MoreOptions } from "../../components/ConfigSection";
import { Meter } from "../../components/charts";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import {
  ListInput,
  SecretInput,
  Segmented,
  TestButton,
  type TestState,
} from "../../components/fields";
import { Section } from "../../components/layout";
import { SortableList } from "../../components/Sortable";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import { api, errorMessage, newId } from "../../lib/api";
import { isPlainObject } from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import type { Indexer, IndexerProtocol, SiteDefinition, SiteTestResult } from "../../lib/types";
import { useResolver } from "../releases/resolverDraft";
import { searchIdentityOptions } from "../settings/schema";
import { LANGUAGE_TAGS } from "../libraries/browser/CatalogPickers";
import { REGIONAL_TRACK_TAGS } from "../setup/language";
import { ClientIdentity } from "./ClientIdentity";
import { hasUsableDebrid } from "./Debrid";
import {
  BoolRow,
  NumRow,
  OrderButtons,
  RecordDrawer,
  TextRow,
  recordErrors,
  swap,
  toastError,
  useTests,
  type Errors,
} from "./form";
import { PluginSourceCards, SourceCard } from "./cards";
import { StremioMark } from "./marks";
import { normalizeAddonUrl, addonUrlHasDebridKey } from "../../components/addons/addons";
import {
  MissingDefinition,
  SiteDefinitionRow,
  SiteDefinitions,
  SitePicker,
  SiteSettingsFields,
  UploadDrawer,
  blankSiteIndexer,
  isBuiltin,
  siteKindLabel,
  siteProtocolLabel,
} from "./IndexerSites";

function blankIndexer(priority: number): Indexer {
  return {
    id: newId("idx"),
    name: "",
    url: "",
    apiKey: "",
    dailyLimit: 0,
    dailyGrabs: 0,
    priority,
    enabled: true,
    movieCategories: null,
    tvCategories: null,
    protocol: "usenet",
  };
}

/** A Stremio stream addon: torrent results streamed through debrid. */
function blankStremioIndexer(priority: number): Indexer {
  return { ...blankIndexer(priority), kind: "stremio", protocol: "torrent" };
}

/**
 * A self-hosted Zilean instance: its Torznab endpoint is searched like any
 * other torrent indexer, so results stream through debrid.
 */
function blankZileanIndexer(priority: number): Indexer {
  return { ...blankIndexer(priority), kind: "zilean", protocol: "torrent" };
}

export function isStremio(ix: { kind?: string }) {
  return ix.kind === "stremio";
}

export function isZilean(ix: { kind?: string }) {
  return ix.kind === "zilean";
}

/**
 * The per-indexer release language: the audio language of the releases an
 * indexer returns, used only to fill a bare DUAL/MULTI whose name names no
 * dub. Empty is Automatic — the site's own declared language, when it has
 * one.
 */
function ReleaseLanguageSelect({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  const { locale } = useLocale();
  const options = useMemo(() => {
    const display = (code: string) => {
      try {
        return new Intl.DisplayNames([locale], { type: "language" }).of(code) ?? code;
      } catch {
        return code;
      }
    };
    const seen = new Set<string>();
    const out: { value: string; label: string }[] = [{ value: "", label: t("Automatic") }];
    for (const tag of LANGUAGE_TAGS) {
      if (seen.has(tag.toLowerCase())) continue;
      seen.add(tag.toLowerCase());
      out.push({ value: tag, label: `${display(tag)} (${tag})` });
    }
    for (const tag of REGIONAL_TRACK_TAGS) {
      if (seen.has(tag.toLowerCase())) continue;
      seen.add(tag.toLowerCase());
      out.push({ value: tag, label: `${display(tag)} (${tag})` });
    }
    if (value && !out.some((o) => o.value === value)) {
      out.push({ value, label: `${display(value)} (${value})` });
    }
    return out.sort((a, b) => {
      if (a.value === "") return -1;
      if (b.value === "") return 1;
      return a.label.localeCompare(b.label, locale);
    });
  }, [locale, value]);
  return (
    <div className="w-56">
      <SearchSelect
        {...searchSelectLabels()}
        value={value}
        options={options}
        onChange={onChange}
        ariaLabel={t("Release language")}
        searchable
        noneLabel={t("Automatic")}
      />
    </div>
  );
}

export { addonUrlHasDebridKey, normalizeAddonUrl } from "../../components/addons/addons";

/** The addon's host, shown instead of a URL whose path may hold settings. */
function addonHost(url: string): string {
  try {
    return new URL(normalizeAddonUrl(url)).host;
  } catch {
    return url;
  }
}

const PROTOCOLS: { value: IndexerProtocol; label: string }[] = localized(() => [
  { value: "usenet", label: t("Usenet (Newznab)") },
  { value: "torrent", label: t("Torrent (Torznab)") },
]);

function isTorrent(ix: { protocol?: string }) {
  return ix.protocol === "torrent";
}

/** The indexer API name for a protocol, as shown on badges. */
function protocolLabel(ix: { protocol?: string }) {
  return isTorrent(ix) ? "Torznab" : "Newznab";
}

/** The host of an address, or the address as typed when it is not a URL. */
function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

function indexerName(ix: Indexer, i: number) {
  return (
    ix.name ||
    (isStremio(ix) && ix.url ? addonHost(ix.url) : ix.url) ||
    t("Indexer {number}", { number: i + 1 })
  );
}

/** A site's test: result count and a few sample titles, or the error. */
function SiteTestResults({ result }: { result: SiteTestResult }) {
  if (!result.ok) return null;
  const titles = result.titles ?? [];
  return (
    <Alert variant="success" role="status">
      <AlertDescription>
        <p>
          {titles.length
            ? tn(
                result.results,
                "Signed in and searched: {count} result, for example:",
                "Signed in and searched: {count} results, for example:",
              )
            : tn(
                result.results,
                "Signed in and searched: {count} result.",
                "Signed in and searched: {count} results.",
              )}
        </p>
        {titles.length ? (
          <ul className="mt-1 list-disc pl-5 text-xs">
            {titles.map((title) => (
              <li key={title}>{title}</li>
            ))}
          </ul>
        ) : null}
      </AlertDescription>
    </Alert>
  );
}

/**
 * Indexers in search order: sites, Newznab/Torznab URLs and Stremio addons,
 * each edited in a drawer, followed by the site definitions and their
 * settings edited in place. `version` changes when indexers were added
 * elsewhere (Prowlarr import).
 */
export function IndexerList({
  version = 0,
  addRequest,
}: {
  version?: number;
  addRequest?: { nonce: number; type: "site" | "newznab" | "stremio" | "zilean" } | null;
}) {
  const { dashboard, refreshDashboard } = useApp();
  const config = useAsync(() => api.getConfig(), [version]);
  const [sitesVersion, setSitesVersion] = useState(0);
  const sites = useAsync(() => api.sites.definitions(), [sitesVersion]);
  const statuses = useAsync(() => api.sites.status().catch(() => []), [version, sitesVersion]);
  const [editing, setEditing] = useState<{ index: number; draft: Indexer } | null>(null);
  const [picking, setPicking] = useState(false);
  /** Uploading a definition: "add" continues to a new indexer for it. */
  const [uploading, setUploading] = useState<"add" | "install" | null>(null);
  const [errors, setErrors] = useState<Errors>({});
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const { tests, runTest, reset } = useTests();
  const seenAdd = useRef(0);
  const [siteTest, setSiteTest] = useState<{ state: TestState; result?: SiteTestResult }>({
    state: { status: "idle" },
  });

  const cfg = config.data;
  const indexers = cfg?.indexers ?? [];
  const definitions = sites.data ?? null;
  // A definition installed a moment ago, until the reloaded list includes it.
  const [installed, setInstalled] = useState<SiteDefinition | null>(null);
  const defFor = (ix: Indexer): SiteDefinition | undefined =>
    definitions?.find((d) => d.id === ix.definition) ??
    (installed && installed.id === ix.definition ? installed : undefined);
  const statusFor = (ix: Indexer) => statuses.data?.find((s) => s.indexerId === ix.id);
  const inUse = useMemo(
    () => new Set(indexers.filter(isBuiltin).map((ix) => ix.definition ?? "")),
    [indexers],
  );
  const status = dashboard?.sections?.indexers;
  const live = Array.isArray(status) ? status.filter(isPlainObject) : [];
  const statFor = (ix: Indexer) => live.find((s) => s.indexer === ix.name || s.id === ix.id);

  async function commit(list: Indexer[], message: string) {
    await api.putSection(
      "indexers",
      list.map((ix, priority) => ({ ...ix, priority })),
    );
    toast.success(message);
    await config.reload();
    void statuses.reload();
    void refreshDashboard();
  }

  const open = (index: number, draft: Indexer) => {
    setErrors({});
    setError(null);
    reset(draft.id);
    setSiteTest({ state: { status: "idle" } });
    setPicking(false);
    setEditing({ index, draft: structuredClone(draft) });
  };
  const close = () => {
    if (!saving) setEditing(null);
  };
  const addSite = (def: SiteDefinition) => {
    setInstalled(def);
    open(indexers.length, blankSiteIndexer(def, indexers.length));
  };
  useEffect(() => {
    if (!addRequest || !cfg || seenAdd.current === addRequest.nonce) return;
    seenAdd.current = addRequest.nonce;
    if (addRequest.type === "site") {
      setEditing(null);
      setPicking(true);
      return;
    }
    open(
      indexers.length,
      addRequest.type === "stremio"
        ? blankStremioIndexer(indexers.length)
        : addRequest.type === "zilean"
          ? blankZileanIndexer(indexers.length)
          : blankIndexer(indexers.length),
    );
  }, [addRequest, cfg, indexers.length]);

  async function save() {
    if (!editing) return;
    const { index } = editing;
    const draft = isStremio(editing.draft)
      ? {
          ...editing.draft,
          url: normalizeAddonUrl(editing.draft.url),
          protocol: "torrent" as const,
        }
      : isZilean(editing.draft)
        ? { ...editing.draft, url: editing.draft.url.trim(), protocol: "torrent" as const }
        : editing.draft;
    const list =
      index < indexers.length
        ? indexers.map((ix, j) => (j === index ? draft : ix))
        : [...indexers, draft];
    setSaving(true);
    setErrors({});
    setError(null);
    try {
      await commit(list, index < indexers.length ? t("Indexer saved") : t("Indexer added"));
      setEditing(null);
    } catch (e) {
      setError(e);
      setErrors(recordErrors(e, `indexers[${index}].`));
    } finally {
      setSaving(false);
    }
  }

  async function remove() {
    if (!editing) return;
    const { index, draft } = editing;
    const ok = await confirmDialog({
      title: t("Remove indexer?"),
      message: t("{indexer} will no longer be searched.", { indexer: indexerName(draft, index) }),
      confirmLabel: t("Remove"),
      tone: "danger",
    });
    if (!ok) return;
    setSaving(true);
    setError(null);
    try {
      await commit(
        indexers.filter((_, j) => j !== index),
        t("Indexer removed"),
      );
      setEditing(null);
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  }

  async function move(i: number, d: -1 | 1) {
    try {
      await commit(swap(indexers, i, i + d), t("Indexer order saved"));
    } catch (e) {
      toastError(t("Couldn't reorder indexers"), e);
    }
  }

  async function testSite(draft: Indexer) {
    setSiteTest({ state: { status: "running" } });
    try {
      const r = await api.sites.test(draft);
      setSiteTest({
        state: r.ok
          ? { status: "ok", durationMs: r.durationMs }
          : { status: "fail", message: r.error ?? t("failed") },
        result: r,
      });
      void statuses.reload();
    } catch (e) {
      setSiteTest({ state: { status: "fail", message: errorMessage(e) } });
    }
  }

  if (config.error) return <ErrorPanel error={config.error} onRetry={() => void config.reload()} />;
  if (!cfg) return <LoadingRows rows={3} />;

  const draft = editing?.draft;
  const set = (patch: Partial<Indexer>) =>
    setEditing((e) => (e ? { ...e, draft: { ...e.draft, ...patch } } : e));
  const isNew = editing ? editing.index >= indexers.length : false;
  const stat = draft ? statFor(draft) : undefined;
  const used = Number(stat?.hitsUsed ?? 0);
  const needsDebrid = indexers.some((ix) => isTorrent(ix) && ix.enabled) && !hasUsableDebrid(cfg);
  const builtin = draft ? isBuiltin(draft) : false;
  const stremio = draft ? isStremio(draft) : false;
  const zilean = draft ? isZilean(draft) : false;
  const draftDef = draft && builtin ? defFor(draft) : undefined;

  return (
    <>
      {needsDebrid ? (
        <Alert variant="info" role="status">
          <AlertDescription>
            {t("Torrent indexers need a debrid account to stream from.")}{" "}
            <Link to="/sources/debrid" className="font-medium underline underline-offset-2">
              {t("Add a debrid provider")}
            </Link>
          </AlertDescription>
        </Alert>
      ) : null}
      <Section
        id="sources-indexers"
        title={t("Indexers")}
        description={t(
          "Searched in order when someone presses Play. Ether follows each service's limits automatically.",
        )}
        actions={
          <ActionMenu
            label={t("Add indexer")}
            variant="default"
            size="sm"
            items={[
              {
                key: "site",
                label: t("Site…"),
                icon: Globe,
                onSelect: () => {
                  setEditing(null);
                  setPicking(true);
                },
              },
              {
                key: "newznab",
                label: t("Newznab or Torznab URL…"),
                icon: Link2,
                onSelect: () => open(indexers.length, blankIndexer(indexers.length)),
              },
              {
                key: "stremio",
                label: t("Stremio addon URL…"),
                icon: Puzzle,
                onSelect: () => open(indexers.length, blankStremioIndexer(indexers.length)),
              },
              {
                key: "zilean",
                label: t("Zilean instance…"),
                icon: Server,
                onSelect: () => open(indexers.length, blankZileanIndexer(indexers.length)),
              },
            ]}
          >
            <Plus size={14} /> {t("Add indexer")}
          </ActionMenu>
        }
        bodyClassName="space-y-3 p-4"
      >
        {indexers.length === 0 ? (
          <EmptyState
            icon={<Search />}
            title={t("No indexers")}
            description={t(
              "Add a site, a Newznab or Torznab indexer, a Stremio addon or a Zilean instance so Ether can find releases.",
            )}
          />
        ) : (
          <SortableList
            items={indexers}
            getId={(ix) => ix.id}
            className="space-y-3"
            onReorder={(next) => void commit(next, t("Indexer order saved"))}
            renderItem={(ix, handle, i) => {
              const s = statFor(ix);
              const label = indexerName(ix, i);
              const hits = Number(s?.hitsUsed ?? 0);
              const site = isBuiltin(ix);
              const def = site ? defFor(ix) : undefined;
              const tested = site ? statusFor(ix) : undefined;
              const countsSearches = site || isStremio(ix);
              return (
                <SourceCard
                  name={label}
                  position={i + 1}
                  logo={isStremio(ix) ? <StremioMark /> : undefined}
                  description={[
                    i === 0 ? t("Searched first") : t("Priority {number}", { number: i + 1 }),
                    site && ix.url ? hostOf(ix.url) : "",
                    site && def && def.name !== label
                      ? t("{name} definition", { name: def.name })
                      : "",
                    ix.dailyLimit > 0
                      ? s
                        ? countsSearches
                          ? tn(
                              ix.dailyLimit,
                              "{used} of {count} search used today",
                              "{used} of {count} searches used today",
                              { used: hits },
                            )
                          : tn(
                              ix.dailyLimit,
                              "{used} of {count} API request used today",
                              "{used} of {count} API requests used today",
                              { used: hits },
                            )
                        : countsSearches
                          ? tn(ix.dailyLimit, "{count} search a day", "{count} searches a day")
                          : tn(
                              ix.dailyLimit,
                              "{count} API request a day",
                              "{count} API requests a day",
                            )
                      : site
                        ? ""
                        : t("Automatic limits"),
                    ix.dailyGrabs > 0
                      ? isTorrent(ix)
                        ? tn(
                            ix.dailyGrabs,
                            "{count} torrent download a day",
                            "{count} torrent downloads a day",
                          )
                        : tn(
                            ix.dailyGrabs,
                            "{count} NZB download a day",
                            "{count} NZB downloads a day",
                          )
                      : "",
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                  meta={
                    <>
                      {site ? (
                        <>
                          <Badge variant="outline">{siteKindLabel(ix.protocol)}</Badge>
                          {definitions && !def ? (
                            <Badge variant="destructive">{t("Definition missing")}</Badge>
                          ) : null}
                          {tested ? (
                            <Badge
                              variant={tested.ok ? "success" : "destructive"}
                              title={tested.message}
                            >
                              {tested.ok ? t("Test passed") : t("Test failed")}
                            </Badge>
                          ) : null}
                        </>
                      ) : isStremio(ix) ? (
                        <Badge variant="outline">{t("Stremio addon")}</Badge>
                      ) : isZilean(ix) ? (
                        <Badge variant="outline">{t("Zilean")}</Badge>
                      ) : (
                        <Badge variant="outline">{protocolLabel(ix)}</Badge>
                      )}
                      {!ix.enabled ? <Badge variant="secondary">{t("Off")}</Badge> : null}
                      {s && Number(s.retryAt ?? 0) > Date.now() / 1000 ? (
                        <Badge variant="warning">
                          {t("Waiting for service · retries automatically")}
                        </Badge>
                      ) : s &&
                        ((ix.dailyLimit > 0 && hits >= ix.dailyLimit) ||
                          (ix.dailyGrabs > 0 && Number(s.grabsUsed ?? 0) >= ix.dailyGrabs)) ? (
                        <Badge variant="warning">{t("Personal cap reached")}</Badge>
                      ) : null}
                    </>
                  }
                  order={
                    <OrderButtons
                      label={label}
                      i={i}
                      n={indexers.length}
                      onMove={(d) => void move(i, d)}
                    />
                  }
                  handle={handle}
                  editLabel={t("Edit {name}", { name: label })}
                  onEdit={() => open(i, ix)}
                />
              );
            }}
          />
        )}
      </Section>

      <ClientIdentity indexers={indexers} onSaved={() => void config.reload()} />
      <PluginSourceCards
        classes={["indexer", "direct"]}
        sectionTitle={t("Plugin sources")}
        sectionDescription={t(
          "Release indexers and direct or local folder sources contributed by enabled plugins.",
        )}
      />

      <SiteDefinitions
        cfg={cfg}
        definitions={definitions}
        definitionsError={sites.error}
        inUse={inUse}
        onAdd={addSite}
        onUpload={() => setUploading("install")}
        onConfigSaved={() => config.reload()}
        onDefinitionsChanged={() => setSitesVersion((v) => v + 1)}
      />

      <UploadDrawer
        open={uploading !== null}
        onClose={() => setUploading(null)}
        onInstalled={(def) => {
          const next = uploading;
          setUploading(null);
          setSitesVersion((v) => v + 1);
          if (next === "add") addSite(def);
        }}
      />

      <Drawer
        open={picking}
        onClose={() => setPicking(false)}
        title={t("Add a site")}
        description={t("Pick the site's definition. Its settings come next.")}
        size="lg"
      >
        {sites.error ? (
          <ErrorPanel error={sites.error} onRetry={() => void sites.reload()} />
        ) : definitions === null ? (
          <LoadingRows rows={3} />
        ) : (
          <SitePicker
            definitions={definitions}
            inUse={inUse}
            onInstalled={() => setSitesVersion((v) => v + 1)}
            onPick={addSite}
            onUpload={() => {
              setPicking(false);
              setUploading("add");
            }}
          />
        )}
      </Drawer>

      <RecordDrawer
        open={editing !== null}
        onClose={close}
        title={
          isNew
            ? builtin
              ? (draftDef?.name ?? t("New site"))
              : stremio
                ? t("New Stremio addon")
                : zilean
                  ? t("New Zilean instance")
                  : t("New indexer")
            : draft && editing
              ? indexerName(draft, editing.index)
              : ""
        }
        description={
          draft
            ? builtin
              ? siteKindLabel(draft.protocol)
              : stremio
                ? t("Stremio addon · Torrent")
                : zilean
                  ? t("Zilean · Torrent")
                  : isTorrent(draft)
                    ? t("Torznab indexer")
                    : t("Newznab indexer")
            : undefined
        }
        onSave={() => void save()}
        saving={saving}
        saveLabel={t("Save indexer")}
        onRemove={isNew ? undefined : () => void remove()}
        removeLabel={t("Remove indexer")}
        error={error}
        tools={
          draft ? (
            builtin ? (
              <TestButton
                state={siteTest.state}
                onTest={() => void testSite(draft)}
                disabled={!draftDef}
              />
            ) : (
              <TestButton
                state={tests[draft.id] ?? { status: "idle" }}
                onTest={() =>
                  runTest(draft.id, () =>
                    api.testIndexer(
                      stremio
                        ? { ...draft, url: normalizeAddonUrl(draft.url), protocol: "torrent" }
                        : draft,
                    ),
                  )
                }
                disabled={!draft.url}
              />
            )
          ) : null
        }
      >
        {draft ? (
          <>
            {stat && draft.dailyLimit > 0 ? (
              <Meter
                value={used / draft.dailyLimit}
                label={tn(
                  used,
                  "{count} API request used today",
                  "{count} API requests used today",
                )}
                valueLabel={t("{left} left of {limit}", {
                  left: Number(stat.hitsRemaining ?? 0),
                  limit: draft.dailyLimit,
                })}
                tone={
                  used >= draft.dailyLimit
                    ? "danger"
                    : used / draft.dailyLimit > 0.8
                      ? "warning"
                      : "primary"
                }
              />
            ) : null}
            {builtin && definitions && !draftDef ? (
              <MissingDefinition id={draft.definition} />
            ) : null}
            {builtin && siteTest.result ? <SiteTestResults result={siteTest.result} /> : null}
            {builtin ? (
              <SettingsSection title={t("Site")}>
                <TextRow label={t("Name")} value={draft.name} onChange={(v) => set({ name: v })} />
                <TextRow
                  label={t("Site address")}
                  type="url"
                  value={draft.url}
                  placeholder={draftDef?.links[0]}
                  help={
                    draftDef && draftDef.links.length > 1
                      ? t("Also: {links}", { links: draftDef.links.slice(1).join(", ") })
                      : undefined
                  }
                  error={errors.url}
                  onChange={(v) => set({ url: v })}
                />
                {draftDef ? <SiteDefinitionRow def={draftDef} /> : null}
                <SettingsRow
                  title={t("Protocol")}
                  description={
                    isTorrent(draft)
                      ? t("Torrent releases stream through your debrid account.")
                      : t("Usenet releases stream from your Usenet providers.")
                  }
                  control={<Badge variant="outline">{siteProtocolLabel(draft.protocol)}</Badge>}
                />
                <BoolRow
                  label={t("Enabled")}
                  value={draft.enabled}
                  onChange={(v) => set({ enabled: v })}
                />
              </SettingsSection>
            ) : stremio ? (
              <SettingsSection title={t("Addon")}>
                <TextRow label={t("Name")} value={draft.name} onChange={(v) => set({ name: v })} />
                <TextRow
                  label={t("Addon URL")}
                  type="url"
                  value={draft.url}
                  placeholder="https://addon.example/manifest.json"
                  help={t(
                    "Paste the addon's install link. Choose an addon that exposes torrent results; direct or proxied playback links alone are not supported. Ether streams through your configured debrid accounts.",
                  )}
                  error={errors.url}
                  onChange={(v) => set({ url: v })}
                />
                {draft.url.includes("torrentio.strem.fun") ? (
                  <p className="px-5 text-sm text-fg-muted">
                    {t(
                      "Lookups go to torrentio.strem.fun. Prefer your own? Use Torznab (Prowlarr, Jackett, Zilean).",
                    )}
                  </p>
                ) : null}
                {addonUrlHasDebridKey(draft.url) ? (
                  <Alert variant="warning" role="status">
                    <AlertDescription>
                      {t(
                        "This link includes a debrid API key. Ether checks your debrid accounts itself, so the addon doesn't need it: a key only adds cached hints, and it is then also stored in this link on this server.",
                      )}
                    </AlertDescription>
                  </Alert>
                ) : null}
                <BoolRow
                  label={t("Enabled")}
                  value={draft.enabled}
                  onChange={(v) => set({ enabled: v })}
                />
              </SettingsSection>
            ) : zilean ? (
              <SettingsSection title={t("Instance")}>
                <TextRow label={t("Name")} value={draft.name} onChange={(v) => set({ name: v })} />
                <TextRow
                  label={t("Instance URL")}
                  type="url"
                  value={draft.url}
                  placeholder="http://zilean:8181"
                  help={t(
                    "Paste the Zilean instance's address. Ether queries its Torznab API and streams the torrents it indexes through your debrid account.",
                  )}
                  error={errors.url}
                  onChange={(v) => set({ url: v })}
                />
                <BoolRow
                  label={t("Enabled")}
                  value={draft.enabled}
                  onChange={(v) => set({ enabled: v })}
                />
              </SettingsSection>
            ) : (
              <>
                <SettingsSection title={t("Connection")}>
                  <SettingsRow
                    title={t("Protocol")}
                    description={
                      isTorrent(draft)
                        ? t("Torrent releases stream through your debrid account.")
                        : t("Usenet releases stream from your Usenet providers.")
                    }
                    control={
                      <Segmented
                        ariaLabel={t("Protocol")}
                        size="sm"
                        options={PROTOCOLS}
                        value={isTorrent(draft) ? "torrent" : "usenet"}
                        onChange={(v) => set({ protocol: v })}
                      />
                    }
                  />
                  <TextRow
                    label={t("Name")}
                    value={draft.name}
                    onChange={(v) => set({ name: v })}
                  />
                  <TextRow
                    label={t("URL")}
                    type="url"
                    value={draft.url}
                    placeholder="https://indexer.example.com"
                    help={t("/api is added automatically.")}
                    error={errors.url}
                    onChange={(v) => set({ url: v })}
                  />
                  <SettingsRow stacked title={t("API key")}>
                    <SecretInput
                      ariaLabel={t("API key")}
                      value={draft.apiKey}
                      onChange={(v) => set({ apiKey: v })}
                    />
                  </SettingsRow>
                  <BoolRow
                    label={t("Enabled")}
                    value={draft.enabled}
                    onChange={(v) => set({ enabled: v })}
                  />
                </SettingsSection>
                <SettingsSection
                  title={t("Client identity")}
                  description={t("What this indexer sees Ether as.")}
                >
                  <SettingsRow
                    title={t("Identity")}
                    description={t("Inherit uses the Client identity card on the Indexers page.")}
                    control={
                      <div className="w-56">
                        <SearchSelect
                          value={draft.clientIdentity ?? ""}
                          onChange={(v) => set({ clientIdentity: v })}
                          options={[
                            { value: "", label: t("Inherit") },
                            ...searchIdentityOptions(false),
                          ]}
                          ariaLabel={t("Identity")}
                          placeholder={t("Select…")}
                          searchPlaceholder={t("Search…")}
                          noMatchesLabel={t("No matches")}
                          selectLabel={t("Select")}
                          closeLabel={t("Close")}
                        />
                      </div>
                    }
                  />
                </SettingsSection>
              </>
            )}
            {builtin && draftDef ? (
              <SiteSettingsFields def={draftDef} draft={draft} onChange={set} errors={errors} />
            ) : null}
            <MoreOptions label={t("More options")}>
              {stremio ? null : (
                <SettingsSection
                  title={t("Release language")}
                  description={t(
                    "The audio language of the releases this indexer returns. It fills the dub a bare DUAL or MULTI release name leaves unnamed; a language named in a release always wins. Sites that declare their own language use it unless you set one here.",
                  )}
                >
                  <SettingsRow
                    title={t("Language")}
                    description={t("Automatic uses the site's own language, when it declares one.")}
                    control={
                      <ReleaseLanguageSelect
                        value={draft.language ?? ""}
                        onChange={(v) => set({ language: v })}
                      />
                    }
                  />
                </SettingsSection>
              )}
              <SettingsSection
                title={t("Optional daily caps")}
                description={
                  builtin
                    ? t(
                        "Sites report no limits to Ether. Add a cap if the site restricts how often you search or download.",
                      )
                    : stremio
                      ? t(
                          "Ether follows the addon's rate limit responses automatically. Add a cap if you want fewer searches.",
                        )
                      : t(
                          "Ether follows service limit responses automatically. Add a lower personal cap only if you want one.",
                        )
                }
              >
                <NumRow
                  label={builtin || stremio ? t("Searches per day") : t("API requests per day")}
                  value={draft.dailyLimit}
                  min={0}
                  help={
                    builtin || stremio
                      ? t("0 = no cap.")
                      : t("0 = automatic. Includes searches and capability checks.")
                  }
                  error={errors.dailyLimit}
                  onChange={(n) => set({ dailyLimit: n })}
                />
                {stremio ? null : (
                  <NumRow
                    label={
                      isTorrent(draft) ? t("Torrent downloads per day") : t("NZB downloads per day")
                    }
                    value={draft.dailyGrabs}
                    min={0}
                    help={
                      builtin ? t("0 = no cap.") : t("0 = automatic. Counts files, not movie data.")
                    }
                    error={errors.dailyGrabs}
                    onChange={(n) => set({ dailyGrabs: n })}
                  />
                )}
              </SettingsSection>
              {builtin || stremio ? null : (
                <SettingsSection title={t("Categories")}>
                  <SettingsRow
                    stacked
                    title={t("Movie categories")}
                    description={t(
                      "{protocol} category ids. Empty uses the standard movie categories.",
                      { protocol: protocolLabel(draft) },
                    )}
                  >
                    <ListInput
                      numeric
                      ariaLabel={t("Movie categories")}
                      value={draft.movieCategories}
                      onChange={(v) => set({ movieCategories: v.length ? (v as number[]) : null })}
                      placeholder="2000, 2040"
                    />
                  </SettingsRow>
                  <SettingsRow
                    stacked
                    title={t("TV categories")}
                    description={t("Empty uses the standard TV categories.")}
                  >
                    <ListInput
                      numeric
                      ariaLabel={t("TV categories")}
                      value={draft.tvCategories}
                      onChange={(v) => set({ tvCategories: v.length ? (v as number[]) : null })}
                      placeholder="5000, 5040"
                    />
                  </SettingsRow>
                </SettingsSection>
              )}
            </MoreOptions>
          </>
        ) : null}
      </RecordDrawer>
    </>
  );
}

/**
 * Sources › Indexers: the owner switch that lets a detail page's
 * file-availability check open on-demand add-on stream links. Off by
 * default; when off an undecided check never hides Play. Warm-ups,
 * prefetch, discovery and idle background work always refuse.
 */
export function AddonStreamsSection() {
  const { config, resolver } = useResolver();
  const saved = useMemo(
    () => (resolver ? Boolean(resolver.openOnDemandAddonStreams) : null),
    [resolver],
  );
  const { draft, setDraft, dirty, reset } = useDraft<boolean>(saved);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  if (config.error) return <ErrorPanel error={config.error} onRetry={() => void config.reload()} />;
  if (draft === null) return null;
  async function submit() {
    if (draft === null) return;
    setSaving(true);
    setError(null);
    try {
      await api.putSection("resolver", { openOnDemandAddonStreams: draft });
      toast.success(t("Setting saved"));
      await config.reload();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  }
  return (
    <SettingsSection title={t("Add-on streams")}>
      <BoolRow
        label={t("Let availability checks open add-on streams")}
        value={draft}
        help={t(
          "Off by default. When on, the file check behind a detail page may open an on-demand add-on link within its existing timeouts and limits. Opening a link can make the add-on fetch, import or download the release. Warm-ups, prefetch and other background work never open links.",
        )}
        onChange={setDraft}
      />
      <SaveBar
        dirty={dirty}
        saving={saving}
        error={error}
        onSave={submit}
        onDiscard={() => {
          reset();
          setError(null);
        }}
      />
    </SettingsSection>
  );
}
