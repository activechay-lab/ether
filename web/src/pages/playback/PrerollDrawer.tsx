import {
  Alert,
  AlertDescription,
  AlertTitle,
  Avatar,
  Badge,
  Button,
  Input,
  MultiSelect,
  Progress,
  Select,
  Spinner,
  Switch,
  confirmDialog,
  cn,
  toast,
} from "@braedonsaunders/appkit-ui";
import { Info, Play, Save, Server, Trash2, UploadCloud, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Drawer } from "../../components/Drawer";
import { ChipGroup, Field, NumberInput, Segmented } from "../../components/fields";
import { Section } from "../../components/layout";
import { FacetPicker, viewerName } from "../../components/pickers";
import { t, tx } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { apiBlob, imageNeedsFetch, isAbort } from "../../lib/api";
import type { CollectionSpec, Viewer } from "../../lib/contract";
import { formatBytes, formatDateTime, relativeTime } from "../../lib/format";
import { ToggleRow } from "../collections/SettingsPanels";
import { Disclosure, ErrorNote, PrerollThumb } from "./PrerollParts";
import {
  mediaUrl,
  prerollInput,
  prerollsApi,
  type Play as PlayRow,
  type Preroll,
  type PrerollInput,
  type PrerollRules,
  type Selection,
} from "./prerolls-api";
import {
  AUDIENCE_OPTIONS,
  DEVICES,
  FREQUENCY_OPTIONS,
  KIND_OPTIONS,
  MONTH_NAMES,
  NETWORK_OPTIONS,
  ORDER_OPTIONS,
  clipLength,
  plainError,
  problemSentence,
  ratingOptions,
  videoState,
  type PlainError,
} from "./prerolls-copy";

const blank = (): PrerollInput => ({
  name: "",
  enabled: true,
  rules: {
    kinds: ["movie"],
    firstEpisodeOnly: false,
    bingeGapMinutes: 30,
    genres: [],
    excludeGenres: [],
    collections: [],
    maxContentRating: "",
    dateFrom: "",
    dateTo: "",
    audience: { mode: "everyone", viewers: [] },
    network: "any",
    devices: [],
  },
  frequency: { mode: "always", hours: 6 },
  priority: 0,
  order: "before_trailers",
});

