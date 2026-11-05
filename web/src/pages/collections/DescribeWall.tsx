import { Button, cn } from "@braedonsaunders/appkit-ui";
import { Maximize2, Sparkles } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Artwork } from "../../components/Artwork";
import { t, tn } from "../../i18n";
import { api } from "../../lib/api";
import type { CollectionSpec, TitleCard } from "../../lib/contract";
import { formatNumber } from "../../lib/format";
import "../plugins/stage.css";
import "./describe.css";

/** Posters shorter than this drop the wall to one row; the rest are one click away. */
const MIN_TWO_ROW_HEIGHT = 96;
const GAP = 12;
const HEAD = 44;
const PAD = 16;
/** Titles fetched for the wall: more than the widest wall can show. */
const WALL_TITLES = 60;

interface Size {
  width: number;
  height: number;
}

/** Rows, columns and poster size that fill the area: two rows when they fit, else one. */
function geometry({ width, height }: Size) {
  const rowsHeight = Math.max(0, height - HEAD - PAD * 2);
  const rows = (rowsHeight - GAP) / 2 >= MIN_TWO_ROW_HEIGHT ? 2 : 1;
  let h = Math.max(48, (rowsHeight - GAP * (rows - 1)) / rows);
  let w = (h * 2) / 3;
  const inner = Math.max(0, width - PAD * 2);
  let cols = Math.max(3, Math.floor((inner + GAP) / (w + GAP)));
  if (cols * (w + GAP) - GAP > inner) {
    w = (inner - GAP * (cols - 1)) / cols;
    h = (w * 3) / 2;
  }
  cols = Math.min(cols, 18);
  return { rows, cols, w, h };
}

/**
 * The collection as it takes shape: the draft's posters fill the area and
 * flow in as each draft arrives. Before the first draft it is an animated
 * wall of empty posters with a few ideas to start from.
 */
export function DescribeWall({
  spec,
  building,
  examples,
  onExample,
  onOpenTitle,
  onShowMore,
}: {
  spec: CollectionSpec | null;
  building: boolean;
  examples: readonly string[];
  onExample: (text: string) => void;
  onOpenTitle: (key: string) => void;
  onShowMore: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState<Size>({ width: 0, height: 0 });
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    // A layout-less renderer (tests) reports 0; fall back to a typical drawer.
    const measure = () => setSize({ width: el.clientWidth || 840, height: el.clientHeight || 520 });
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const specKey = spec ? JSON.stringify(spec) : "";
  const [shown, setShown] = useState<{ key: string; items: TitleCard[]; total: number } | null>(
    null,
  );
  useEffect(() => {
    if (!spec) {
      setShown(null);
      return;
    }
    const ctl = new AbortController();
    api
      .previewCollection(spec, ctl.signal, WALL_TITLES)
      .then((r) => setShown({ key: specKey, items: r.items, total: r.total }))
      .catch(() => undefined);
    return () => ctl.abort();
    // The spec's content, not its identity, decides a reload.
  }, [specKey]);

  const fit = geometry(size);
  const { rows, w, h } = fit;
  // A short collection splits evenly over the rows instead of leaving one poster alone.
  const cols =
    shown && shown.items.length < fit.cols * rows
      ? Math.max(1, Math.ceil(shown.items.length / rows))
      : fit.cols;
  const slots = fit.cols * rows;
  const items = shown?.items.slice(0, cols * rows) ?? [];
  const grid = {
    gridTemplateColumns: `repeat(${cols}, ${w}px)`,
    gridAutoRows: `${h}px`,
    gap: `${GAP}px`,
  };

  return (
    <div
      ref={ref}
      className={cn("flex h-full flex-col overflow-hidden", building && "describe-building")}
      style={{ padding: PAD }}
    >
      {shown && size.width ? (
        <div className="mx-auto flex flex-col" style={{ width: cols * w + GAP * (cols - 1) }}>
          <div className="flex shrink-0 items-center gap-3" style={{ height: HEAD }}>
            <h3 className="min-w-0 truncate text-base font-semibold text-fg">
              {spec?.name || t("Untitled collection")}
            </h3>
            <span className="shrink-0 text-sm tabular-nums text-fg-muted">
              {tn(shown.total, "{count} title", "{count} titles", {
                count: formatNumber(shown.total),
              })}
            </span>
            <Button className="ml-auto shrink-0" variant="outline" size="sm" onClick={onShowMore}>
              <Maximize2 size={14} /> {t("Show more")}
            </Button>
          </div>
          <div className="grid justify-center" style={grid} aria-label={t("Collection posters")}>
            {items.map((it, i) => (
              <button
                key={`${shown.key}:${it.key}`}
                type="button"
                aria-label={it.title}
                onClick={() => onOpenTitle(it.key)}
                className="stage-flow rounded-md transition-transform hover:scale-[1.03] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
                style={{ animationDelay: `${i * 70}ms` }}
              >
                <Artwork
                  src={it.poster}
                  alt={it.title}
                  title={it.title}
                  className="h-full w-full overflow-hidden rounded-md shadow-md ring-1 ring-border-subtle"
                />
              </button>
            ))}
          </div>
        </div>
      ) : size.width ? (
        <div className="relative flex-1">
          <div className="grid h-full content-center justify-center" style={grid} aria-hidden>
            {Array.from({ length: slots }, (_, i) => (
              <div
                key={i}
                className="describe-ghost"
                style={{
                  animationDelay: `${(i % fit.cols) * 140 + Math.floor(i / fit.cols) * 70}ms`,
                }}
              />
            ))}
          </div>
          <div className="absolute inset-0 flex items-center justify-center p-6">
            <div className="stage-rise max-w-lg rounded-2xl border border-border bg-surface/90 p-6 text-center shadow-lg backdrop-blur">
              <span className="mx-auto flex size-11 items-center justify-center rounded-full bg-primary/15 text-primary">
                <Sparkles size={20} aria-hidden />
              </span>
              <h3 className="mt-3 text-lg font-semibold text-fg">
                {building
                  ? t("Ether is looking through your catalogue…")
                  : t("What should this collection be?")}
              </h3>
              <p className="mt-1.5 text-sm text-fg-muted">
                {building
                  ? t("The posters appear here as soon as the first draft is ready.")
                  : t(
                      "Say it in plain words. Ether searches your catalogue, tries a few drafts and shows the posters as it goes. Nothing is saved until you choose.",
                    )}
              </p>
              {building ? null : (
                <div className="mt-4 flex flex-wrap justify-center gap-1.5">
                  {examples.map((ex) => (
                    <button
                      key={ex}
                      type="button"
                      onClick={() => onExample(ex)}
                      className="rounded-full border border-border px-2.5 py-1 text-xs text-fg-muted transition-colors hover:border-border-strong hover:text-fg"
                    >
                      {ex}
                    </button>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}
