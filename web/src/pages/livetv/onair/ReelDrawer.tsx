import { LinkImport } from "./LinkImport";
import { Badge, Button, Input, Popover, Select, Switch } from "@braedonsaunders/appkit-ui";
import { MoreHorizontal, Play, UploadCloud } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Drawer } from "../../../components/Drawer";
import { MoreOptions } from "../../../components/ConfigSection";
import { SettingsRow } from "../../../components/SettingsRow";
import { DragHandle, SortableList } from "../../../components/Sortable";
import { Segmented } from "../../../components/fields";
import { useUnsavedGuard } from "../../../components/SaveBar";
import { Artwork } from "../../../components/Artwork";
import { errorMessage } from "../../../lib/api";
import { t, tn } from "../../../i18n";
import { PreviewPlayer } from "../../playback/PrerollDrawer";
import { tv, type ChannelSummary } from "../model";
import { onair, type Reel, type ReelClip } from "./model";
const blank = (): Reel => ({
  id: "",
  name: "",
  order: "shuffle",
  repeatHours: 1,
  loudnessMatch: true,
  trimSilence: false,
  clips: [],
  channelIds: [],
});
const runtime = (ms: number) =>
  `${Math.floor(ms / 60000)}:${String(Math.round(ms / 1000) % 60).padStart(2, "0")}`;
