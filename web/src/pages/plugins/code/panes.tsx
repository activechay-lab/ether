import { cn } from "@braedonsaunders/appkit-ui";
import { useCallback, useEffect, useRef, useState, type CSSProperties } from "react";
import { t } from "../../../i18n";

/** Pane widths Code Studio remembers between visits. */
interface Panes {
  left: number;
  right: number;
  collapsed: boolean;
}

const KEY = "ether-studio-panes";
const DEFAULTS: Panes = { left: 224, right: 420, collapsed: false };
const RAIL_WIDTH = 44;
const LEFT = { min: 160, max: 480 };
const RIGHT = { min: 300, max: 760 };
/** The editor never gets narrower than this while a pane is dragged. */
const EDITOR_MIN = 360;

function load(): Panes {
  try {
    const raw = JSON.parse(localStorage.getItem(KEY) ?? "{}") as Partial<Panes>;
    return {
      left: clamp(Number(raw.left) || DEFAULTS.left, LEFT.min, LEFT.max),
      right: clamp(Number(raw.right) || DEFAULTS.right, RIGHT.min, RIGHT.max),
      collapsed: Boolean(raw.collapsed),
    };
  } catch {
    return DEFAULTS;
  }
}

function clamp(v: number, min: number, max: number) {
  return Math.min(max, Math.max(min, Math.round(v)));
}

/**
 * Code Studio's three columns: a files pane that can fold to a rail, the
 * editor, and the tools pane. Both side panes resize by dragging their
 * inner edge. The layout only applies at desktop widths; smaller screens
 * stack as before.
 */
export function useStudioPanes() {
  const [panes, setPanes] = useState<Panes>(load);
  const grid = useRef<HTMLDivElement>(null);
  useEffect(() => {
    try {
      localStorage.setItem(KEY, JSON.stringify(panes));
    } catch {
      // Private windows may refuse storage; the layout still works.
    }
  }, [panes]);

  const room = useCallback(
    (other: number) => (grid.current?.clientWidth ?? 1600) - other - EDITOR_MIN,
    [],
  );
  const setLeft = useCallback(
    (w: number) =>
      setPanes((p) => ({
        ...p,
        left: clamp(w, LEFT.min, Math.max(LEFT.min, Math.min(LEFT.max, room(p.right)))),
      })),
    [room],
  );
  const setRight = useCallback(
    (w: number) =>
      setPanes((p) => {
        const left = p.collapsed ? RAIL_WIDTH : p.left;
        return {
          ...p,
          right: clamp(w, RIGHT.min, Math.max(RIGHT.min, Math.min(RIGHT.max, room(left)))),
        };
      }),
    [room],
  );
  const setCollapsed = useCallback(
    (collapsed: boolean) => setPanes((p) => ({ ...p, collapsed })),
    [],
  );

  const style = {
    "--studio-cols": `${panes.collapsed ? RAIL_WIDTH : panes.left}px minmax(0,1fr) ${panes.right}px`,
  } as CSSProperties;
  return {
    grid,
    style,
    /** Grid classes: one column on small screens, the three panes on desktop. */
    className: "grid min-h-0 flex-1 grid-cols-1 lg:[grid-template-columns:var(--studio-cols)]",
    ...panes,
    setLeft,
    setRight,
    setCollapsed,
    resetLeft: () => setLeft(DEFAULTS.left),
    resetRight: () => setRight(DEFAULTS.right),
    leftBounds: LEFT,
    rightBounds: RIGHT,
  };
}

/**
 * A pane's draggable inner edge. Drag with the mouse, use the arrow keys
 * when focused, or double-click to go back to the default width.
 */
export function PaneResizer({
  side,
  width,
  bounds,
  onWidth,
  onReset,
  label,
}: {
  /** Which edge of its pane the handle sits on. */
  side: "right" | "left";
  width: number;
  bounds: { min: number; max: number };
  onWidth: (w: number) => void;
  onReset: () => void;
  label: string;
}) {
  const start = useRef<{ x: number; w: number } | null>(null);
  const [dragging, setDragging] = useState(false);
  // A handle on a pane's right edge grows it to the right, and vice versa.
  const sign = side === "right" ? 1 : -1;
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuenow={width}
      aria-valuemin={bounds.min}
      aria-valuemax={bounds.max}
      tabIndex={0}
      title={t("Drag to resize, double-click to reset")}
      onPointerDown={(e) => {
        e.preventDefault();
        e.currentTarget.setPointerCapture(e.pointerId);
        start.current = { x: e.clientX, w: width };
        setDragging(true);
      }}
      onPointerMove={(e) => {
        if (!start.current) return;
        onWidth(start.current.w + sign * (e.clientX - start.current.x));
      }}
      onPointerUp={(e) => {
        e.currentTarget.releasePointerCapture(e.pointerId);
        start.current = null;
        setDragging(false);
      }}
      onDoubleClick={onReset}
      onKeyDown={(e) => {
        if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
        e.preventDefault();
        const step = e.shiftKey ? 64 : 16;
        onWidth(width + sign * (e.key === "ArrowRight" ? step : -step));
      }}
      className={cn(
        "group absolute inset-y-0 z-20 hidden w-2 cursor-col-resize touch-none select-none focus-visible:outline-none lg:block",
        side === "right" ? "-right-1" : "-left-1",
      )}
    >
      <span
        aria-hidden
        className={cn(
          "absolute inset-y-0 left-1/2 w-px -translate-x-1/2 bg-transparent transition-colors group-hover:bg-primary/60 group-focus-visible:bg-primary",
          dragging && "bg-primary",
        )}
      />
    </div>
  );
}