/** An editable copy; list fields the server may send as null become arrays. */
function draftOf(p?: Preroll): PrerollInput {
  if (!p) return blank();
  const base = blank();
  const r = p.rules ?? base.rules;
  return prerollInput({
    ...p,
    rules: {
      ...base.rules,
      ...r,
      kinds: [...(r.kinds ?? base.rules.kinds)],
      genres: [...(r.genres ?? [])],
      excludeGenres: [...(r.excludeGenres ?? [])],
      collections: [...(r.collections ?? [])],
      devices: [...(r.devices ?? [])],
      audience: {
        mode: r.audience?.mode ?? "everyone",
        viewers: [...(r.audience?.viewers ?? [])],
      },
    },
    frequency: { ...base.frequency, ...p.frequency },
  });
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

type Busy = null | "saving" | "uploading" | "deleting";

export function PrerollDrawer({
  open,
  entry,
  isNew,
  selection,
  viewers,
  collections,
  onClose,
  onSaved,
  onDeleted,
}: {
  open: boolean;
  /** The latest server copy, kept fresh while its video converts. */
  entry?: Preroll;
  isNew: boolean;
  selection: Selection;
  viewers: Viewer[] | null;
  collections: CollectionSpec[] | null;
  onClose: () => void;
  onSaved: (p: Preroll) => void;
  onDeleted: (id: string) => void;
}) {
  const { locale } = useLocale();
  const [baseline, setBaseline] = useState<PrerollInput>(() => draftOf(entry));
  const [draft, setDraft] = useState<PrerollInput>(baseline);
  const [file, setFile] = useState<File | null>(null);
  const [serverPath, setServerPath] = useState("");
  const [progress, setProgress] = useState(0);
  const [busy, setBusy] = useState<Busy>(null);
  const [error, setError] = useState<PlainError | null>(null);
  const [nameError, setNameError] = useState("");
  const upload = useRef<AbortController | null>(null);
  useEffect(() => () => upload.current?.abort(), []);
  useEffect(() => {
    if (!open) upload.current?.abort();
  }, [open]);

  const id = entry?.id;
  const dirty = !same(draft, baseline) || Boolean(file) || Boolean(serverPath.trim());
  const set = (patch: Partial<PrerollInput>) => setDraft((d) => ({ ...d, ...patch }));
  const rules = (patch: Partial<PrerollRules>) =>
    setDraft((d) => ({ ...d, rules: { ...d.rules, ...patch } }));

  async function save() {
    if (!draft.name.trim()) {
      setNameError(t("Give the pre-roll a name."));
      return;
    }
    setNameError("");
    setError(null);
    setBusy("saving");
    let saved = entry;
    try {
      saved = id
        ? await prerollsApi.update(id, prerollInput(draft))
        : await prerollsApi.create(prerollInput(draft));
      onSaved(saved);
      setBaseline(draftOf(saved));
    } catch (e) {
      setBusy(null);
      setError(plainError(e, t("Ether couldn't save this pre-roll.")));
      return;
    }
    try {
      if (file) {
        setBusy("uploading");
        setProgress(0);
        const ctl = new AbortController();
        upload.current = ctl;
        saved = await prerollsApi.upload(saved.id, file, setProgress, ctl.signal);
        onSaved(saved);
        setFile(null);
      } else if (serverPath.trim()) {
        saved = await prerollsApi.path(saved.id, serverPath.trim());
        onSaved(saved);
        setServerPath("");
      }
    } catch (e) {
      setBusy(null);
      if (isAbort(e)) return;
      setError(
        plainError(
          e,
          file
            ? t("The video didn't upload. Your other changes are saved.")
            : t("Ether couldn't use that file. Your other changes are saved."),
        ),
      );
      return;
    }
    setBusy(null);
    toast.success(
      isNew ? t("Added {name}", { name: saved.name }) : t("Saved {name}", { name: saved.name }),
      {
        description:
          saved.video.state === "processing"
            ? t("Ether is converting the video so every app can play it.")
            : undefined,
      },
    );
    onClose();
  }

  async function remove() {
    if (!entry) return;
    const ok = await confirmDialog({
      title: t("Delete {name}?", { name: entry.name }),
      message: t("It stops playing right away, and its video and activity are removed."),
      confirmLabel: t("Delete"),
      cancelLabel: t("Cancel"),
      tone: "danger",
    });
    if (!ok) return;
    setBusy("deleting");
    setError(null);
    try {
      await prerollsApi.remove(entry.id);
      onDeleted(entry.id);
      toast.success(t("Deleted {name}", { name: entry.name }));
      onClose();
    } catch (e) {
      setError(plainError(e, t("Ether couldn't delete this pre-roll.")));
    } finally {
      setBusy(null);
    }
  }

  const title = isNew && !id ? t("Add pre-roll") : entry?.name || t("Pre-roll");
  return (
    <Drawer
      open={open}
      onClose={onClose}
      size="lg"
      title={title}
      description={t("The video, when it plays, and who sees it.")}
      footer={
        <div className="flex w-full flex-wrap items-center gap-2">
          {entry ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="text-danger"
              disabled={busy !== null}
              onClick={() => void remove()}
            >
              <Trash2 size={14} aria-hidden /> {t("Delete")}
            </Button>
          ) : null}
          {dirty && busy === null ? <Badge variant="warning">{t("Unsaved changes")}</Badge> : null}
          <div className="ml-auto flex gap-2">
            <Button type="button" variant="outline" size="sm" onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button
              type="button"
              size="sm"
              disabled={busy !== null || (!dirty && Boolean(id))}
              onClick={() => void save()}
            >
              <Save size={14} aria-hidden />{" "}
              {busy === "uploading"
                ? t("Uploading… {percent}%", { percent: progress })
                : busy === "saving"
                  ? t("Saving…")
                  : isNew && !id
                    ? t("Add pre-roll")
                    : t("Save")}
            </Button>
          </div>
        </div>
      }
    >
      <div className="space-y-5">
        <ErrorNote error={error} />
        <div className="grid gap-4 sm:grid-cols-[1fr_auto] sm:items-start">
          <Field label={t("Name")} htmlFor="preroll-name" error={nameError}>
            <Input
              id="preroll-name"
              value={draft.name}
              autoFocus={isNew}
              maxLength={120}
              aria-invalid={nameError ? true : undefined}
              placeholder={t("Halloween sting")}
              onChange={(e) => {
                set({ name: e.currentTarget.value });
                if (nameError) setNameError("");
              }}
            />
          </Field>
          <label className="flex h-10 cursor-pointer items-center gap-2.5 rounded-md border border-border bg-surface px-3 sm:mt-6">
            <Switch
              checked={draft.enabled}
              aria-label={t("Turned on")}
              onChange={(e) => set({ enabled: e.currentTarget.checked })}
            />
            <span className="text-sm font-medium text-fg">{t("Turned on")}</span>
          </label>
        </div>

        <VideoSection
          entry={entry}
          file={file}
          onFile={setFile}
          serverPath={serverPath}
          onServerPath={setServerPath}
          uploading={busy === "uploading"}
          progress={progress}
        />

        <PlaysBeforeSection draft={draft} rules={rules} collections={collections} />

        <Section title={t("Who")} description={t("Which viewers see it, where, and on what.")}>
          <Segmented
            ariaLabel={t("Who sees it")}
            value={draft.rules.audience.mode}
            onChange={(mode) => rules({ audience: { ...draft.rules.audience, mode } })}
            options={AUDIENCE_OPTIONS}
          />
          {draft.rules.audience.mode !== "everyone" ? (
            <div className="space-y-1.5">
              <MultiSelect
                value={draft.rules.audience.viewers.map(String)}
                onChange={(v) =>
                  rules({ audience: { ...draft.rules.audience, viewers: v.map(Number) } })
                }
                ariaLabel={t("Viewers")}
                placeholder={viewers ? t("Choose viewers…") : t("Loading viewers…")}
                searchPlaceholder={t("Search…")}
                noMatchesLabel={t("No matches")}
                clearLabel={t("Clear")}
                selectAllLabel={t("Select all")}
                clearable
                options={(viewers ?? []).map((v) => ({
                  value: String(v.id),
                  label: viewerName(v),
                  description: v.isOwner ? t("Owner") : v.username,
                }))}
              />
              {draft.rules.audience.viewers.length === 0 ? (
                <p className="text-xs text-warning">
                  {draft.rules.audience.mode === "only"
                    ? t("No viewers chosen yet, so nobody sees it.")
                    : t("No viewers chosen yet, so everyone sees it.")}
                </p>
              ) : null}
            </div>
          ) : null}
          <Field label={t("Home or away")}>
            <Segmented
              ariaLabel={t("Home or away")}
              value={draft.rules.network}
              onChange={(network) => rules({ network })}
              options={NETWORK_OPTIONS}
            />
          </Field>
          <Field label={t("Devices")} hint={t("Leave all off to play on every device.")}>
            <ChipGroup
              ariaLabel={t("Devices")}
              size="sm"
              value={draft.rules.devices}
              onChange={(devices) => rules({ devices })}
              options={DEVICES}
            />
          </Field>
        </Section>

        <Section title={t("How often")}>
          <Segmented
            ariaLabel={t("How often")}
            value={draft.frequency.mode}
            onChange={(mode) => set({ frequency: { ...draft.frequency, mode } })}
            options={FREQUENCY_OPTIONS.map((o) => ({ value: o.value, label: o.label }))}
          />
          <p className="text-xs text-fg-muted">
            {FREQUENCY_OPTIONS.find((o) => o.value === draft.frequency.mode)?.hint}
          </p>
          {draft.frequency.mode === "hours" ? (
            <Field label={t("Hours between plays")} htmlFor="preroll-hours">
              <NumberInput
                id="preroll-hours"
                className="w-28"
                min={1}
                max={720}
                value={draft.frequency.hours}
                onChange={(n) =>
                  set({
                    frequency: { ...draft.frequency, hours: Math.max(1, Math.round(n)) },
                  })
                }
              />
            </Field>
          ) : null}
        </Section>

        <Section title={t("Order")}>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field
              label={t("Priority")}
              htmlFor="preroll-priority"
              hint={
                selection === "priority"
                  ? t("When several match, the highest number plays.")
                  : t("Shuffle is on, so priority only matters if you switch back.")
              }
            >
              <NumberInput
                id="preroll-priority"
                className="w-28"
                min={0}
                max={1000}
                value={draft.priority}
                onChange={(n) => set({ priority: Math.max(0, Math.round(n)) })}
              />
            </Field>
            <Field
              label={t("Cinema trailers")}
              hint={t("Whether it plays before or after any cinema trailers.")}
            >
              <Segmented
                ariaLabel={t("Cinema trailers")}
                value={draft.order}
                onChange={(order) => set({ order })}
                options={ORDER_OPTIONS}
              />
            </Field>
          </div>
        </Section>

        <ActivitySection id={id} locale={locale} />

        <Alert variant="info" role="note">
          <AlertTitle className="flex items-center gap-2">
            <Info size={15} aria-hidden /> {t("Where it shows")}
          </AlertTitle>
          <AlertDescription>
            {t(
              "Plex apps, Jellyfin web and Android TV, and Emby apps show pre-rolls. Infuse, Swiftfin, Silo and Stremio don't.",
            )}
          </AlertDescription>
        </Alert>
      </div>
    </Drawer>
  );
}

