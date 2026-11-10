import { Button, SettingsSection, Switch, toast } from "@braedonsaunders/appkit-ui";
import { RefreshCw, Wand2 } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { SettingsRow } from "../../components/SettingsRow";
import { ToolbarPicker } from "../../components/ToolbarPicker";
import { t } from "../../i18n";
import { errorMessage } from "../../lib/api";
import { formatDateTime } from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import { librariesApi, type Library, type Presentations } from "./api";
import { NewReleasesSettings, useAvailabilityDraft } from "./browser/NewReleasesSettings";
import { LibraryCacheCard } from "./LibraryCache";
import { SetupWizard } from "./SetupWizard";

const PLEX_BASES = ["movie", "show", "artist", "photo"];
const MB_TYPES = ["movies", "tvshows", "music", "audiobooks", "books", "photos", "homevideos"];
const STREMIO_TYPES = ["movie", "series", "channel", "tv"];
const XTREAM_TYPES = ["movie", "series", "live"];

function defaultPlexBase(lib: Library): string {
  if (lib.source === "plugin") return (lib.kind?.levels.length ?? 1) >= 3 ? "show" : "movie";
  return lib.mediaType === "show" || lib.filter?.type === "show" ? "show" : "movie";
}
function defaultMBType(lib: Library): string {
  return defaultPlexBase(lib) === "show" ? "tvshows" : "movies";
}

/** "Show in apps", per protocol (spec §6.2, §15.3). Builtins are synthesised. */
function PresentationsEditor({ library, onSaved }: { library: Library; onSaved: () => void }) {
  const [present, setPresent] = useState<Presentations>(library.present ?? {});
  const [busy, setBusy] = useState(false);
  useEffect(() => setPresent(library.present ?? {}), [library.present, library.id]);
  const dirty = JSON.stringify(present) !== JSON.stringify(library.present ?? {});

  const toggle = (key: keyof Presentations, on: boolean, make: () => unknown) =>
    setPresent((p) => {
      const next: Presentations = { ...p };
      if (on) (next as Record<string, unknown>)[key] = make();
      else delete (next as Record<string, unknown>)[key];
      return next;
    });

  const row = (
    key: "plex" | "mediaBrowser" | "stremio" | "xtream" | "silo",
    title: string,
    value: string,
    options: string[],
    make: (v: string) => unknown,
    current: string,
  ) => (
    <SettingsRow
      title={title}
      control={
        <span className="library-present-row">
          <Switch
            aria-label={t("Show in {app}", { app: title })}
            checked={Boolean(present[key])}
            onChange={(e) => toggle(key, e.target.checked, () => make(current))}
          />
          {present[key] ? (
            <ToolbarPicker
              ariaLabel={t("How {app} shows it", { app: title })}
              value={value}
              options={options.map((o) => ({ value: o, label: o }))}
              onChange={(v) => toggle(key, true, () => make(v))}
            />
          ) : null}
        </span>
      }
    />
  );

  return (
    <SettingsSection title={t("Show in apps")}>
      <p className="pb-2 text-sm text-fg-muted">
        {t("Choose where this library appears. Anything off stays just in this console.")}
      </p>
      {row(
        "plex",
        "Plex",
        present.plex?.base ?? defaultPlexBase(library),
        PLEX_BASES,
        (v) => ({ base: v }),
        defaultPlexBase(library),
      )}
      {row(
        "mediaBrowser",
        t("Jellyfin & Emby"),
        present.mediaBrowser?.collectionType ?? defaultMBType(library),
        MB_TYPES,
        (v) => ({ collectionType: v }),
        defaultMBType(library),
      )}
      {row(
        "stremio",
        "Stremio",
        present.stremio?.type ?? (defaultPlexBase(library) === "show" ? "series" : "movie"),
        STREMIO_TYPES,
        (v) => ({ type: v }),
        defaultPlexBase(library) === "show" ? "series" : "movie",
      )}
      {row(
        "xtream",
        "Xtream",
        present.xtream?.category ?? (defaultPlexBase(library) === "show" ? "series" : "movie"),
        XTREAM_TYPES,
        (v) => ({ category: v }),
        defaultPlexBase(library) === "show" ? "series" : "movie",
      )}
      {row(
        "silo",
        "Silo",
        present.silo?.type ?? defaultMBType(library),
        MB_TYPES,
        (v) => ({ type: v }),
        defaultMBType(library),
      )}
      {library.source === "plugin" ? (
        <SettingsRow
          title={t("Only in its own app")}
          description={t(
            "The plugin serves this library itself instead of through the apps above.",
          )}
          control={
            <Switch
              aria-label={t("Only in its own app")}
              checked={Boolean(present.adapterOnly)}
              onChange={(e) => toggle("adapterOnly", e.target.checked, () => true)}
            />
          }
        />
      ) : null}
      <div className="pt-2">
        <Button
          size="sm"
          disabled={busy || !dirty}
          onClick={() => {
            setBusy(true);
            librariesApi
              .patch(library.id, { present })
              .then(() => {
                toast.success(t("Saved"));
                onSaved();
              })
              .catch((e) => toast.error(errorMessage(e)))
              .finally(() => setBusy(false));
          }}
        >
          {t("Save")}
        </Button>
      </div>
    </SettingsSection>
  );
}

