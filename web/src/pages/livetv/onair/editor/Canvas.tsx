import { useEffect, useMemo, useRef, useState } from "react";
import { Input } from "@braedonsaunders/appkit-ui";
import { t } from "../../../../i18n";
import {
  renderDocument,
  releaseRenderer,
  evaluate,
  type Scene,
  type SceneBindings,
  type Layer,
  type Value,
} from "../scene-renderer";
import { Handles, pathNodes } from "./Handles";
import { isAnimated, setKeyframe } from "./keyframes";
import { layerBounds } from "./scene-bounds";
import {
  findLayer,
  updateLayer,
  removeLayers,
  duplicateLayers,
  groupLayers,
  ungroup,
  insertLayer,
  newLayerId,
} from "./scene-edit";
export type CanvasProps = {
  document: Scene;
  bindings: SceneBindings;
  time: number;
  selected: string[];
  onSelect(ids: string[]): void;
  onChange(doc: Scene): void;
  safe?: boolean;
  fit?: boolean;
  zoom?: number;
  pen?: boolean;
  onPenDone?(): void;
  onGestureStart?(): void;
  onGestureEnd?(): void;
  video?: React.ReactNode;
};
// Preserve animation tracks when an ordinary editor gesture changes an animated value.
export function editValue(
  layer: Layer,
  key: keyof Layer,
  value: Value,
  time: number,
): Partial<Layer> {
  return isAnimated(layer[key])
    ? { [key]: setKeyframe(layer, key, time, value)[key] }
    : { [key]: value };
}
export function Canvas(p: CanvasProps) {
  const viewport = useRef<HTMLDivElement>(null);
  const [fitWidth, setFitWidth] = useState(960);
  useEffect(() => {
    if (!p.fit || !viewport.current) return;
    const observer = new ResizeObserver(([entry]) => {
      if (entry)
        setFitWidth(
          Math.max(
            120,
            Math.min(entry.contentRect.width - 48, ((entry.contentRect.height - 48) * 16) / 9),
          ),
        );
    });
    observer.observe(viewport.current);
    return () => observer.disconnect();
  }, [p.fit]);
  const canvas = useRef<HTMLCanvasElement>(null),
    host = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<string>(),
    [editing, setEditing] = useState<string>(),
    [marquee, setMarquee] = useState<{ x: number; y: number; w: number; h: number }>(),
    [guides, setGuides] = useState<{ x?: number; y?: number }>({});
  const geometry = useMemo(
    () => layerBounds(p.document, p.bindings, p.time),
    [p.document, p.bindings, p.selected.length ? p.time : 0],
  );
  const [penPreview, setPenPreview] = useState("");
  const penPoints = useRef<{ x: number; y: number; cx?: number; cy?: number }[]>([]);
  const gesture = useRef<
    { x: number; y: number; doc: Scene; ids: string[]; mode: string } | undefined
  >(undefined);
  useEffect(() => {
    const ctx = canvas.current?.getContext("2d");
    if (ctx) renderDocument(ctx, p.document, p.bindings, p.time);
  }, [p.document, p.bindings, p.time]);
  useEffect(
    () => () => {
      const ctx = canvas.current?.getContext("2d");
      if (ctx) releaseRenderer(ctx);
    },
    [],
  );
  const point = (e: { clientX: number; clientY: number }) => {
    const r = host.current!.getBoundingClientRect();
    return {
      x: ((e.clientX - r.left) * 1920) / r.width,
      y: ((e.clientY - r.top) * 1080) / r.height,
    };
  };
  const hit = (q: { x: number; y: number }) =>
    [...geometry].reverse().find((g) => {
      const l = findLayer(p.document, g.id);
      const locked = (layers: Layer[], ancestor = false): boolean => {
        for (const layer of layers) {
          const blocked = ancestor || !!layer.locked;
          if (layer.id === g.id) return blocked;
          if (layer.layers && findLayer({ ...p.document, layers: layer.layers }, g.id))
            return locked(layer.layers, blocked);
        }
        return false;
      };
      if (locked(p.document.layers) || l?.type === "group") return false;
      const m = new DOMMatrix(g.matrix).inverse();
      const pt = m.transformPoint(q);
      return (
        pt.x >= g.local.x && pt.y >= g.local.y && pt.x <= g.local.x + g.w && pt.y <= g.local.y + g.h
      );
    });
  const move = (e: React.PointerEvent) => {
    const q = point(e),
      g = gesture.current;
    if (p.pen && g) {
      const last = penPoints.current.at(-1);
      if (last) {
        last.cx = q.x;
        last.cy = q.y;
        setPenPreview(penPoints.current.map((p, i) => `${i ? "L" : "M"}${p.x} ${p.y}`).join(" "));
      }
      return;
    }
    if (!g) {
      setHover(hit(q)?.id);
      return;
    }
    let dx = q.x - g.x,
      dy = q.y - g.y;
    if (e.shiftKey && g.mode === "move") {
      if (Math.abs(dx) > Math.abs(dy)) dy = 0;
      else dx = 0;
    }
    if (g.mode === "marquee") {
      setMarquee({
        x: Math.min(g.x, q.x),
        y: Math.min(g.y, q.y),
        w: Math.abs(dx),
        h: Math.abs(dy),
      });
      return;
    }
    let doc = g.doc;
    const nextGuides: { x?: number; y?: number } = {};
    for (const id of g.ids) {
      const l = findLayer(g.doc, id);
      if (!l || l.locked) continue;
      const n = (key: keyof Layer, f = 0) =>
        Number(evaluate(l[key] as Value, p.bindings, p.time) ?? f);
      let patch: Partial<Layer> = {};
      if (g.mode.startsWith("node:")) {
        const [, xi, yi, ox, oy] = g.mode.split(":").map(Number);
        const bounds = geometry.find((b) => b.id === id);
        if (bounds) {
          const at = new DOMMatrix(bounds.matrix).inverse().transformPoint(q);
          const nodes = pathNodes(l.path ?? "");
          nodes.tokens[xi!] = String(at.x - ox!);
          nodes.tokens[yi!] = String(at.y - oy!);
          doc = updateLayer(doc, id, { path: nodes.tokens.join(" ") });
        }
        continue;
      }
      if (g.mode === "rotate")
        patch = editValue(
          l,
          "rotate",
          e.shiftKey
            ? Math.round((Math.atan2(q.y - n("y"), q.x - n("x")) + Math.PI / 2) / (Math.PI / 12)) *
                (Math.PI / 12)
            : Math.atan2(q.y - n("y"), q.x - n("x")) + Math.PI / 2,
          p.time,
        );
      else if (g.mode === "resize") {
        patch = {
          ...editValue(
            l,
            "scaleX",
            Math.max(
              0.01,
              n("scaleX", 1) + dx / Math.max(1, geometry.find((b) => b.id === id)?.w ?? 100),
            ),
            p.time,
          ),
          ...editValue(
            l,
            "scaleY",
            Math.max(
              0.01,
              n("scaleY", 1) +
                (e.shiftKey ? dx : dy) / Math.max(1, geometry.find((b) => b.id === id)?.h ?? 100),
            ),
            p.time,
          ),
        };
      } else {
        let x = n("x") + dx,
          y = n("y") + dy;
        const xs = [
            0,
            960,
            1920,
            ...geometry
              .filter((b) => !g.ids.includes(b.id))
              .flatMap((b) => [b.x, b.x + b.w / 2, b.x + b.w]),
          ],
          ys = [
            0,
            540,
            1080,
            ...geometry
              .filter((b) => !g.ids.includes(b.id))
              .flatMap((b) => [b.y, b.y + b.h / 2, b.y + b.h]),
          ];
        const snapX = xs.find((v) => Math.abs(v - x) < 8),
          snapY = ys.find((v) => Math.abs(v - y) < 8);
        if (snapX !== undefined) {
          x = snapX;
          nextGuides.x = x;
        }
        if (snapY !== undefined) {
          y = snapY;
          nextGuides.y = y;
        }
        patch = { ...editValue(l, "x", x, p.time), ...editValue(l, "y", y, p.time) };
      }
      doc = updateLayer(doc, id, patch);
    }
    setGuides(nextGuides);
    p.onChange(doc);
  };
  return (
    <div
      ref={viewport}
      className={
        p.fit
          ? "relative flex h-full min-h-0 items-center justify-center overflow-auto p-6"
          : "overflow-auto p-4"
      }
      style={{
        backgroundImage: "radial-gradient(var(--color-border, #ffffff12) 1px, transparent 1px)",
        backgroundSize: "12px 12px",
      }}
    >
      {p.pen && (
        <p className="mb-2 text-sm text-fg-muted">
          {t("Click to add corners, drag to curve, and double-click to close the path.")}
        </p>
      )}
      <div
        ref={host}
        className="relative mx-auto aspect-video shrink-0 touch-none bg-black shadow-[0_8px_40px_#0005] ring-1 ring-white/10 outline-none focus-visible:ring-2 focus-visible:ring-primary"
        style={{ width: p.fit ? fitWidth * (p.zoom ?? 1) : `${(p.zoom ?? 1) * 100}%` }}
        tabIndex={0}
        role="application"
        aria-label={t("Scene canvas")}
        onPointerMove={move}
        onPointerLeave={() => setHover(undefined)}
        onPointerDown={(e) => {
          if ((e.target as HTMLElement).closest("input")) return;
          p.onGestureStart?.();
          const q = point(e);
          if (p.pen) {
            penPoints.current.push(q);
            gesture.current = { ...q, doc: p.document, ids: [], mode: "pen" };
            return;
          }
          const target = (e.target as HTMLElement).dataset.mode;
          const id = hit(q)?.id;
          const ids = target
            ? p.selected
            : id
              ? e.shiftKey
                ? p.selected.includes(id)
                  ? p.selected.filter((s) => s !== id)
                  : [...p.selected, id]
                : p.selected.includes(id)
                  ? p.selected
                  : [id]
              : [];
          p.onSelect(ids);
          gesture.current = {
            ...q,
            doc: p.document,
            ids,
            mode: target ?? (id ? "move" : "marquee"),
          };
          e.currentTarget.setPointerCapture(e.pointerId);
        }}
        onPointerUp={() => {
          p.onGestureEnd?.();
          if (p.pen) {
            gesture.current = undefined;
            return;
          }
          if (marquee)
            p.onSelect(
              geometry
                .filter(
                  (b) =>
                    !findLayer(p.document, b.id)?.locked &&
                    b.x >= marquee.x &&
                    b.y >= marquee.y &&
                    b.x + b.w <= marquee.x + marquee.w &&
                    b.y + b.h <= marquee.y + marquee.h,
                )
                .map((b) => b.id),
            );
          gesture.current = undefined;
          setMarquee(undefined);
          setGuides({});
        }}
        onDoubleClick={(e) => {
          if (p.pen) {
            const points = penPoints.current;
            const first = points[0];
            if (first) {
              const path =
                points
                  .map((q, i) =>
                    i
                      ? q.cx !== undefined
                        ? `Q${q.cx} ${q.cy} ${q.x} ${q.y}`
                        : `L${q.x} ${q.y}`
                      : `M${q.x} ${q.y}`,
                  )
                  .join(" ") + " Z";
              const id = newLayerId(p.document, "path");
              p.onChange(
                insertLayer(p.document, {
                  id,
                  type: "path",
                  name: t("Pen path"),
                  path,
                  fill: "{accent}",
                  stroke: "{text}",
                  strokeWidth: 2,
                }),
              );
              p.onSelect([id]);
            }
            penPoints.current = [];
            setPenPreview("");
            p.onPenDone?.();
            return;
          }
          const id = hit(point(e))?.id;
          if (id && findLayer(p.document, id)?.type === "text") setEditing(id);
        }}
        onKeyDown={(e) => {
          if ((e.target as HTMLElement).matches("input,textarea")) return;
          let doc = p.document;
          const cmd = e.metaKey || e.ctrlKey;
          if (e.key === "Delete" || e.key === "Backspace") doc = removeLayers(doc, p.selected);
          else if (cmd && e.key.toLowerCase() === "d") doc = duplicateLayers(doc, p.selected);
          else if (cmd && e.key.toLowerCase() === "g")
            doc = e.shiftKey
              ? p.selected.reduce((d, id) => ungroup(d, id), doc)
              : groupLayers(doc, p.selected);
          else if (e.key.startsWith("Arrow")) {
            for (const id of p.selected) {
              const l = findLayer(doc, id);
              if (!l || l.locked) continue;
              const key = e.key === "ArrowLeft" || e.key === "ArrowRight" ? "x" : "y";
              const delta =
                (e.shiftKey ? 10 : 1) * (e.key === "ArrowLeft" || e.key === "ArrowUp" ? -1 : 1);
              doc = updateLayer(
                doc,
                id,
                editValue(
                  l,
                  key,
                  Number(evaluate(l[key], p.bindings, p.time) ?? 0) + delta,
                  p.time,
                ),
              );
            }
          } else return;
          e.preventDefault();
          p.onChange(doc);
        }}
      >
        {!p.document.layers.length && (
          <div className="pointer-events-none absolute inset-0 z-10 flex flex-col items-center justify-center gap-2 bg-bg-subtle text-fg-muted">
            <span className="text-sm font-medium">{t("Your scene starts here")}</span>
            <span className="max-w-64 px-4 text-center text-xs">
              {t("Press T to add text, or pick a template scene")}
            </span>
          </div>
        )}
        {p.video}
        <canvas ref={canvas} width={960} height={540} className="h-full w-full" />
        <svg
          viewBox="0 0 1920 1080"
          className="pointer-events-none absolute inset-0 h-full w-full overflow-visible"
          aria-hidden="true"
        >
          {penPreview && <path d={penPreview} fill="none" stroke="#76a9ff" strokeWidth="3" />}
          {p.safe && (
            <g fill="none" stroke="#ffffff88" strokeWidth="2" strokeDasharray="12 8">
              <rect x="96" y="54" width="1728" height="972" />
              <rect x="192" y="108" width="1536" height="864" />
            </g>
          )}
          {geometry
            .filter((b) => p.selected.includes(b.id) || b.id === hover)
            .map((b) => (
              <g
                key={b.id}
                transform={`translate(${b.x} ${b.y}) rotate(${(b.rotate * 180) / Math.PI}) scale(${b.scaleX} ${b.scaleY})`}
                fill="none"
                stroke="#76a9ff"
                strokeWidth="3"
              >
                <rect width={b.w} height={b.h} />
                {p.selected.includes(b.id) && (
                  <>
                    <rect
                      data-mode="resize"
                      className="pointer-events-auto cursor-nwse-resize"
                      x={b.w - 10}
                      y={b.h - 10}
                      width="20"
                      height="20"
                      fill="white"
                    />
                    <line x1={b.w / 2} x2={b.w / 2} y1="0" y2="-45" />
                    <circle
                      data-mode="rotate"
                      className="pointer-events-auto cursor-grab"
                      cx={b.w / 2}
                      cy="-45"
                      r="12"
                      fill="white"
                    />
                  </>
                )}
              </g>
            ))}
          {geometry
            .filter((b) => p.selected.includes(b.id))
            .map((b) => (
              <Handles
                key={`nodes-${b.id}`}
                layer={findLayer(p.document, b.id)!}
                matrix={b.matrix}
              />
            ))}
          {marquee && <rect {...marquee} fill="#76a9ff33" stroke="#76a9ff" />}
          {guides.x !== undefined && (
            <line x1={guides.x} x2={guides.x} y1="0" y2="1080" stroke="#ff77d4" />
          )}
          {guides.y !== undefined && (
            <line y1={guides.y} y2={guides.y} x1="0" x2="1920" stroke="#ff77d4" />
          )}
        </svg>
        {editing && (
          <Input
            autoFocus
            className="absolute left-1/4 top-1/2 w-1/2"
            aria-label={t("Edit text")}
            value={String(findLayer(p.document, editing)?.text ?? "")}
            onChange={(e) => p.onChange(updateLayer(p.document, editing, { text: e.target.value }))}
            onBlur={() => setEditing(undefined)}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === "Escape") setEditing(undefined);
            }}
          />
        )}
      </div>
    </div>
  );
}
