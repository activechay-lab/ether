import { ViewerCatalogRules } from "./ViewerCatalogRules";
import { Button, SearchSelect, SettingsSection, Switch, toast } from "@braedonsaunders/appkit-ui";
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { ChipGroup, NumberInput } from "../../components/fields";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import {
  RatingLimitRow,
  StreamLimitRow,
  userLimits,
  type UserLimits,
} from "../../components/UserLimits";
import { viewerName } from "../../components/pickers";
import { t } from "../../i18n";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { api, apiFetch, errorMessage } from "../../lib/api";
import type { Viewer } from "../../lib/contract";
import { useAsync } from "../../lib/hooks";
import { timeZones } from "../livetv/model";
import { PlexWatchSync } from "./sections/PlexWatchSync";
import { FromPlexDefaults } from "./FromPlexDefaults";
import { ViewerLiveTVAccess } from "./sections/ViewerLiveTV";
import { ViewerSimkl } from "./sections/ViewerSimkl";
import { ViewerTrakt } from "./sections/ViewerTrakt";
import { KID_PRESETS, ListText, normalizePolicy, type SafetyPolicy } from "./shared";
import { librariesApi } from "../libraries/api";

const HOURS = Array.from({ length: 24 }, (_, h) => ({
  value: h,
  label: `${String(h).padStart(2, "0")}:00`,
}));

/** A searchable IANA timezone picker; a stored zone the browser lacks stays listed. */
function TimeZoneSelect({ value, onChange }: { value: string; onChange: (zone: string) => void }) {
  const options = useMemo(() => {
    const zones = timeZones();
    const list = value && !zones.includes(value) ? [value, ...zones] : zones;
    return list.map((zone) => ({ value: zone, label: zone.replace(/_/g, " ") }));
  }, [value]);
  return (
    <SearchSelect
      value={value}
      onChange={onChange}
      options={options}
      ariaLabel={t("Playback timezone")}
      searchable
      {...searchSelectLabels()}
    />
  );
}

/** Libraries this viewer can open (empty restriction = every library, §1.4). */
function ViewerLibraries({
  userId,
  allowed,
  isOwner,
  onSaved,
}: {
  userId: number;
  allowed?: number[] | null;
  isOwner?: boolean;
  onSaved: () => void;
}) {
  const libs = useAsync((signal) => librariesApi.list(signal), []);
  const [draft, setDraft] = useState<number[] | null>(null);
  const [saving, setSaving] = useState(false);
  const all = libs.data ?? [];
  const current = draft ?? allowed ?? [];
  const unrestricted = current.length === 0;
  const dirty = draft !== null && JSON.stringify(draft) !== JSON.stringify(allowed ?? []);
  const toggleLibrary = (id: number, on: boolean) =>
    setDraft((prev) => {
      const base = prev ?? allowed ?? [];
      return on ? [...base, id].sort((a, b) => a - b) : base.filter((x) => x !== id);
    });
  return (
    <SettingsSection
      title={t("Libraries this viewer can open")}
      description={t(
        "With every library allowed, new libraries open for this viewer too. Pick libraries to limit them to just those.",
      )}
    >
      {libs.loading ? (
        <LoadingRows rows={2} />
      ) : (
        <>
          <SettingsRow
            title={t("All libraries")}
            control={
              <Switch
                aria-label={t("All libraries")}
                checked={unrestricted || Boolean(isOwner)}
                disabled={Boolean(isOwner)}
                onChange={(e) => setDraft(e.target.checked ? [] : all.map((l) => l.id))}
              />
            }
          />
          {!unrestricted && !isOwner
            ? all.map((l) => (
                <SettingsRow
                  key={l.id}
                  title={l.name}
                  control={
                    <Switch
                      aria-label={l.name}
                      checked={current.includes(l.id)}
                      onChange={(e) => toggleLibrary(l.id, e.target.checked)}
                    />
                  }
                />
              ))
            : null}
          {isOwner ? (
            <p className="px-5 py-4 text-sm text-fg-muted">
              {t("The owner can always open every library.")}
            </p>
          ) : (
            <div className="pt-2">
              <Button
                size="sm"
                disabled={!dirty || saving}
                onClick={() => {
                  setSaving(true);
                  api
                    .updateUser(userId, { allowedLibraries: current })
                    .then(() => {
                      toast.success(t("Saved"));
                      setDraft(null);
                      onSaved();
                    })
                    .catch((e: unknown) => toast.error(errorMessage(e)))
                    .finally(() => setSaving(false));
                }}
              >
                {t("Save")}
              </Button>
            </div>
          )}
        </>
      )}
    </SettingsSection>
  );
}

