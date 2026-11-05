import { useState } from "react";
import { Button, Input, Select } from "@braedonsaunders/appkit-ui";
import { t } from "../../../../i18n";
import { studio, type StyleScene, type LibraryAsset } from "../studio-model";
import type { Layer, Value } from "../scene-renderer";
import {
  isAnimated,
  moveKeyframe,
  removeKeyframe,
  setEase,
  setKeyframe,
  valueAt,
  type Ease,
} from "./keyframes";
import { applyAnimation, presets, type AnimationPreset } from "./animation-presets";
import { SoundLane } from "./SoundLane";

import { onair } from "../model";
type Timed = Layer & { name?: string; start?: number; end?: number };
export function Timeline(p: {
  scene: StyleScene;
  duration: number;
  time: number;
  playing: boolean;
  onPlayingChange?(p: boolean): void;
  loop?: boolean;
  onLoopChange?(v: boolean): void;
  rate?: number;
  onRateChange?(v: number): void;
  onPreviewMixChange?(muted: string[], solo: string): void;
  onTime(t: number): void;
  selected: string[];
  onSelect(ids: string[]): void;
  onChange(scene: StyleScene): void;
}) {
  const [loop, setLoop] = useState(true),
    [half, setHalf] = useState(false),
    [zoom, setZoom] = useState(1),
    [expanded, setExpanded] = useState<string[]>([]),
    [prop, setProp] = useState<keyof Layer>("opacity"),
    [assets, setAssets] = useState<LibraryAsset[]>([]),
    [picker, setPicker] = useState(false),
    [muted, setMuted] = useState<string[]>([]),
    [solo, setSolo] = useState(""),
    [error, setError] = useState("");
  const edit = (id: string, fn: (l: Timed) => Layer) => {
    const visit = (layers: Layer[]): Layer[] =>
      layers.map((l) =>
        l.id === id ? fn(l) : { ...l, layers: l.layers ? visit(l.layers) : undefined },
      );
    p.onChange({
      ...p.scene,
      document: { ...p.scene.document, layers: visit(p.scene.document.layers) },
    });
  };
  const snap = (v: number) => {
    const points = [p.time, ...Array.from({ length: Math.ceil(p.duration) + 1 }, (_, i) => i)];
    const visit = (layers: Layer[]) =>
      layers.forEach((l) => {
        Object.values(l).forEach((v) => {
          if (isAnimated(v)) points.push(...v.keys.map((k) => k[0]));
        });
        if (l.layers) visit(l.layers);
      });
    visit(p.scene.document.layers);
    const nearest = points.sort((a, b) => Math.abs(a - v) - Math.abs(b - v))[0] ?? v;
    return Math.max(0, Math.min(p.duration, Math.abs(nearest - v) < 0.12 / zoom ? nearest : v));
  };
  const drag = (e: React.PointerEvent, initial: number, change: (v: number) => void) => {
    e.stopPropagation();
    const x = e.clientX,
      width = e.currentTarget.parentElement!.getBoundingClientRect().width;
    const node = e.currentTarget;
    node.setPointerCapture(e.pointerId);
    const move = (event: PointerEvent) =>
      change(snap(initial + ((event.clientX - x) / width) * p.duration));
    const end = () => {
      node.removeEventListener("pointermove", move as EventListener);
      node.removeEventListener("pointerup", end);
    };
    node.addEventListener("pointermove", move as EventListener);
    node.addEventListener("pointerup", end, { once: true });
  };
  const rows = (layers: Layer[], depth = 0): React.ReactNode =>
    layers.map((layer) => {
      const l = layer as Timed,
        start = l.start || 0,
        end = l.end || p.duration;
      return (
        <div key={l.id}>
          <div className="flex items-center gap-2 py-1">
            <Button
              aria-expanded={expanded.includes(l.id)}
              onClick={() =>
                setExpanded(
                  expanded.includes(l.id)
                    ? expanded.filter((id) => id !== l.id)
                    : [...expanded, l.id],
                )
              }
            >
              ↕
            </Button>
            <button
              className="w-36 shrink-0 truncate text-left"
              style={{ paddingLeft: depth * 12 }}
              onClick={() => p.onSelect([l.id])}
              aria-pressed={p.selected.includes(l.id)}
            >
              {l.name || l.id}
            </button>
            <div className="relative h-8 grow bg-current/5">
              <div
                className="absolute h-8 rounded bg-blue-500/40 flex justify-between"
                style={{
                  left: `${(start / p.duration) * 100}%`,
                  width: `${((end - start) / p.duration) * 100}%`,
                }}
                onPointerDown={(e) =>
                  drag(e, start, (v) =>
                    edit(l.id, (old) => ({
                      ...old,
                      start: Math.min(v, p.duration - (end - start)),
                      end: Math.min(p.duration, v + end - start),
                    })),
                  )
                }
              >
                <button
                  aria-label={t("Trim layer start")}
                  onPointerDown={(e) =>
                    drag(e, start, (v) =>
                      edit(l.id, (old) => ({ ...old, start: Math.min(v, end - 0.01) })),
                    )
                  }
                >
                  ┃
                </button>
                <button
                  aria-label={t("Trim layer end")}
                  onPointerDown={(e) =>
                    drag(e, end, (v) =>
                      edit(l.id, (old) => ({ ...old, end: Math.max(start + 0.01, v) })),
                    )
                  }
                >
                  ┃
                </button>
              </div>
            </div>
          </div>
          {expanded.includes(l.id) && (
            <div className="pl-12">
              {Object.entries(l)
                .filter(([, v]) => isAnimated(v))
                .map(([key, v]) => (
                  <div key={key} className="flex h-9 items-center">
                    <span className="w-32">{key}</span>
                    <div className="relative grow h-8">
                      {isAnimated(v) &&
                        v.keys.map((k) => (
                          <button
                            key={k[0]}
                            className="absolute text-blue-500"
                            style={{ left: `${(k[0] / p.duration) * 100}%` }}
                            aria-label={t("Keyframe")}
                            onClick={() => p.onTime(k[0])}
                            onPointerDown={(e) =>
                              drag(e, k[0], (time) =>
                                edit(l.id, (old) =>
                                  moveKeyframe(old, key as keyof Layer, k[0], time),
                                ),
                              )
                            }
                            onKeyDown={(e) => {
                              if (e.key === "Delete")
                                edit(l.id, (old) => removeKeyframe(old, key as keyof Layer, k[0]));
                              if (e.key === "ArrowLeft" || e.key === "ArrowRight")
                                edit(l.id, (old) =>
                                  moveKeyframe(
                                    old,
                                    key as keyof Layer,
                                    k[0],
                                    snap(k[0] + (e.key === "ArrowLeft" ? -0.1 : 0.1)),
                                  ),
                                );
                            }}
                            onContextMenu={(e) => {
                              e.preventDefault();
                              p.onTime(k[0]);
                              p.onSelect([l.id]);
                            }}
                          >
                            ◆
                          </button>
                        ))}
                    </div>
                    <Select
                      aria-label={t("Keyframe easing")}
                      onChange={(e) =>
                        edit(l.id, (old) =>
                          setEase(old, key as keyof Layer, p.time, e.target.value as Ease),
                        )
                      }
                    >
                      {["linear", "in", "out", "inOut", "back", "elastic", "spring"].map((e) => (
                        <option key={e}>{e}</option>
                      ))}
                    </Select>
                  </div>
                ))}
              {l.behave?.map((b, i) =>
                Object.entries(b).map(([kind, params]) => (
                  <div key={i} className="flex flex-wrap gap-2 rounded border p-2">
                    <span>{kind}</span>
                    {(["start", "amplitude", "speed"] as const).map((field) => (
                      <label key={field}>
                        {field}
                        <Input
                          type="number"
                          value={params[field] ?? 0}
                          onChange={(e) =>
                            edit(l.id, (old) => ({
                              ...old,
                              behave: old.behave?.map((v, j) =>
                                i === j
                                  ? { [kind]: { ...params, [field]: Number(e.target.value) } }
                                  : v,
                              ),
                            }))
                          }
                        />
                      </label>
                    ))}
                  </div>
                )),
              )}
            </div>
          )}
          {l.layers && rows(l.layers, depth + 1)}
        </div>
      );
    });
  const addSound = (a: LibraryAsset) => {
    if (p.scene.sounds.length >= 4) return;
    p.onChange({
      ...p.scene,
      sounds: [
        ...p.scene.sounds,
        {
          id: crypto.randomUUID(),
          assetDigest: a.digest,
          at: p.time,
          start: 0,
          end: a.durationMs ? a.durationMs / 1000 : undefined,
          volume: 1,
          fadeIn: 0,
          fadeOut: 0,
        },
      ],
    });
    setPicker(false);
  };
  return (
    <section className="space-y-3 rounded-xl border p-3" aria-label={t("Timeline")}>
      <div className="flex flex-wrap gap-2">
        <Button onClick={() => p.onPlayingChange?.(!p.playing)}>
          {p.playing ? t("Pause") : t("Play")}
        </Button>
        <Button
          aria-pressed={loop}
          onClick={() => {
            setLoop(!loop);
            p.onLoopChange?.(!loop);
          }}
        >
          {t("Loop")}
        </Button>
        <Button
          aria-pressed={half}
          onClick={() => {
            setHalf(!half);
            p.onRateChange?.(half ? 1 : 0.5);
          }}
        >
          {t("Half speed")}
        </Button>
        <label>
          {t("Zoom")}
          <Input
            type="range"
            min={1}
            max={4}
            step={0.25}
            value={zoom}
            onChange={(e) => setZoom(Number(e.target.value))}
          />
        </label>
        <output>{p.time.toFixed(2)}</output>
      </div>
      <div className="flex flex-wrap gap-2">
        <Select
          aria-label={t("Animation preset")}
          value=""
          disabled={!p.selected.length}
          onChange={(e) => {
            const preset = e.target.value as AnimationPreset;
            const visit = (layers: Layer[]): Layer[] =>
              layers.map((l) => ({
                ...(p.selected.includes(l.id) ? applyAnimation(l, preset, p.duration) : l),
                layers: l.layers ? visit(l.layers) : undefined,
              }));
            p.onChange({
              ...p.scene,
              document: { ...p.scene.document, layers: visit(p.scene.document.layers) },
            });
          }}
        >
          <option value="">{t("Add animation")}</option>
          {presets.map((id) => (
            <option key={id} value={id}>
              {id}
            </option>
          ))}
        </Select>
        <Select
          aria-label={t("Animated property")}
          value={prop}
          onChange={(e) => setProp(e.target.value as keyof Layer)}
        >
          {[
            "x",
            "y",
            "w",
            "h",
            "scale",
            "scaleX",
            "scaleY",
            "rotate",
            "opacity",
            "fill",
            "stroke",
            "strokeWidth",
            "text",
            "font",
            "size",
            "track",
            "src",
            "tint",
          ].map((v) => (
            <option key={v}>{v}</option>
          ))}
        </Select>
        <Button
          disabled={!p.selected.length}
          onClick={() =>
            p.selected.forEach((id) =>
              edit(id, (l) =>
                setKeyframe(
                  l,
                  prop,
                  p.time,
                  valueAt(l[prop] as Value, p.time) ??
                    (prop === "opacity" || prop === "scale" ? 1 : 0),
                ),
              ),
            )
          }
        >
          {t("Add keyframe")}
        </Button>
      </div>
      {!p.selected.length && <p>{t("Pick a layer, then add an animation")}</p>}
      <div className="overflow-x-auto">
        <div style={{ minWidth: 600 * zoom }}>
          <Input
            aria-label={t("Playhead")}
            type="range"
            min={0}
            max={p.duration}
            step={0.01}
            value={p.time}
            onChange={(e) => p.onTime(Number(e.target.value))}
          />
          <div className="flex justify-between text-xs">
            {Array.from({ length: Math.floor(p.duration) + 1 }, (_, i) => (
              <span key={i}>{i}</span>
            ))}
          </div>
          {rows(p.scene.document.layers)}
        </div>
      </div>
      {p.scene.sounds.map((sound) => (
        <SoundLane
          key={sound.id}
          sound={sound}
          duration={p.duration}
          muted={muted.includes(sound.id)}
          onMute={() => {
            const next = muted.includes(sound.id)
              ? muted.filter((id) => id !== sound.id)
              : [...muted, sound.id];
            setMuted(next);
            p.onPreviewMixChange?.(next, solo);
          }}
          onSolo={() => {
            const next = solo === sound.id ? "" : sound.id;
            setSolo(next);
            p.onPreviewMixChange?.(muted, next);
          }}
          onChange={(s) =>
            p.onChange({
              ...p.scene,
              sounds: p.scene.sounds.map((old) => (old.id === s.id ? s : old)),
            })
          }
          onRemove={() =>
            p.onChange({ ...p.scene, sounds: p.scene.sounds.filter((s) => s.id !== sound.id) })
          }
        />
      ))}
      <Button
        disabled={p.scene.sounds.length >= 4}
        onClick={() => {
          setPicker(!picker);
          void studio
            .library()
            .then(setAssets)
            .catch((e) => setError(String(e)));
        }}
      >
        {t("Add sound")}
      </Button>
      {picker && (
        <div>
          {assets
            .filter((a) => a.kind === "audio")
            .map((a) => (
              <Button key={a.digest} onClick={() => addSound(a)}>
                {a.name}
              </Button>
            ))}
          <Input
            aria-label={t("Upload sound")}
            type="file"
            accept="audio/*"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file)
                void onair
                  .uploadAsset(file, "audio")
                  .then((a) => addSound(a as LibraryAsset))
                  .catch((e) => setError(String(e)));
            }}
          />
        </div>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