function VideoSection({
  entry,
  file,
  onFile,
  serverPath,
  onServerPath,
  uploading,
  progress,
}: {
  entry?: Preroll;
  file: File | null;
  onFile: (f: File | null) => void;
  serverPath: string;
  onServerPath: (v: string) => void;
  uploading: boolean;
  progress: number;
}) {
  const state = entry ? videoState(entry) : "none";
  const [replacing, setReplacing] = useState(false);
  const [usePath, setUsePath] = useState(false);
  const showDrop = !file && !serverPath.trim() && (state === "none" || replacing);
  return (
    <Section
      title={t("Video")}
      description={t("Up to 3 minutes. Ether converts it so every app can play it.")}
      actions={
        state !== "none" && !showDrop && !file ? (
          <Button type="button" variant="outline" size="sm" onClick={() => setReplacing(true)}>
            <UploadCloud size={14} aria-hidden /> {t("Replace video")}
          </Button>
        ) : null
      }
    >
      {file ? (
        <div className="space-y-2 rounded-lg border border-border bg-bg-subtle p-3">
          <div className="flex items-center gap-3">
            <UploadCloud size={18} className="shrink-0 text-primary" aria-hidden />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium text-fg">{file.name}</p>
              <p className="text-xs text-fg-muted">
                {uploading
                  ? t("Uploading… {percent}%", { percent: progress })
                  : t("{size} · uploads when you save", { size: formatBytes(file.size) })}
              </p>
            </div>
            {!uploading ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                aria-label={t("Remove {name}", { name: file.name })}
                onClick={() => onFile(null)}
              >
                <X size={14} />
              </Button>
            ) : null}
          </div>
          {uploading ? <Progress value={progress} aria-label={t("Upload progress")} /> : null}
        </div>
      ) : null}

      {!file && entry && state === "ready" && !showDrop ? (
        <div className="max-w-md space-y-2">
          <PreviewPlayer preroll={entry} />
          <p className="text-sm text-fg-muted">
            {t("Length: {length}", { length: clipLength(entry.video.durationMs) })}
            {entry.video.width ? (
              <span className="text-fg-subtle">
                {" · "}
                {entry.video.width}×{entry.video.height}
              </span>
            ) : null}
          </p>
        </div>
      ) : null}

      {!file && entry && state === "processing" && !showDrop ? (
        <div className="flex items-center gap-3 rounded-lg border border-border bg-bg-subtle p-3">
          <PrerollThumb preroll={entry} className="w-32" />
          <div>
            <p className="text-sm font-medium text-fg">{t("Converting video…")}</p>
            <p className="text-xs text-fg-muted">
              {t("Ether is making a copy every app can play. This usually takes under a minute.")}
            </p>
          </div>
        </div>
      ) : null}

      {!file && entry && state === "failed" && !showDrop ? (
        <Alert variant="destructive">
          <AlertTitle>{t("This video can't be used")}</AlertTitle>
          <AlertDescription>{problemSentence(entry.video.problem)}</AlertDescription>
        </Alert>
      ) : null}

      {showDrop ? (
        <Dropzone
          onFile={(f) => {
            onFile(f);
            setReplacing(false);
          }}
          onCancel={state !== "none" ? () => setReplacing(false) : undefined}
        />
      ) : null}

      {!file ? (
        usePath || serverPath ? (
          <Field
            label={t("Use a file on the server")}
            htmlFor="preroll-path"
            hint={t("The full path to a video Ether can read. Ether uses it when you save.")}
          >
            <div className="flex gap-2">
              <div className="relative flex-1">
                <Server
                  size={14}
                  aria-hidden
                  className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-fg-subtle"
                />
                <Input
                  id="preroll-path"
                  className="pl-8 font-mono text-xs"
                  spellCheck={false}
                  value={serverPath}
                  placeholder="/media/prerolls/intro.mp4"
                  onChange={(e) => onServerPath(e.currentTarget.value)}
                />
              </div>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => {
                  onServerPath("");
                  setUsePath(false);
                }}
              >
                {t("Cancel")}
              </Button>
            </div>
          </Field>
        ) : (
          <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto px-0"
            onClick={() => setUsePath(true)}
          >
            <Server size={14} aria-hidden /> {t("Use a file on the server instead")}
          </Button>
        )
      ) : null}
    </Section>
  );
}

