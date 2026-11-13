import { useSlots, PluginActionMenu } from "../../viewspec/slots/Slots";
import { PluginView } from "../../viewspec/PluginView";
import { SlotAnchor } from "../../components/SlotXray";
import { textOf } from "../../lib/extend";
import { SubtabNav } from "../../components/layout";
import {
  Avatar,
  Badge,
  DetailPageLayout,
  EmptyState,
  Spinner,
  toast,
} from "@braedonsaunders/appkit-ui";
import { ArrowLeft, CheckCircle2, Gauge, History } from "lucide-react";
import { useCallback, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { PageSubtabs } from "../../components/layout";
import { Artwork } from "../../components/Artwork";
import { Meter } from "../../components/charts";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { titleSubtitle } from "../../components/Poster";
import { viewerName } from "../../components/pickers";
import { t } from "../../i18n";
import SignInMethods from "../me/SignInMethods";
import { FavoritesMirror } from "../me/FavoritesMirror";
import { ConnectApps } from "../me/ConnectApps";
import { FavoriteEpisodeAlerts } from "../me/FavoriteEpisodeAlerts";
import { NotInterested, type NotInterestedEntry } from "../me/NotInterested";
import { getToken } from "../../lib/api";
import { api, apiFetch, errorMessage } from "../../lib/api";
import type { Viewer, ViewerDetail } from "../../lib/contract";
import { formatDateTime, formatPercent, relativeTime } from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import { titlePosterURL } from "../../lib/titles";
import { EMPTY_LAYOUT } from "../../lib/layout";
import { ContextBar, type ViewContext } from "../designer/ContextBar";
import { HomeCanvas } from "../designer/HomeCanvas";
import { ViewerWatchlist } from "./sections/ViewerWatchlist";
import { TasteView } from "./TasteView";
import { ViewerDashboard } from "./ViewerDashboard";
import { ViewerAccess } from "./ViewerAccess";
import { ViewerActions, TvCodeForm } from "./ViewerActions";
import { ViewerPlayback } from "./ViewerPlayback";
import { ViewerStatus, viewerLifecycle, viewerStatus } from "./ViewerStatus";

const TABS = [
  "overview",
  "home",
  "taste",
  "activity",
  "playback",
  "limits",
  "sign-in",
  "lists",
] as const;
type Tab = (typeof TABS)[number];
function asTab(raw: string | undefined): Tab | undefined {
  return raw && (TABS as readonly string[]).includes(raw) ? (raw as Tab) : undefined;
}

export default function ViewerProfile() {
  const { id: raw, tab: routeTab } = useParams();
  const id = Number(raw);
  const slots = useSlots("viewer.tab");
  const pluginTab = slots.find((d) => `plugin:${d.owner}:${d.meta?.key}` === routeTab);
  const tab = pluginTab ? routeTab! : (asTab(routeTab) ?? "overview");
  const navigate = useNavigate();
  const [search, setSearch] = useSearchParams();
  const listTab = ["alerts", "hidden"].includes(search.get("list") ?? "")
    ? search.get("list")!
    : "watchlist";

  const viewer = useAsync<ViewerDetail | null>(() => api.viewer(id), [id]);
  const taste = useAsync(() => api.taste(id), [id]);
  const [refreshing, setRefreshing] = useState(false);
  const loadFavorites = useCallback(
    () => apiFetch<{ enabled: boolean }>(`/viewers/${id}/favorites-mirror`),
    [id],
  );
  const saveFavorites = useCallback(
    (enabled: boolean) =>
      apiFetch(`/viewers/${id}/favorites-mirror`, {
        method: "POST",
        body: JSON.stringify({ enabled }),
      }),
    [id],
  );
  const loadAlerts = useCallback(
    () => apiFetch<{ enabled: boolean }>(`/viewers/${id}/favorite-episode-alert`),
    [id],
  );
  const loadHidden = useCallback(async (): Promise<NotInterestedEntry[]> => {
    const result = await apiFetch<{
      items: {
        handle: string;
        key?: string;
        title?: string;
        year?: number;
        poster?: string;
        posterPath?: string;
      }[];
    }>(`/viewers/${id}/not-interested`);
    return (result.items ?? []).map((item) => ({
      handle: item.handle,
      title: item.title,
      year: item.year,
      poster: item.poster ?? titlePosterURL(item, 92),
      unavailable: !item.title,
    }));
  }, [id]);
  const restoreHidden = useCallback(
    async (handle: string) => {
      const result = await apiFetch<{ restored: boolean }>(
        `/viewers/${id}/not-interested/restore`,
        { method: "POST", body: JSON.stringify({ handle }) },
      );
      if (!result.restored) throw new Error("restore failed");
    },
    [id],
  );

  const v = viewer.data;
  const setTab = (next: string) => navigate(`/viewers/${id}/${next}`);

  const refresh = async () => {
    setRefreshing(true);
    try {
      await api.refreshViewer(id);
      toast.success(t("Rows rebuilt"), { description: t("Their Home is up to date.") });
    } catch (e) {
      toast.error(t("Couldn't rebuild rows"), { description: errorMessage(e) });
    } finally {
      setRefreshing(false);
    }
  };

  if (viewer.error)
    return (
      <div className="p-6">
        <BackLink />
        <ErrorPanel error={viewer.error} onRetry={() => void viewer.reload()} />
      </div>
    );
  if (!v)
    return (
      <div className="grid flex-1 place-items-center">
        <Spinner size={22} label={t("Loading viewer")} />
      </div>
    );
  const name = viewerName(v);
  const life = viewerLifecycle(v);
  // Watchlist, alerts and connected apps need server access; a viewer kept
  // for imported history (or whose access ended) has none of them.
  const serverAccess = v.isOwner || viewerStatus(v) === "active";

  return (
    <DetailPageLayout
      header={
        <div>
          <BackLink />
          <div className="mt-2 flex flex-wrap items-center gap-4">
            <Avatar src={v.thumb} name={name} size={56} />
            <div className="min-w-0 flex-1">
              <h1 className="flex flex-wrap items-center gap-2 text-xl font-semibold text-fg">
                {name}
                {life.paused ? (
                  <Badge variant="warning">{t("Paused")}</Badge>
                ) : (
                  <ViewerStatus viewer={v} />
                )}
                {life.managedBy === "plex" ? <Badge>{t("Managed on plex.tv")}</Badge> : null}
              </h1>
              <p className="text-sm text-fg-muted">
                {v.username} · {t("Active {time}", { time: relativeTime(v.lastActive) })}
              </p>
              <ExperienceLink viewer={v} />
            </div>
            <PluginActionMenu
              slot="actionMenu.viewer"
              recordId={String(id)}
              onRefresh={() => void viewer.reload()}
            />
            <ViewerActions
              viewer={v}
              onChange={() => void viewer.reload()}
              onRebuild={() => void refresh()}
              rebuilding={refreshing}
              onPreviewHome={() => setTab("home")}
            />
          </div>
        </div>
      }
      subtabs={
        <SlotAnchor>
          <SubtabNav
            ariaLabel={t("Viewer sections")}
            active={tab}
            onSelect={setTab}
            tabs={[
              { key: "overview", label: t("Overview") },
              { key: "home", label: t("Home") },
              { key: "taste", label: t("Taste") },
              { key: "activity", label: t("Recent activity") },
              { key: "playback", label: t("Audio & subtitles") },
              { key: "limits", label: t("Limits") },
              { key: "sign-in", label: t("Sign-in & devices") },
              ...(serverAccess ? [{ key: "lists", label: t("Lists & alerts") }] : []),
              // Plugin tabs follow Ether's own sections.
              ...slots.map((d) => ({
                key: `plugin:${d.owner}:${d.meta!.key}`,
                label: textOf(d.label),
              })),
            ]}
          />
        </SlotAnchor>
      }
    >
      {pluginTab ? (
        <PluginView
          plugin={pluginTab.owner}
          page={pluginTab.meta!.key}
          params={{ recordType: "viewer", recordId: String(id) }}
        />
      ) : tab === "overview" ? (
        <ViewerDashboard viewer={v} taste={taste.data} />
      ) : tab === "limits" ? (
        <ViewerAccess key={id} viewer={v} />
      ) : tab === "playback" ? (
        <ViewerPlayback key={id} viewer={v} />
      ) : tab === "sign-in" ? (
        <div className="space-y-6">
          <SignInMethods
            base={`/users/${id}`}
            token={getToken()}
            owner
            showInvite={!v.isOwner}
            quickConnect={
              <section className="rounded-xl border border-border bg-surface p-5 space-y-4">
                <h2 className="font-semibold">{t("Approve a TV code")}</h2>
                <TvCodeForm key={id} viewer={v} />
              </section>
            }
            beforeDevices={
              serverAccess ? <ConnectApps base={`/viewers/${id}`} token={getToken()} /> : null
            }
          />
        </div>
      ) : tab === "home" ? (
        <TheirHome viewer={v} />
      ) : tab === "activity" ? (
        <Activity viewer={v} />
      ) : tab === "taste" ? (
        <div className="space-y-8">
          {taste.error ? (
            <ErrorPanel error={taste.error} onRetry={() => void taste.reload()} />
          ) : !taste.data ? (
            <LoadingRows rows={4} />
          ) : (
            <TasteView userId={id} taste={taste.data} onChange={(next) => taste.setData(next)} />
          )}
        </div>
      ) : (
        <div className="space-y-6">
          {serverAccess ? (
            <>
              <PageSubtabs
                ariaLabel={t("Lists and alerts sections")}
                active={listTab}
                onSelect={(next) =>
                  setSearch(
                    (previous) => {
                      previous.set("list", next);
                      return previous;
                    },
                    { preventScrollReset: true },
                  )
                }
                tabs={[
                  { key: "watchlist", label: t("Watchlist") },
                  { key: "alerts", label: t("Alerts") },
                  { key: "hidden", label: t("Not interested") },
                ]}
              />
              {listTab === "watchlist" ? (
                <>
                  <ViewerWatchlist viewerId={id} />
                  <FavoritesMirror load={loadFavorites} save={saveFavorites} />
                </>
              ) : listTab === "alerts" ? (
                <FavoriteEpisodeAlerts load={loadAlerts} />
              ) : (
                <NotInterested load={loadHidden} restore={restoreHidden} />
              )}
            </>
          ) : null}
        </div>
      )}
    </DetailPageLayout>
  );
}

function BackLink() {
  return (
    <Link
      to="/viewers"
      className="inline-flex items-center gap-1 text-sm text-fg-muted hover:text-fg"
    >
      <ArrowLeft size={14} /> {t("Viewers")}
    </Link>
  );
}

/** Deep link to this viewer's drawer in Activity › Experience. */
function ExperienceLink({ viewer }: { viewer: ViewerDetail }) {
  return (
    <Link
      to={`/activity/experience?viewer=${viewer.id}`}
      className="inline-flex items-center gap-1 text-sm font-medium text-primary hover:underline"
    >
      <Gauge size={14} /> {t("See this viewer's experience")}
    </Link>
  );
}

function Activity({ viewer }: { viewer: ViewerDetail }) {
  const recent = viewer.recent ?? [];
  if (recent.length === 0)
    return (
      <EmptyState
        icon={<History />}
        title={t("Nothing watched recently")}
        description={t("Plays, finishes and abandons appear here as they happen.")}
      />
    );
  return (
    <div className="overflow-auto rounded-xl border border-border bg-surface">
      <table className="w-full text-left">
        <thead className="bg-bg-subtle text-xs text-fg-muted">
          <tr>
            <th className="p-3">{t("Title")}</th>
            <th className="p-3">{t("Progress")}</th>
            <th className="p-3 text-right">{t("Last watched")}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {recent.map((r, i) => (
            <tr key={`${r.title.key}-${i}`}>
              <td className="p-3">
                <div className="flex items-center gap-3">
                  <Artwork
                    src={r.title.poster}
                    alt=""
                    title={r.title.title}
                    className="aspect-[2/3] w-12 shrink-0 rounded"
                  />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-fg">{r.title.title}</p>
                    <p className="truncate text-xs text-fg-muted">{titleSubtitle(r.title)}</p>
                  </div>
                </div>
              </td>
              <td className="p-3">
                {!r.completed ? (
                  <div className="mt-1.5 max-w-xs">
                    <Meter
                      value={r.progress}
                      valueLabel={formatPercent(r.progress)}
                      label={t("Stopped at")}
                    />
                  </div>
                ) : null}
              </td>
              <td className="p-3 text-right">
                {r.completed ? (
                  <Badge variant="success">
                    <CheckCircle2 size={11} /> {t("Finished")}
                  </Badge>
                ) : (
                  <Badge variant="warning">{t("In progress")}</Badge>
                )}
                <p className="mt-1 text-xs text-fg-subtle" title={formatDateTime(r.at)}>
                  {relativeTime(r.at)}
                </p>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function TheirHome({ viewer }: { viewer: Viewer }) {
  const now = new Date();
  const [ctx, setCtx] = useState<ViewContext>({
    device: "tv",
    day: now.getDay(),
    hour: now.getHours(),
    section: "home",
  });
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="w-full max-w-md">
          <ContextBar ctx={ctx} onChange={setCtx} />
        </div>
        <Link to="/home" className="text-sm font-medium text-primary hover:underline">
          {t("Shape it in Home →")}
        </Link>
      </div>
      <div>
        <HomeCanvas
          readOnly
          draft={EMPTY_LAYOUT}
          saved={EMPTY_LAYOUT}
          dirty={false}
          layer={viewer.id}
          layerName={viewerName(viewer)}
          ctx={ctx}
          viewers={[viewer]}
          viewerId={viewer.id}
          onSpotCheck={() => undefined}
          refreshNonce={0}
          onChange={() => undefined}
          onEditRow={() => undefined}
        />
      </div>
    </div>
  );
}
