import { Button, SettingsSection, Spinner, Switch, toast } from "@braedonsaunders/appkit-ui";
import { AlertTriangle, RefreshCw } from "lucide-react";
import { useState } from "react";
import { ErrorPanel } from "../../../components/feedback";
import { NumberInput, SecretInput } from "../../../components/fields";
import { SaveBar, useDraft } from "../../../components/SaveBar";
import { SettingsRow } from "../../../components/SettingsRow";
import { localized, t, tn } from "../../../i18n";
import { apiFetch, errorMessage } from "../../../lib/api";
import { formatDateTime } from "../../../lib/format";
import { useAsync } from "../../../lib/hooks";

export type RSSFeedStatus = {
  configured: boolean;
  lastRead: number;
  imported: number;
  unmatchedTitles: number;
  problem?: string;
};

/** `/discovery/settings`: watchlist alerts plus the language-model features. */
export type DiscoverySettings = {
  enabled: boolean;
  plex: boolean;
  plexFriendsRssUrl?: string;
  plexOwnerRssUrl?: string;
  plexFriendsRssStatus?: RSSFeedStatus;
  plexOwnerRssStatus?: RSSFeedStatus;
  trakt: boolean;
  notify: boolean;
  prewarm: boolean;
  checkHours: number;
  naturalSearch: boolean;
  recaps: boolean;
  subtitleTranslate?: boolean;
  speechToText?: boolean;
  overviewTranslate?: boolean;
};

/**
 * Load and save the shared discovery settings object. Each caller edits only
 * its own fields; saving sends the whole object back, as the API expects.
 */