function Dropzone({ onFile, onCancel }: { onFile: (f: File) => void; onCancel?: () => void }) {
  const input = useRef<HTMLInputElement>(null);
  const [over, setOver] = useState(false);
  return (
    <div className="space-y-2">
      <div
        role="button"
        tabIndex={0}
        aria-label={t("Choose a video file")}
        onClick={() => input.current?.click()}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            input.current?.click();
          }
        }}
        onDragOver={(e) => {
          e.preventDefault();
          setOver(true);
        }}
        onDragLeave={() => setOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setOver(false);
          const f = e.dataTransfer.files[0];
          if (f) onFile(f);
        }}
        className={cn(
          "flex cursor-pointer flex-col items-center justify-center gap-2 rounded-xl border-2 border-dashed px-6 py-8 text-center transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
          over
            ? "border-primary bg-primary-subtle"
            : "border-border bg-bg-subtle hover:border-border-strong",
        )}
      >
        <span className="flex size-11 items-center justify-center rounded-full bg-surface text-primary shadow-sm ring-1 ring-border">
          <UploadCloud size={20} aria-hidden />
        </span>
        <span className="text-sm font-medium text-fg">
          {tx("Drop a video here, or {choose}", {
            choose: <span className="text-primary underline">{t("choose a file")}</span>,
          })}
        </span>
        <span className="text-xs text-fg-muted">
          {t("MP4, MKV, MOV and other common formats.")}
        </span>
        <input
          ref={input}
          type="file"
          accept="video/*,.mkv,.mov,.mp4,.m4v,.avi,.webm,.ts"
          className="sr-only"
          tabIndex={-1}
          aria-hidden
          onChange={(e) => {
            const f = e.currentTarget.files?.[0];
            if (f) onFile(f);
            e.currentTarget.value = "";
          }}
        />
      </div>
      {onCancel ? (
        <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
          {t("Keep the current video")}
        </Button>
      ) : null}
    </div>
  );
}