function FilterSummary({ library }: { library: Library }) {
  const f = library.filter ?? {};
  const parts: string[] = [];
  parts.push(f.type === "show" ? t("TV shows") : t("Movies"));
  if (Array.isArray(f.genres) && f.genres.length) parts.push((f.genres as string[]).join(", "));
  if (f.yearFrom || f.yearTo)
    parts.push(`${Number(f.yearFrom ?? "") || "…"}–${Number(f.yearTo ?? "") || "…"}`);
  if (typeof f.query === "string" && f.query) parts.push(`“${f.query}”`);
  if (f.group) parts.push(t("plus your filter choices"));
  return (
    <SettingsSection title={t("What is in it")}>
      <p className="text-sm">{parts.join(" · ")}</p>
      <p className="pt-1 text-sm text-fg-muted">
        {t(
          "Titles stay in Movies and TV Shows too — this library is another way to reach them. The filter itself cannot be changed yet; delete and recreate the library to change it.",
        )}
      </p>
    </SettingsSection>
  );
}

function ReleaseSection() {
  const availability = useAvailabilityDraft();
  if (!availability.draft) return null;
  return (
    <>
      <NewReleasesSettings
        availability={availability.draft}
        onAvailability={availability.setDraft}
      />
      {availability.saveBar}
    </>
  );
}