export function useDiscoverySettings() {
  const doc = useAsync(() => apiFetch<DiscoverySettings>("/discovery/settings"), []);
  const { draft, setDraft, dirty, reset } = useDraft(doc.data);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const save = async (toastTitle: string) => {
    if (!draft) return;
    setSaving(true);
    setError(null);
    try {
      const next = await apiFetch<DiscoverySettings>("/discovery/settings", {
        method: "PUT",
        body: JSON.stringify(draft),
      });
      doc.setData(next);
      toast.success(toastTitle);
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };
  const discard = () => {
    reset();
    setError(null);
  };
  return { doc, draft, setDraft, dirty, saving, error, save, discard };
}

const SOURCES = localized(
  () =>
    [
      {
        key: "plex",
        label: t("Plex watchlists"),
        hint: t(
          "Plex Home members only. A friend's server access does not include their watchlist.",
        ),
      },
      {
        key: "trakt",
        label: t("Trakt watchlists"),
        hint: t("Anyone who has connected Trakt, including friends."),
      },
      {
        key: "notify",
        label: t("Tell me too"),
        hint: t("Also send availability alerts to your notification channels."),
      },
    ] as const,
);

const RSS_PROBLEMS = localized<Record<string, string>>(() => ({
  auth: t("Plex rejected this feed URL. Generate a fresh RSS link in Plex settings."),
  rate_limited: t("Plex asked Ether to slow down. Ether will try again."),
  invalid_feed: t("The feed response was not valid RSS XML."),
  metadata: t("Catalogue lookup failed temporarily. Previous items were kept."),
  network: t("Plex RSS could not be reached. Ether will try again."),
}));

function FeedStatusLine({ status, sharedSet }: { status?: RSSFeedStatus; sharedSet: boolean }) {
  if (!status?.configured) return null;
  const when = status.lastRead
    ? formatDateTime(new Date(status.lastRead * 1000))
    : t("Not read yet");
  const imported = sharedSet
    ? tn(status.imported, "{count} title in set", "{count} titles in set")
    : tn(status.imported, "{count} item imported", "{count} items imported");
  const unmatched = tn(
    status.unmatchedTitles,
    "{count} unmatched title",
    "{count} unmatched titles",
  );
  const problem = status.problem ? (RSS_PROBLEMS[status.problem] ?? RSS_PROBLEMS.network) : "";
  return (
    <span className="mt-1.5 block space-y-1 text-xs text-fg-muted">
      <span className="block">
        {t("Last read: {when}", { when })} · {imported} · {unmatched}
      </span>
      {problem ? (
        <span className="inline-flex items-center gap-1.5 text-warning">
          <AlertTriangle size={13} aria-hidden />
          {problem}
        </span>
      ) : null}
    </span>
  );
}

/** Server-wide watchlist monitoring, edited in place on Catalog settings. */
export function WatchlistAlerts() {
  const s = useDiscoverySettings();
  const [syncingRss, setSyncingRss] = useState(false);
  if (s.doc.error) return <ErrorPanel error={s.doc.error} onRetry={() => void s.doc.reload()} />;
  if (!s.draft)
    return (
      <div className="py-10 text-center">
        <Spinner size={20} label={t("Loading watchlist settings")} />
      </div>
    );
  const draft = s.draft;
  const setRssField = (field: "plexFriendsRssUrl" | "plexOwnerRssUrl", nextValue: string) => {
    const savedVal = s.doc.data?.[field];
    if (savedVal === undefined && nextValue === "") {
      const copy = { ...draft };
      delete copy[field];
      s.setDraft(copy);
      return;
    }
    s.setDraft({ ...draft, [field]: nextValue });
  };
  const readRssNow = async () => {
    setSyncingRss(true);
    try {
      const next = await apiFetch<DiscoverySettings>("/discovery/rss/sync", {
        method: "POST",
        body: "{}",
      });
      s.doc.setData(next);
      toast.success(t("Watchlist RSS feeds read"));
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSyncingRss(false);
    }
  };
  const hasSavedRss = Boolean(
    s.doc.data?.plexFriendsRssStatus?.configured || s.doc.data?.plexOwnerRssStatus?.configured,
  );
  return (
    <>
      <SettingsSection
        title={t("Watchlists")}
        description={t(
          "A streamable watchlist title shows on their Home. A release date alone is not enough.",
        )}
      >
        <SettingsRow
          title={
            <label htmlFor="watchlist-enabled" data-setting="watchlist-monitoring">
              {t("Watchlist monitoring")}
            </label>
          }
          description={t("Check viewers' watchlists for titles that have become available.")}
          control={
            <Switch
              id="watchlist-enabled"
              aria-label={t("Watchlist monitoring")}
              checked={draft.enabled}
              onChange={(e) => s.setDraft({ ...draft, enabled: e.currentTarget.checked })}
            />
          }
        />
        {SOURCES.map((o) => (
          <SettingsRow
            key={o.key}
            setting={
              { plex: "plex-watchlists", trakt: "trakt-watchlists", notify: "tell-me-too" }[o.key]
            }
            title={<label htmlFor={`watchlist-${o.key}`}>{o.label}</label>}
            description={o.hint}
            control={
              <Switch
                id={`watchlist-${o.key}`}
                aria-label={o.label}
                checked={draft[o.key]}
                disabled={!draft.enabled}
                onChange={(e) => s.setDraft({ ...draft, [o.key]: e.currentTarget.checked })}
              />
            }
          />
        ))}
        <SettingsRow
          setting="plex-friends-watchlist-rss"
          title={<label htmlFor="watchlist-plex-friends-rss">{t("Friends' Watchlist RSS")}</label>}
          description={
            <>
              {t(
                "Paste the “Your Friends' Watchlist” RSS URL from plex.tv Watchlist settings. Plex doesn't say which friend added what, so it shows as a shared “Your friends want to watch” row.",
              )}
              <FeedStatusLine status={s.doc.data?.plexFriendsRssStatus} sharedSet />
            </>
          }
          control={
            <div className="w-full sm:w-80">
              <SecretInput
                id="watchlist-plex-friends-rss"
                ariaLabel={t("Friends' Watchlist RSS")}
                disabled={!draft.enabled}
                placeholder="https://rss.plex.tv/…"
                value={draft.plexFriendsRssUrl ?? ""}
                onChange={(v) => setRssField("plexFriendsRssUrl", v)}
              />
            </div>
          }
        />
        <SettingsRow
          setting="plex-your-watchlist-rss"
          title={<label htmlFor="watchlist-plex-owner-rss">{t("Your Watchlist RSS")}</label>}
          description={
            <>
              {t("Optional “Your Watchlist” RSS URL from plex.tv for the owner's watchlist.")}
              <FeedStatusLine status={s.doc.data?.plexOwnerRssStatus} sharedSet={false} />
            </>
          }
          control={
            <div className="w-full sm:w-80">
              <SecretInput
                id="watchlist-plex-owner-rss"
                ariaLabel={t("Your Watchlist RSS")}
                disabled={!draft.enabled}
                placeholder="https://rss.plex.tv/…"
                value={draft.plexOwnerRssUrl ?? ""}
                onChange={(v) => setRssField("plexOwnerRssUrl", v)}
              />
            </div>
          }
        />
        {hasSavedRss && !s.dirty ? (
          <div className="flex flex-wrap items-center gap-2 px-5 py-3">
            <Button
              type="button"
              variant="outline"
              size="sm"
              disabled={!draft.enabled || syncingRss}
              onClick={() => void readRssNow()}
            >
              <RefreshCw size={14} /> {t("Read RSS feeds now")}
            </Button>
          </div>
        ) : null}
        <SettingsRow
          title={
            <label htmlFor="watchlist-hours" data-setting="check-every">
              {t("Check every")}
            </label>
          }
          description={t("Minimum time between checks of the same watchlist.")}
          control={
            <span className="flex items-center gap-2">
              <NumberInput
                id="watchlist-hours"
                aria-label={t("Check every")}
                className="w-24"
                min={1}
                max={168}
                disabled={!draft.enabled}
                value={draft.checkHours}
                onChange={(n) => s.setDraft({ ...draft, checkHours: n })}
              />
              <span className="text-sm text-fg-muted">{t("hours")}</span>
            </span>
          }
        />
      </SettingsSection>
      <SaveBar
        dirty={s.dirty}
        saving={s.saving}
        error={s.error}
        onSave={() => s.save(t("Watchlist settings saved"))}
        onDiscard={s.discard}
      />
    </>
  );
}

/**
 * Opening-minute prefetch for a watchlist title that just became playable.
 * That is cache behaviour, so it lives on Cache rather than with the alerts.
 */
export function WarmNewArrivals() {
  const s = useDiscoverySettings();
  if (s.doc.error) return <ErrorPanel error={s.doc.error} onRetry={() => void s.doc.reload()} />;
  if (!s.draft)
    return (
      <div className="py-6 text-center">
        <Spinner size={18} label={t("Loading watchlist settings")} />
      </div>
    );
  const draft = s.draft;
  return (
    <>
      <SettingsSection title={t("New arrivals")}>
        <SettingsRow
          title={<label htmlFor="watchlist-prewarm">{t("Warm up new arrivals")}</label>}
          description={t(
            "Fetch the opening minutes as soon as a title becomes available, so it starts quickly. Needs watchlist monitoring in Catalog settings.",
          )}
          control={
            <Switch
              id="watchlist-prewarm"
              aria-label={t("Warm up new arrivals")}
              checked={draft.prewarm}
              disabled={!draft.enabled || s.saving}
              onChange={(e) => s.setDraft({ ...draft, prewarm: e.currentTarget.checked })}
            />
          }
        />
      </SettingsSection>
      <SaveBar
        dirty={s.dirty}
        saving={s.saving}
        error={s.error}
        onSave={() => s.save(t("Watchlist settings saved"))}
        onDiscard={s.discard}
      />
    </>
  );
}