/**
 * The converted clip. Admin auth travels in a header, which a <video> can't
 * send, so the preview is fetched with it on demand (clips are at most three
 * minutes) and played from a blob that is released on close.
 */
function PreviewPlayer({ preroll }: { preroll: Preroll }) {
  const url = mediaUrl(preroll.video.previewUrl);
  const [src, setSrc] = useState<string | null>(null);
  const [phase, setPhase] = useState<"idle" | "loading" | "error">("idle");
  useEffect(() => {
    setSrc(null);
    setPhase("idle");
  }, [url]);
  useEffect(
    () => () => {
      if (src?.startsWith("blob:")) URL.revokeObjectURL(src);
    },
    [src],
  );
  async function play() {
    if (!imageNeedsFetch(url)) {
      setSrc(url);
      return;
    }
    setPhase("loading");
    try {
      setSrc(URL.createObjectURL(await apiBlob(url)));
      setPhase("idle");
    } catch {
      setPhase("error");
    }
  }
  if (src)
    return (
      <video
        src={src}
        controls
        autoPlay
        playsInline
        aria-label={t("Preview of {name}", { name: preroll.name })}
        className="aspect-video w-full rounded-lg bg-black"
        onError={() => {
          setSrc(null);
          setPhase("error");
        }}
      />
    );
  return (
    <div className="relative">
      <PrerollThumb preroll={preroll} className="w-full rounded-lg" eager />
      <button
        type="button"
        onClick={() => void play()}
        disabled={phase === "loading"}
        aria-label={t("Play preview")}
        className="absolute inset-0 grid place-items-center rounded-lg bg-black/25 transition-colors hover:bg-black/35 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
      >
        <span className="flex size-14 items-center justify-center rounded-full bg-white/90 text-black shadow-lg">
          {phase === "loading" ? (
            <Spinner size={20} label={t("Loading preview")} />
          ) : (
            <Play size={24} className="ml-1" aria-hidden />
          )}
        </span>
      </button>
      {phase === "error" ? (
        <p role="alert" className="mt-2 text-xs text-danger">
          {t("The preview couldn't load. The pre-roll still plays in apps.")}
        </p>
      ) : null}
    </div>
  );
}