/**
 * Everything that decides what one viewer may watch and when: streams at
 * once, the content rating limit, parental controls, allowed times, Live TV
 * and their connected accounts. One in-place form; the save bar writes
 * whichever parts changed. Account actions run immediately.
 */
export function ViewerAccess({ viewer }: { viewer: Viewer }) {
  const id = viewer.id;
  const name = viewerName(viewer);
  const users = useAsync(() => api.users(), []);
  const config = useAsync(() => api.getConfig(), []);
  const policy = useAsync(
    async (signal) =>
      normalizePolicy(await apiFetch<SafetyPolicy>(`/safety/policies/${id}`, { signal })),
    [id],
  );
  const user = users.data?.find((u) => u.id === id);
  const limits = useDraft<UserLimits>(user ? userLimits(user) : null);
  const rules = useDraft<SafetyPolicy>(policy.data);
  const [saving, setSaving] = useState(false);
  const [presetBusy, setPresetBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const patchLimits = (p: Partial<UserLimits>) => limits.setDraft((d) => (d ? { ...d, ...p } : d));
  const patchPolicy = (p: Partial<SafetyPolicy>) => rules.setDraft((d) => (d ? { ...d, ...p } : d));

  const applyPreset = async (age: string) => {
    setPresetBusy(true);
    try {
      const { maxContentRating, ...preset } = await apiFetch<
        Partial<SafetyPolicy> & { maxContentRating?: string }
      >(`/safety/policies/presets/kids?age=${age}`);
      rules.setDraft(normalizePolicy(preset));
      if (maxContentRating !== undefined) patchLimits({ maxContentRating });
    } catch (e) {
      toast.error(t("Couldn't load the preset"), { description: errorMessage(e) });
    } finally {
      setPresetBusy(false);
    }
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      if (limits.dirty && limits.draft && user) {
        const saved = await api.updateUser(id, limits.draft);
        const next = saved ?? { ...user, ...limits.draft };
        users.setData((list) => (list ?? []).map((u) => (u.id === id ? next : u)));
      }
      if (rules.dirty && rules.draft) {
        await apiFetch(`/safety/policies/${id}`, {
          method: "PUT",
          body: JSON.stringify(rules.draft),
        });
        policy.setData(rules.draft);
      }
      toast.success(t("Preferences saved"), {
        description: t("{name}'s Ether preferences are up to date.", { name }),
      });
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };

  const l = limits.draft;
  const p = rules.draft;
  const noUser = users.data && (!user || !l);
  const trakt = config.data?.trakt;
  const simkl = config.data?.simkl;
  return (
    <div className="space-y-6">
      <ViewerCatalogRules key={id} id={id} region={String(config.data?.catalog?.region || "US")} />

      {users.error ? (
        <ErrorPanel error={users.error} onRetry={() => void users.reload()} />
      ) : policy.error ? (
        <ErrorPanel error={policy.error} onRetry={() => void policy.reload()} />
      ) : !users.data || !p ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          {noUser ? (
            <p className="text-sm text-fg-muted">
              {t(
                "This viewer has no user record on the server yet, so there are no limits to set.",
              )}
            </p>
          ) : null}
          <SettingsSection
            title={t("Parental controls")}
            description={t(
              "Adult titles are always excluded. Unrated titles are blocked when a rating limit applies.",
            )}
          >
            <SettingsRow
              title={t("Start from a preset")}
              description={t(
                "Replaces the parental controls, playback restrictions and allowed times below.",
              )}
              control={
                <div className="flex flex-wrap gap-2">
                  {KID_PRESETS.map((preset) => (
                    <Button
                      key={preset.value}
                      variant="outline"
                      size="sm"
                      disabled={presetBusy}
                      onClick={() => void applyPreset(preset.value)}
                    >
                      {preset.label}
                    </Button>
                  ))}
                </div>
              }
            />
            {user && l ? (
              <RatingLimitRow
                value={l.maxContentRating}
                onChange={(maxContentRating) => patchLimits({ maxContentRating })}
                isOwner={user.isOwner}
              />
            ) : null}
            <SettingsRow
              title={t("Blocked genres")}
              description={t("Comma-separated, e.g. Horror, Thriller.")}
              control={
                <div className="w-64">
                  <ListText
                    ariaLabel={t("Blocked genres")}
                    value={p.blockedGenres}
                    onChange={(v) => patchPolicy({ blockedGenres: v })}
                  />
                </div>
              }
            />
            <SettingsRow
              title={t("Blocked keywords")}
              description={t("Comma-separated TMDB keywords.")}
              control={
                <div className="w-64">
                  <ListText
                    ariaLabel={t("Blocked keywords")}
                    value={p.blockedKeywords}
                    onChange={(v) => patchPolicy({ blockedKeywords: v })}
                  />
                </div>
              }
            />
          </SettingsSection>

          <ViewerLibraries
            userId={id}
            allowed={user?.allowedLibraries}
            isOwner={user?.isOwner}
            onSaved={() => void users.reload()}
          />

          <SettingsSection title={t("Playback restrictions")}>
            {user && l ? (
              <StreamLimitRow
                value={l.streamLimit}
                onChange={(streamLimit) => patchLimits({ streamLimit })}
                defaultStreamLimit={config.data?.server.defaultStreamLimit}
                hint={
                  <Link
                    to="/playback/transcoding"
                    className="font-medium text-primary hover:underline"
                  >
                    {t("Change the server default")}
                  </Link>
                }
              />
            ) : null}
            <SettingsRow
              title={t("Local network only")}
              description={t("Block playback from outside your home network.")}
              control={
                <Switch
                  aria-label={t("Local network only")}
                  checked={p.remoteDisabled}
                  onChange={(e) => patchPolicy({ remoteDisabled: e.target.checked })}
                />
              }
            />
            <SettingsRow
              title={t("Maximum bitrate")}
              description={t("In kbps. 0 uses the server default.")}
              control={
                <NumberInput
                  aria-label={t("Maximum bitrate")}
                  className="w-28"
                  min={0}
                  value={p.maxBitrateKbps}
                  onChange={(n) => patchPolicy({ maxBitrateKbps: Math.max(0, Math.round(n)) })}
                />
              }
            />
          </SettingsSection>

          <SettingsSection
            title={t("Allowed times")}
            description={t(
              "Hours when this viewer can start playback. Leave every hour clear to allow any time.",
            )}
          >
            <SettingsRow
              title={t("Timezone")}
              control={
                <div className="w-64">
                  <TimeZoneSelect
                    value={p.timezone}
                    onChange={(timezone) => patchPolicy({ timezone })}
                  />
                </div>
              }
            />
            <SettingsRow stacked title={t("Allowed hours")}>
              <ChipGroup
                ariaLabel={t("Allowed hours")}
                size="sm"
                options={HOURS}
                value={p.allowedHours}
                onChange={(v) => patchPolicy({ allowedHours: [...v].sort((a, b) => a - b) })}
              />
            </SettingsRow>
          </SettingsSection>
        </>
      )}

      <FromPlexDefaults viewerId={id} show="share" />

      <ViewerLiveTVAccess viewerId={id} />

      <PlexWatchSync viewerId={id} plexId={viewer.plexId} />

      {config.data ? (
        <>
          <ViewerTrakt viewerId={id} ready={Boolean(trakt?.enabled && trakt.clientId.trim())} />
          <ViewerSimkl viewerId={id} ready={Boolean(simkl?.enabled && simkl.clientId.trim())} />
        </>
      ) : null}

      <SaveBar
        dirty={limits.dirty || rules.dirty}
        saving={saving}
        error={error}
        onSave={save}
        onDiscard={() => {
          limits.reset();
          rules.reset();
          setError(null);
        }}
      />
    </div>
  );
}