/** Per-library settings: only the sections that apply to this library (§11). */
export function LibrarySettings({ library, onSaved }: { library: Library; onSaved: () => void }) {
  const navigate = useNavigate();
  const [name, setName] = useState(library.name);
  const [busy, setBusy] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [wizard, setWizard] = useState(false);
  const setup = useAsync(
    (signal) =>
      library.source === "plugin"
        ? librariesApi.librarySetup(library.id, signal)
        : Promise.resolve(null),
    [library.id, library.source],
  );
  useEffect(() => setName(library.name), [library.name]);

  const patch = async (body: Record<string, unknown>, done: string) => {
    setBusy(true);
    try {
      await librariesApi.patch(library.id, body);
      toast.success(done);
      onSaved();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const syncNow = async (fullWalk: boolean) => {
    setSyncing(true);
    try {
      const res = await librariesApi.sync(library.id, fullWalk);
      toast.success(t("Sync finished: {count} updated", { count: res.upserted }));
      onSaved();
    } catch (e) {
      toast.error(errorMessage(e));
    } finally {
      setSyncing(false);
    }
  };

  const blockers = setup.data?.blockers ?? [];

  return (
    <div className="flex flex-col gap-2">
      {library.source === "plugin" && blockers.length ? (
        <p role="alert" className="library-setup-banner">
          {t("This library is waiting for setup. Finish it and the library can appear in apps.")}{" "}
          <Button size="sm" onClick={() => setWizard(true)}>
            <Wand2 size={14} /> {t("Finish setup")}
          </Button>
        </p>
      ) : null}
      <SettingsSection title={t("Basics")}>
        <SettingsRow
          title={t("Name")}
          description={t("The name viewers see in their apps.")}
          control={
            <span className="library-present-row">
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                aria-label={t("Library name")}
              />
              <Button
                size="sm"
                disabled={busy || !name.trim() || name.trim() === library.name}
                onClick={() => void patch({ name: name.trim() }, t("Library renamed"))}
              >
                {t("Save")}
              </Button>
            </span>
          }
        />
        <SettingsRow
          title={t("Hide from apps")}
          description={t("The library stays in this console but disappears from every app.")}
          control={
            <Switch
              aria-label={t("Hide from apps")}
              checked={library.hidden}
              disabled={busy}
              onChange={(e) => void patch({ hidden: e.target.checked }, t("Saved"))}
            />
          }
        />
        <SettingsRow
          title={t("Sharing")}
          description={t("Choose who can open each library on the Viewers page.")}
          control={
            <Link to="/viewers" className="text-sm text-primary underline">
              {t("Open Viewers")}
            </Link>
          }
        />
      </SettingsSection>

      {library.source === "builtin" ? (
        <SettingsSection title={t("Show in apps")}>
          <p className="text-sm text-fg-muted">
            {t("Movies and TV Shows appear in every app. Use “Hide from apps” above to hide one.")}
          </p>
          <p className="pt-2 text-sm">
            <Link to="/metadata" className="text-primary underline">
              {t("Metadata settings")}
            </Link>
            {" · "}
            <Link to="/libraries/settings" className="text-primary underline">
              {t("Catalog-wide settings")}
            </Link>
          </p>
        </SettingsSection>
      ) : (
        <PresentationsEditor library={library} onSaved={onSaved} />
      )}

      {library.source === "filter" ? <FilterSummary library={library} /> : null}

      {library.source !== "plugin" ? <ReleaseSection /> : null}

      {library.source === "plugin" ? (
        <>
          <SettingsSection title={t("Keeping it up to date")}>
            <SettingsRow
              title={t("Last sync")}
              description={
                library.sync?.lastError
                  ? library.sync.lastError
                  : library.sync?.lastOk
                    ? formatDateTime(library.sync.lastOk)
                    : t("Not synced yet")
              }
              control={
                <span className="library-present-row">
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={syncing}
                    onClick={() => void syncNow(true)}
                  >
                    {t("Full re-sync")}
                  </Button>
                  <Button size="sm" disabled={syncing} onClick={() => void syncNow(false)}>
                    <RefreshCw size={14} className={syncing ? "animate-spin" : ""} />{" "}
                    {t("Sync now")}
                  </Button>
                </span>
              }
            />
            <SettingsRow
              title={t("Setup")}
              description={
                blockers.length
                  ? t("Some steps still need finishing.")
                  : t("Re-run the guided setup or check a step any time.")
              }
              control={
                <Button size="sm" variant="outline" onClick={() => setWizard(true)}>
                  <Wand2 size={14} /> {t("Open setup")}
                </Button>
              }
            />
          </SettingsSection>
          <LibraryCacheCard library={library} />
        </>
      ) : null}

      {library.source === "filter" ? (
        <SettingsSection title={t("Remove")}>
          <SettingsRow
            title={t("Delete this library")}
            description={t("Its titles stay in Movies and TV Shows.")}
            control={
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => {
                  if (
                    window.confirm(
                      t("Delete the library “{name}”? Its titles stay in Movies and TV Shows.", {
                        name: library.name,
                      }),
                    )
                  )
                    librariesApi
                      .remove(library.id)
                      .then(() => navigate("/libraries"))
                      .catch((e) => toast.error(errorMessage(e)));
                }}
              >
                {t("Delete")}
              </Button>
            }
          />
        </SettingsSection>
      ) : null}

      {wizard && library.pluginId ? (
        <SetupWizard
          pluginId={library.pluginId}
          onClose={() => {
            setWizard(false);
            void setup.reload();
            onSaved();
          }}
        />
      ) : null}
    </div>
  );
}