function PlaysBeforeSection({
  draft,
  rules,
  collections,
}: {
  draft: PrerollInput;
  rules: (patch: Partial<PrerollRules>) => void;
  collections: CollectionSpec[] | null;
}) {
  const r = draft.rules;
  const kind = r.kinds.length === 2 ? "both" : r.kinds[0] === "episode" ? "episode" : "movie";
  const allYear = !r.dateFrom;
  return (
    <Section title={t("Plays before")} description={t("Leave a rule empty to match everything.")}>
      <Segmented
        ariaLabel={t("Plays before")}
        value={kind}
        onChange={(v) => rules({ kinds: v === "both" ? ["movie", "episode"] : [v] })}
        options={KIND_OPTIONS}
      />
      {kind !== "movie" ? (
        <div className="space-y-2">
          <ToggleRow
            title={t("Only the first episode of a session")}
            description={t("Skips it when the next episode plays straight after.")}
            checked={r.firstEpisodeOnly}
            onChange={(firstEpisodeOnly) => rules({ firstEpisodeOnly })}
          />
          {r.firstEpisodeOnly ? (
            <Disclosure label={t("Advanced")}>
              <Field
                label={t("New session after")}
                htmlFor="preroll-gap"
                hint={t(
                  "Minutes without watching before the next episode counts as a new session.",
                )}
              >
                <div className="flex items-center gap-2">
                  <NumberInput
                    id="preroll-gap"
                    className="w-24"
                    min={1}
                    max={1440}
                    value={r.bingeGapMinutes}
                    onChange={(n) => rules({ bingeGapMinutes: Math.max(1, Math.round(n)) })}
                  />
                  <span className="text-sm text-fg-muted">{t("minutes")}</span>
                </div>
              </Field>
            </Disclosure>
          ) : null}
        </div>
      ) : null}
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label={t("Only these genres")}>
          <FacetPicker
            type="mixed"
            facet="genre"
            value={r.genres}
            onChange={(genres) => rules({ genres })}
            ariaLabel={t("Only these genres")}
            placeholder={t("Any genre")}
          />
        </Field>
        <Field label={t("Never these genres")}>
          <FacetPicker
            type="mixed"
            facet="genre"
            value={r.excludeGenres}
            onChange={(excludeGenres) => rules({ excludeGenres })}
            ariaLabel={t("Never these genres")}
            placeholder={t("None")}
          />
        </Field>
        <Field label={t("In these collections")}>
          <MultiSelect
            value={r.collections}
            onChange={(v) => rules({ collections: v })}
            ariaLabel={t("In these collections")}
            placeholder={collections ? t("Any collection") : t("Loading…")}
            searchPlaceholder={t("Search…")}
            noMatchesLabel={t("No matches")}
            clearLabel={t("Clear")}
            selectAllLabel={t("Select all")}
            clearable
            maxVisibleValues={3}
            options={[
              ...(collections ?? [])
                .filter((c) => c.id)
                .map((c) => ({ value: c.id!, label: c.name })),
              // Keep a chosen collection that was since deleted removable.
              ...r.collections
                .filter((id) => collections && !collections.some((c) => c.id === id))
                .map((id) => ({ value: id, label: t("Deleted collection") })),
            ]}
          />
        </Field>
        <Field label={t("Content rating")}>
          <Select
            aria-label={t("Content rating")}
            value={r.maxContentRating}
            onChange={(e) => rules({ maxContentRating: e.currentTarget.value })}
          >
            {ratingOptions().map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </Select>
        </Field>
      </div>
      <Field
        label={t("Dates")}
        hint={allYear ? undefined : t("Repeats every year. The end date can be in the next year.")}
      >
        <div className="space-y-3">
          <Segmented
            ariaLabel={t("Dates")}
            value={allYear ? "all" : "window"}
            onChange={(v) =>
              rules(
                v === "all" ? { dateFrom: "", dateTo: "" } : { dateFrom: "10-01", dateTo: "10-31" },
              )
            }
            options={[
              { value: "all", label: t("All year") },
              { value: "window", label: t("Between dates") },
            ]}
          />
          {!allYear ? (
            <div className="flex flex-wrap items-center gap-2 text-sm text-fg-muted">
              {tx("From {start} to {end}", {
                start: (
                  <MonthDay
                    label={t("Start date")}
                    value={r.dateFrom}
                    onChange={(dateFrom) => rules({ dateFrom })}
                  />
                ),
                end: (
                  <MonthDay
                    label={t("End date")}
                    value={r.dateTo || r.dateFrom}
                    onChange={(dateTo) => rules({ dateTo })}
                  />
                ),
              })}
            </div>
          ) : null}
        </div>
      </Field>
    </Section>
  );
}

const DAYS_IN_MONTH = [31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];

/** A month and day that repeat every year ("MM-DD"). */
function MonthDay({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
}) {
  const [m, d] = (value || "01-01").split("-").map(Number) as [number, number];
  const emit = (month: number, day: number) =>
    onChange(
      `${String(month).padStart(2, "0")}-${String(Math.min(day, DAYS_IN_MONTH[month - 1]!)).padStart(2, "0")}`,
    );
  return (
    <span role="group" aria-label={label} className="inline-flex gap-1.5">
      <Select
        aria-label={t("{date}: month", { date: label })}
        className="w-36"
        value={String(m)}
        onChange={(e) => emit(Number(e.currentTarget.value), d)}
      >
        {MONTH_NAMES.map((name, i) => (
          <option key={name} value={String(i + 1)}>
            {name}
          </option>
        ))}
      </Select>
      <Select
        aria-label={t("{date}: day", { date: label })}
        className="w-20"
        value={String(d)}
        onChange={(e) => emit(m, Number(e.currentTarget.value))}
      >
        {Array.from({ length: DAYS_IN_MONTH[m - 1] ?? 31 }, (_, i) => (
          <option key={i + 1} value={String(i + 1)}>
            {i + 1}
          </option>
        ))}
      </Select>
    </span>
  );
}

function ActivitySection({ id, locale }: { id?: string; locale: string }) {
  const [plays, setPlays] = useState<PlayRow[] | null>(null);
  const [error, setError] = useState<PlainError | null>(null);
  useEffect(() => {
    if (!id) return;
    const ctl = new AbortController();
    prerollsApi
      .plays(id, ctl.signal)
      .then((r) => setPlays(r.plays ?? []))
      .catch((e) => {
        if (!isAbort(e)) setError(plainError(e, t("Ether couldn't load recent plays.")));
      });
    return () => ctl.abort();
  }, [id]);
  const body: ReactNode = useMemo(() => {
    if (!id)
      return (
        <p className="text-sm text-fg-muted">
          {t("Plays show up here once viewers start seeing it.")}
        </p>
      );
    if (error) return <ErrorNote error={error} />;
    if (!plays)
      return (
        <p className="flex items-center gap-2 text-sm text-fg-muted">
          <Spinner size={14} label={t("Loading…")} /> {t("Loading…")}
        </p>
      );
    if (!plays.length)
      return <p className="text-sm text-fg-muted">{t("Nobody has seen it yet.")}</p>;
    return (
      <ul className="divide-y divide-border-subtle">
        {plays.map((p, i) => (
          <li key={`${p.at}-${i}`} className="flex items-center gap-3 py-2.5 first:pt-0 last:pb-0">
            <Avatar name={p.viewerName} size={28} />
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm text-fg">
                {tx("{viewer} before {title}", {
                  viewer: <span className="font-medium">{p.viewerName}</span>,
                  title: <span className="font-medium">{p.title}</span>,
                })}
              </p>
              <p className="truncate text-xs text-fg-muted">{p.client}</p>
            </div>
            <span className="shrink-0 text-xs text-fg-subtle" title={formatDateTime(p.at)}>
              {relativeTime(p.at)}
            </span>
          </li>
        ))}
      </ul>
    );
  }, [id, plays, error, locale]);
  return (
    <Section title={t("Activity")} description={t("The latest plays, newest first.")}>
      {body}
    </Section>
  );
}