export function ReelDrawer({
  open,
  reelId = "new",
  onClose,
  onSaved,
}: {
  open: boolean;
  reelId?: string;
  onClose: () => void;
  onSaved?: (reel: Reel) => void;
}) {
  const [draft, setDraft] = useState<Reel>(blank),
    [saved, setSaved] = useState<Reel>(blank);
  const [menu, setMenu] = useState<string | null>(null);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const [preview, setPreview] = useState<string | null>(null),
    [rename, setRename] = useState<string | null>(null);
  const [channels, setChannels] = useState<ChannelSummary[]>([]);
  const upload = useRef<HTMLInputElement>(null),
    replace = useRef<string | null>(null);
  const current = useRef(draft);
  current.current = draft;
  const localPreviews = useRef(new Map<string, string>());
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved);
  useUnsavedGuard(open && dirty);
  useEffect(() => {
    if (!open) return;
    let active = true;
    setError("");
    setPreview(null);
    setRename(null);
    setDraft(blank());
    setSaved(blank());
    if (reelId !== "new")
      void onair
        .reel(reelId)
        .then((r) => {
          if (active) {
            setDraft(r);
            setSaved(r);
          }
        })
        .catch((e) => setError(errorMessage(e)));
    void tv
      .channelSummaries()
      .then(setChannels)
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [open, reelId]);
  useEffect(
    () => () => {
      for (const url of localPreviews.current.values()) URL.revokeObjectURL(url);
    },
    [],
  );
  const converting = draft.clips.some(
    (c) => c.status === "converting" || c.status === "queued" || c.status === "downloading",
  );
  useEffect(() => {
    if (!converting || !draft.id) return;
    let active = true;
    const timer = setInterval(() => {
      void onair
        .reel(draft.id)
        .then((r) => {
          if (!active) return;
          setDraft((d) => ({
            ...d,
            clips: [
              ...d.clips
                .filter((c) => c.status !== "queued" && c.status !== "downloading")
                .map((c) => ({ ...c, ...r.clips.find((v) => v.id === c.id) })),
              ...r.clips.filter(
                (c) =>
                  !d.clips.some(
                    (v) => v.id === c.id && v.status !== "queued" && v.status !== "downloading",
                  ),
              ),
            ],
          }));
        })
        .catch((e) => setError(errorMessage(e)));
    }, 500);
    return () => {
      active = false;
      clearInterval(timer);
    };
  }, [converting, draft.id]);
  async function add(files: File[]) {
    if (!files.length) return;
    setError("");
    setBusy(true);
    try {
      if (files.some((f) => !/\.(mp4|mov|mkv|webm)$/i.test(f.name)))
        throw new Error(t("Choose MP4, MOV, MKV or WebM videos."));
      if (files.some((f) => f.size > 500 * 1024 * 1024))
        throw new Error(t("Each video must be 500 MB or smaller."));
      let reel = current.current;
      if (!reel.id) {
        reel = await onair.saveReel({ ...reel, name: reel.name.trim() || t("New reel") });
        setDraft(reel);
      }
      for (const file of files) {
        const clip = await onair.uploadClip(reel.id, file);
        localPreviews.current.set(clip.id, URL.createObjectURL(file));
        if (replace.current) {
          const id = replace.current;
          await onair.deleteClip(reel.id, id);
          replace.current = null;
          setDraft((d) => ({ ...d, clips: d.clips.map((c) => (c.id === id ? clip : c)) }));
        } else setDraft((d) => ({ ...d, clips: [...d.clips, clip] }));
      }
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
      if (upload.current) upload.current.value = "";
    }
  }
  const clip = draft.clips.find((c) => c.id === preview);
  const changeClip = (clip: ReelClip) =>
    setDraft((d) => ({ ...d, clips: d.clips.map((c) => (c.id === clip.id ? clip : c)) }));
  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={draft.name || t("New reel")}
      size="lg"
      footer={
        <div className="flex w-full flex-wrap items-center gap-2">
          {dirty ? <Badge variant="warning">{t("Unsaved changes")}</Badge> : null}
          <div className="ml-auto flex gap-2">
            <Button variant="outline" onClick={onClose}>
              {t("Cancel")}
            </Button>
            <Button
              disabled={busy || !draft.name.trim()}
              onClick={async () => {
                setBusy(true);
                setError("");
                try {
                  const result = await onair.saveReel(draft);
                  setSaved(result);
                  setDraft(result);
                  onSaved?.(result);
                  if (!onSaved) onClose();
                } catch (e) {
                  setError(errorMessage(e));
                } finally {
                  setBusy(false);
                }
              }}
            >
              {t("Save reel")}
            </Button>
          </div>
        </div>
      }
    >
      <div className="space-y-5">
        <Input
          aria-label={t("Reel name")}
          placeholder={t("Name this reel")}
          value={draft.name}
          onChange={(e) => setDraft({ ...draft, name: e.currentTarget.value })}
        />
        <input
          ref={upload}
          type="file"
          accept=".mp4,.mov,.mkv,.webm"
          multiple
          hidden
          onChange={(e) => void add(Array.from(e.currentTarget.files || []))}
        />
        <div
          onDragOver={(e) => e.preventDefault()}
          onDrop={(e) => {
            e.preventDefault();
            replace.current = null;
            void add(Array.from(e.dataTransfer.files));
          }}
          className="flex flex-col items-center gap-3 rounded-xl border border-dashed border-border-strong bg-bg-subtle p-6 text-center"
        >
          <UploadCloud size={28} className="text-primary" />
          <p className="text-sm font-medium text-fg">{t("Drop videos here, or choose files")}</p>
          <p className="text-xs text-fg-muted">
            {t("MP4, MOV, MKV, WebM · up to 3 min and 500 MB each")}
          </p>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() => {
              replace.current = null;
              upload.current?.click();
            }}
          >
            {busy ? t("Uploading…") : t("Choose files")}
          </Button>
        </div>
        <LinkImport
          reelId={draft.id || undefined}
          ensureReel={async () => {
            if (current.current.id) return current.current.id;
            const reel = await onair.saveReel({
              ...current.current,
              name: current.current.name.trim() || t("New reel"),
            });
            current.current = reel;
            setDraft(reel);
            setSaved(reel);
            return reel.id;
          }}
          onImported={async () => {
            const r = await onair.reel(current.current.id);
            setDraft((d) => ({ ...d, clips: r.clips }));
          }}
        />
        {error ? (
          <p role="alert" className="text-sm text-danger">
            {error}
          </p>
        ) : null}
        {clip ? (
          <PreviewPlayer
            preroll={{
              name: clip.name,
              video: {
                state: "ready",
                durationMs: clip.durationMs,
                width: 1920,
                height: 1080,
                thumbUrl: clip.thumbnail || "",
                previewUrl:
                  localPreviews.current.get(clip.id) ||
                  clip.preview ||
                  onair.clipPreview(draft.id, clip.id),
                problem: "",
                source: "upload",
              },
            }}
          />
        ) : null}
        {!draft.clips.length ? (
          <p className="py-4 text-center text-sm text-fg-muted">{t("No clips yet")}</p>
        ) : (
          <>
            <SortableList
              items={draft.clips}
              getId={(c) => c.id}
              onReorder={(clips) => setDraft({ ...draft, clips })}
              className="divide-y divide-border rounded-xl border border-border bg-surface"
              renderItem={(c, handle) => (
                <div className="flex items-center gap-2 p-3">
                  <DragHandle handle={handle} label={t("Move {name}", { name: c.name })} />
                  <button
                    type="button"
                    disabled={c.status !== "ready"}
                    onClick={() => setPreview(preview === c.id ? null : c.id)}
                    aria-label={t("Preview {name}", { name: c.name })}
                    className="relative h-12 w-20 shrink-0 overflow-hidden rounded-lg bg-bg-subtle"
                  >
                    <Artwork src={c.thumbnail} title={c.name} alt="" className="size-full" />
                    <Play
                      size={16}
                      className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 text-fg"
                    />
                  </button>
                  <div className="min-w-0 flex-1">
                    {rename === c.id ? (
                      <Input
                        aria-label={t("Clip name")}
                        autoFocus
                        value={c.name}
                        onChange={(e) => changeClip({ ...c, name: e.currentTarget.value })}
                        onBlur={() => setRename(null)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") setRename(null);
                        }}
                      />
                    ) : (
                      <p className="truncate text-sm font-medium text-fg">{c.name}</p>
                    )}
                    <p className="text-xs text-fg-muted">
                      {c.status === "queued" || c.status === "downloading"
                        ? t("Importing…")
                        : c.status === "converting"
                          ? t("Converting {progress}%", { progress: c.progress || 0 })
                          : c.status === "error"
                            ? t("Conversion failed")
                            : t("Ready")}
                    </p>
                    {c.source ? (
                      <a
                        className="block truncate text-xs text-primary"
                        href={c.source}
                        target="_blank"
                        rel="noreferrer"
                      >
                        {t("Source")}
                      </a>
                    ) : null}
                    {c.attribution ? (
                      <p className="text-xs text-fg-muted">{c.attribution}</p>
                    ) : null}
                    {c.error ? <p className="text-xs text-danger">{c.error}</p> : null}
                    {c.status === "converting" || c.status === "downloading" ? (
                      <progress
                        className="mt-1 h-1 w-full accent-primary"
                        value={c.progress || 0}
                        max={100}
                        aria-label={t("Conversion progress")}
                      />
                    ) : null}
                  </div>
                  <span className="text-xs tabular-nums text-fg-muted">
                    {runtime(c.durationMs)}
                  </span>
                  <Popover
                    open={menu === c.id}
                    onOpenChange={(v) => setMenu(v ? c.id : null)}
                    trigger={
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={t("Clip actions for {name}", { name: c.name })}
                        onClick={() => setMenu(menu === c.id ? null : c.id)}
                      >
                        <MoreHorizontal size={16} />
                      </Button>
                    }
                  >
                    <div className="grid gap-1 p-1">
                      <Button
                        variant="ghost"
                        onClick={() => {
                          setRename(c.id);
                          setMenu(null);
                        }}
                      >
                        {t("Rename")}
                      </Button>
                      <Button
                        variant="ghost"
                        onClick={() => {
                          replace.current = c.id;
                          setMenu(null);
                          upload.current?.click();
                        }}
                      >
                        {t("Replace file")}
                      </Button>
                      <Button
                        variant="ghost"
                        className="text-danger"
                        onClick={() => {
                          setDraft({ ...draft, clips: draft.clips.filter((v) => v.id !== c.id) });
                          setMenu(null);
                        }}
                      >
                        {t("Remove")}
                      </Button>
                    </div>
                  </Popover>
                </div>
              )}
            />
            <p className="text-right text-xs tabular-nums text-fg-muted">
              {tn(
                draft.clips.length,
                "{count} clip · {duration} total",
                "{count} clips · {duration} total",
                { duration: runtime(draft.clips.reduce((n, c) => n + c.durationMs, 0)) },
              )}
            </p>
          </>
        )}
        <SettingsRow
          title={t("Order")}
          control={
            <Segmented
              ariaLabel={t("Order")}
              value={draft.order}
              onChange={(order) => setDraft({ ...draft, order })}
              options={[
                { value: "shuffle", label: t("Shuffle") },
                { value: "sequence", label: t("In order") },
              ]}
            />
          }
        />
        <SettingsRow
          title={t("Don't repeat a clip within")}
          control={
            <Select
              aria-label={t("Repeat window")}
              value={String(draft.repeatHours)}
              onChange={(e) => setDraft({ ...draft, repeatHours: Number(e.currentTarget.value) })}
            >
              {[1, 2, 6, 12, 24].map((h) => (
                <option key={h} value={h}>
                  {tn(h, "{count} hour", "{count} hours")}
                </option>
              ))}
            </Select>
          }
        />
        <MoreOptions label={t("More options")}>
          <div className="px-4">
            <SettingsRow
              title={t("Loudness match")}
              control={
                <Switch
                  aria-label={t("Loudness match")}
                  checked={draft.loudnessMatch}
                  onChange={(e) => setDraft({ ...draft, loudnessMatch: e.currentTarget.checked })}
                />
              }
            />
            <SettingsRow
              title={t("Trim silence")}
              control={
                <Switch
                  aria-label={t("Trim silence")}
                  checked={draft.trimSilence}
                  onChange={(e) => setDraft({ ...draft, trimSilence: e.currentTarget.checked })}
                />
              }
            />
          </div>
        </MoreOptions>
        <div className="flex flex-wrap items-center gap-2 text-sm text-fg-muted">
          <span>{t("Used on")}</span>
          {draft.channelIds.length ? (
            draft.channelIds.map((id) => (
              <a
                key={id}
                href={`?channel=${encodeURIComponent(id)}`}
                className="text-primary hover:underline"
              >
                {channels.find((c) => c.id === id)?.name || id}
              </a>
            ))
          ) : (
            <span>{t("No channels yet")}</span>
          )}
        </div>
      </div>
    </Drawer>
  );
}
