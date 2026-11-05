import { useEffect, useMemo, useRef, useState } from "react";
import { Badge, Button, Input, Popover, confirmDialog, toast } from "@braedonsaunders/appkit-ui";
import { Drawer } from "../../../components/Drawer";
import { SubtabNav } from "../../../components/layout";
import { ToolbarPicker } from "../../../components/ToolbarPicker";
import { useUnsavedGuard } from "../../../components/SaveBar";
import { t } from "../../../i18n";
import { api } from "../../../lib/api";
import type { EtherUser } from "../../../lib/types";
import { tv, type ChannelSummary } from "../model";
import { onair, type Bindings } from "./model";
import { studio, type Style, type StyleScene } from "./studio-model";
import { LookStage, styleBindings } from "./Stage";
import { LayersPanel } from "./LayersPanel";
import { StyleScreensEditor } from "./StyleScreens";
import { BugControls } from "./BugControls";
import { InsertBar } from "./editor/InsertBar";
import { Inspector } from "./editor/Inspector";
import { SceneStrip } from "./editor/SceneStrip";
import { Timeline } from "./editor/Timeline";
import { VideoPicker } from "./editor/VideoPicker";
import { useStageClock } from "./editor/clock";
import { usePreviewAudio } from "./editor/preview-audio";
import { RemixPanel } from "./editor/RemixPanel";
import { SequencePreview } from "./editor/SequencePreview";
export function LookDrawer({
  look,
  onClose,
  onSaved,
  initialTab = "remix",
  stacked,
}: {
  look: Style;
  onClose(): void;
  onSaved(style?: Style): void;
  initialTab?: string;
  stacked?: boolean;
}) {
  const [draft, setDraft] = useState(look),
    [saved, setSaved] = useState(look),
    [tab, setTab] = useState(initialTab),
    [designTab, setDesignTab] = useState("layers"),
    [sceneId, setSceneId] = useState(look.scenes[0]?.id ?? ""),
    [selected, setSelected] = useState<string[]>([]),
    [channels, setChannels] = useState<ChannelSummary[]>([]),
    [channel, setChannel] = useState(look.channelIds[0] ?? ""),
    [viewers, setViewers] = useState<EtherUser[]>([]),
    [viewer, setViewer] = useState(""),
    [at, setAt] = useState(() =>
      new Date(Date.now() - new Date().getTimezoneOffset() * 60000).toISOString().slice(0, 16),
    ),
    [bound, setBound] = useState<Bindings>({}),
    [pen, setPen] = useState(false),
    [safe, setSafe] = useState(false),
    [zoom, setZoom] = useState(1),
    [saving, setSaving] = useState(false),
    [menu, setMenu] = useState(false),
    [useOpen, setUseOpen] = useState(false),
    [useChannels, setUseChannels] = useState(look.channelIds),
    [sequence, setSequence] = useState(false),
    [muted, setMuted] = useState(true),
    [mix, setMix] = useState<{ muted: string[]; solo: string }>({ muted: [], solo: "" });
  const gestureActive = useRef(false);
  const history = useRef<Style[]>([]),
    future = useRef<Style[]>([]),
    current = useRef(draft);
  current.current = draft;
  const change = (next: Style) => {
    if (!gestureActive.current) history.current.push(current.current);
    future.current = [];
    current.current = next;
    setDraft(next);
  };
  const undo = () => {
    const next = history.current.pop();
    if (next) {
      future.current.push(current.current);
      setDraft(next);
    }
  };
  const redo = () => {
    const next = future.current.pop();
    if (next) {
      history.current.push(current.current);
      setDraft(next);
    }
  };
  const scene = draft.scenes.find((s) => s.id === sceneId) ?? draft.scenes[0];
  const clock = useStageClock(scene?.document.duration ?? 8);
  useEffect(() => {
    clock.seek(scene?.document.poster ?? 0);
  }, [scene?.id]);
  const bindings = useMemo(() => styleBindings(draft, bound), [draft, bound]);
  const audioScene = useMemo(
    () =>
      scene
        ? {
            ...scene,
            sounds: scene.sounds.filter(
              (s) => !mix.muted.includes(s.id) && (!mix.solo || mix.solo === s.id),
            ),
          }
        : undefined,
    [scene, mix],
  );
  usePreviewAudio(audioScene, clock.time, clock.playing, muted);
  const updateScene = (next: StyleScene) =>
    change({ ...draft, scenes: draft.scenes.map((s) => (s.id === next.id ? next : s)) });
  const dirty = !draft.id || JSON.stringify(draft) !== JSON.stringify(saved);
  useUnsavedGuard(dirty);
  useEffect(() => {
    void tv.channelSummaries().then((rows) => {
      setChannels(rows);
      setChannel((id) => (rows.some((c) => c.id === id) ? id : (rows[0]?.id ?? "")));
    });
    void api.users().then((rows) => {
      setViewers(rows);
      setViewer(String(rows.find((u) => u.isOwner)?.id ?? rows[0]?.id ?? ""));
    });
  }, []);
  useEffect(() => {
    if (channel && Number.isFinite(Date.parse(at)))
      void onair
        .bindings({
          channel,
          at: new Date(at).toISOString(),
          viewer,
          moment: scene?.role ?? "ident",
        })
        .then(setBound);
  }, [channel, at, viewer, scene?.role]);
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "z") {
        e.preventDefault();
        if (e.shiftKey) redo();
        else undo();
      }
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, []);
  const close = async () => {
    if (
      !dirty ||
      (await confirmDialog({
        title: t("Discard unsaved changes?"),
        message: t("Your changes to this style haven’t been saved."),
        confirmLabel: t("Discard"),
        tone: "danger",
      }))
    )
      onClose();
  };
  const save = async () => {
    setSaving(true);
    try {
      const result = draft.id ? await studio.saveStyle(draft) : await studio.createStyle(draft);
      setDraft(result);
      setSaved(result);
      onSaved(result);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t("Couldn’t save style"));
    } finally {
      setSaving(false);
    }
  };
  return (
    <Drawer
      open
      stacked={stacked}
      size="2xl"
      onClose={() => void close()}
      title={
        <Input
          aria-label={t("Style name")}
          value={draft.name}
          onChange={(e) => change({ ...draft, name: e.target.value })}
        />
      }
      panelClassName="max-sm:[&>header]:flex-wrap max-sm:[&>header>div:first-child]:w-full max-sm:[&>header>div:last-child]:w-full max-sm:[&>header>div:last-child]:justify-end"
      bodyClassName="flex min-h-0 flex-1 flex-col overflow-y-auto p-0 lg:overflow-hidden"
      headerActions={
        <div className="flex flex-wrap items-center gap-2">
          {dirty && <Badge>{t("Unsaved")}</Badge>}
          <Button size="sm" variant="ghost" disabled={!history.current.length} onClick={undo}>
            {t("Undo")}
          </Button>
          <Button size="sm" variant="ghost" disabled={!future.current.length} onClick={redo}>
            {t("Redo")}
          </Button>
          <Button size="sm" disabled={saving} onClick={() => void save()}>
            {t("Save")}
          </Button>
          <Popover
            open={menu}
            onOpenChange={setMenu}
            trigger={
              <Button
                size="sm"
                variant="ghost"
                aria-label={t("Style actions")}
                onClick={() => setMenu(!menu)}
              >
                ⋯
              </Button>
            }
          >
            <div className="flex flex-col gap-2 p-2">
              <Button variant="ghost" onClick={() => setUseOpen(true)}>
                {t("Use on channels…")}
              </Button>
              <Button
                variant="ghost"
                onClick={() =>
                  void studio
                    .createStyle({ ...draft, id: "", name: t("{name} copy", { name: draft.name }) })
                    .then(onSaved)
                }
              >
                {t("Duplicate")}
              </Button>
              <a href={studio.exportPack(saved.id)} download>
                {t("Export pack")}
              </a>
              <Button
                variant="ghost"
                onClick={async () => {
                  if (
                    await confirmDialog({
                      title: t("Delete {name}?", { name: draft.name }),
                      message: t("Channels using this style will lose their on-air graphics."),
                      confirmLabel: t("Delete"),
                      tone: "danger",
                    })
                  ) {
                    await onair.deleteLook(saved.id);
                    onSaved();
                    onClose();
                  }
                }}
              >
                {t("Delete")}
              </Button>
            </div>
          </Popover>
        </div>
      }
    >
      <div
        inert={saving}
        className="flex flex-col lg:grid lg:min-h-0 lg:flex-1 lg:grid-cols-[320px_minmax(0,1fr)]"
      >
        <aside className="order-2 lg:overflow-y-auto border-r border-border p-4 lg:order-1">
          <SubtabNav
            active={tab}
            onSelect={setTab}
            ariaLabel={t("Style inspector")}
            tabs={[
              { key: "remix", label: t("Remix") },
              { key: "design", label: t("Design") },
              { key: "screens", label: t("Screens") },
            ]}
          />
          <div className="space-y-5 py-4">
            {tab === "design" && scene ? (
              <>
                <SubtabNav
                  active={designTab}
                  onSelect={setDesignTab}
                  ariaLabel={t("Design tools")}
                  tabs={[
                    { key: "properties", label: t("Properties") },
                    { key: "layers", label: t("Layers") },
                  ]}
                />
                {designTab === "layers" ? (
                  <LayersPanel
                    document={scene.document}
                    selected={selected}
                    onSelect={setSelected}
                    onChange={(document) => updateScene({ ...scene, document })}
                  />
                ) : (
                  <Inspector
                    time={clock.time}
                    style={draft}
                    scene={scene}
                    selected={selected}
                    onChange={change}
                  />
                )}
                <VideoPicker scene={scene} onChange={updateScene} />
              </>
            ) : tab === "screens" ? (
              <>
                <StyleScreensEditor look={draft} onChange={change} />
                <BugControls
                  value={draft.bug ?? {}}
                  onChange={(bug) => change({ ...draft, bug })}
                />
              </>
            ) : (
              <RemixPanel
                style={draft}
                onChange={change}
                onOpenScene={(id) => {
                  setSceneId(id);
                  setTab("design");
                }}
              />
            )}
          </div>
        </aside>
        <main className="order-1 min-w-0 space-y-4 lg:overflow-y-auto bg-bg-subtle p-4 lg:order-2">
          <div className="flex flex-wrap items-center gap-2">
            <ToolbarPicker
              ariaLabel={t("Preview channel")}
              value={channel}
              onChange={setChannel}
              options={channels.map((c) => ({ value: c.id, label: c.name }))}
            />
            <Input
              className="w-52"
              type="datetime-local"
              aria-label={t("Preview time")}
              value={at}
              onChange={(e) => setAt(e.target.value)}
            />
            <ToolbarPicker
              ariaLabel={t("Preview viewer")}
              value={viewer}
              onChange={setViewer}
              options={viewers.map((v) => ({ value: String(v.id), label: v.title || v.username }))}
            />
            <Button size="sm" variant="ghost" aria-pressed={safe} onClick={() => setSafe(!safe)}>
              {t("Safe area")}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setZoom(1)}>
              {t("Fit")}
            </Button>
            <Input
              className="w-20"
              type="number"
              min="25"
              max="300"
              aria-label={t("Zoom percent")}
              value={zoom * 100}
              onChange={(e) => setZoom(Math.max(0.25, Math.min(3, Number(e.target.value) / 100)))}
            />
            <Button size="sm" variant="ghost" onClick={() => clock.setPlaying(!clock.playing)}>
              {clock.playing ? t("Pause") : t("Play")}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => setMuted(!muted)}
              aria-pressed={!muted}
            >
              {t("Sound")}
            </Button>
          </div>
          <Button size="sm" variant="ghost" disabled={!channel} onClick={() => setSequence(true)}>
            {t("Play the sequence")}
          </Button>
          {sequence && (
            <Drawer
              open
              stacked
              size="2xl"
              title={t("Play the sequence")}
              onClose={() => setSequence(false)}
            >
              <SequencePreview style={draft} channelId={channel} at={new Date(at).toISOString()} />
            </Drawer>
          )}
          {scene ? (
            <>
              {tab === "design" && (
                <InsertBar
                  onPen={() => {
                    setPen(true);
                    setTab("design");
                  }}
                  scene={scene}
                  onChange={updateScene}
                  onSelect={setSelected}
                />
              )}
              <LookStage
                onGestureStart={() => {
                  if (!gestureActive.current) {
                    history.current.push(current.current);
                    future.current = [];
                    gestureActive.current = true;
                  }
                }}
                onGestureEnd={() => {
                  gestureActive.current = false;
                }}
                sceneVideo={scene.video}
                playing={clock.playing}
                muted={muted}
                pen={pen}
                onPenDone={() => setPen(false)}
                document={scene.document}
                bindings={bindings}
                time={clock.time}
                selected={selected}
                onSelect={(ids) => {
                  setSelected(ids);
                  if (ids.length) {
                    clock.setPlaying(false);
                    setTab("design");
                  }
                }}
                onChange={(document) => updateScene({ ...scene, document })}
                safe={safe}
                zoom={zoom}
              />
              {tab === "design" && (
                <Timeline
                  scene={scene}
                  duration={scene.document.duration}
                  time={clock.time}
                  playing={clock.playing}
                  onPlayingChange={clock.setPlaying}
                  onTime={clock.seek}
                  loop={clock.loop}
                  onLoopChange={clock.setLoop}
                  rate={clock.rate}
                  onRateChange={clock.setRate}
                  selected={selected}
                  onSelect={setSelected}
                  onChange={updateScene}
                  onPreviewMixChange={(muted, solo) => setMix({ muted, solo })}
                />
              )}
            </>
          ) : (
            <p>{t("Add a scene to start designing.")}</p>
          )}
          <SceneStrip
            style={draft}
            selected={scene?.id ?? ""}
            bindings={bindings}
            onSelect={(id) => {
              setSceneId(id);
              setSelected([]);
              clock.seek(0);
            }}
            onChange={change}
          />
        </main>
      </div>
      {useOpen ? (
        <Drawer
          open
          size="md"
          title={t("Use on channels")}
          onClose={() => setUseOpen(false)}
          footer={
            <Button
              onClick={async () => {
                for (const id of new Set([...saved.channelIds, ...useChannels])) {
                  const c = await tv.channel(id);
                  await tv.saveChannel({
                    ...c,
                    look: useChannels.includes(id)
                      ? { ...c.look, lookId: saved.id, ident: true, upNext: true, offAir: true }
                      : c.look?.lookId === saved.id
                        ? undefined
                        : c.look,
                  });
                }
                setUseOpen(false);
                toast.success(t("Style applied"));
              }}
            >
              {t("Apply style")}
            </Button>
          }
        >
          <div className="space-y-3">
            {channels
              .filter((c) => c.kind === "virtual")
              .map((c) => (
                <label
                  key={c.id}
                  className="flex items-center gap-3 rounded-lg border border-border p-3 text-sm"
                >
                  <input
                    type="checkbox"
                    className="accent-primary"
                    checked={useChannels.includes(c.id)}
                    onChange={(e) =>
                      setUseChannels((s) =>
                        e.target.checked ? [...s, c.id] : s.filter((id) => id !== c.id),
                      )
                    }
                  />
                  {c.name}
                </label>
              ))}
          </div>
        </Drawer>
      ) : null}
    </Drawer>
  );
}
