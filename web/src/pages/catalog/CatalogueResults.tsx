import { Button } from "@braedonsaunders/appkit-ui";
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Artwork } from "../../components/Artwork";
import type { TitleCard } from "../../lib/contract";
import { fixed } from "../../lib/format";
import { t, tn } from "../../i18n";
import "./CatalogueResults.css";

export interface CatalogueResultsProps {
  items: TitleCard[];
  list: boolean;
  queryKey: string;
  page: number;
  hasMore: boolean;
  loading: boolean;
  onLoadMore: () => void;
  onInspect: (key: string) => void;
  renderAction?: (key: string) => ReactNode;
  onBulkWatchlist?: (keys: string[]) => Promise<void>;
}

/** Keeps only visible rows mounted; pages remain available when scrolling back. */
export function CatalogueResults({
  items,
  list,
  queryKey,
  page,
  hasMore,
  loading,
  onLoadMore,
  onInspect,
  renderAction,
  onBulkWatchlist,
}: CatalogueResultsProps) {
  const [pages, setPages] = useState<{ key: string; pages: Record<number, TitleCard[]> }>({
    key: queryKey,
    pages: {},
  });
  const viewport = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 900, height: 640, top: 0 });
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [focused, setFocused] = useState(0);
  const [columns, setColumns] = useState(["year", "rating", "runtime", "genres"]);
  const [writing, setWriting] = useState(false);
  const [bulkError, setBulkError] = useState(false);
  const [peek, setPeek] = useState<string | null>(null);
  const requested = useRef(0);
  useEffect(() => {
    if (loading) return;
    setPages((prev) => ({
      key: queryKey,
      pages: { ...(prev.key === queryKey ? prev.pages : {}), [page]: items },
    }));
  }, [items, loading, page, queryKey]);
  useEffect(() => {
    setSelected(new Set());
    setFocused(0);
    setPeek(null);
    requested.current = 0;
    if (viewport.current) viewport.current.scrollTop = 0;
  }, [queryKey]);
  useLayoutEffect(() => {
    const el = viewport.current;
    if (!el) return;
    const measure = () =>
      setSize({ width: el.clientWidth || 900, height: el.clientHeight || 640, top: el.scrollTop });
    measure();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    observer?.observe(el);
    el.addEventListener("scroll", measure, { passive: true });
    return () => {
      observer?.disconnect();
      el.removeEventListener("scroll", measure);
    };
  }, []);
  const stored = pages.key === queryKey ? pages.pages : {};
  const all = Object.values({ ...stored, ...(!loading ? { [page]: items } : {}) }).flat();
  const titles = [...new Map(all.map((title) => [title.key, title])).values()];
  const lanes = list ? 1 : Math.max(2, Math.floor(size.width / 165));
  const rowHeight = list ? 84 : Math.round((size.width / lanes - 16) * 1.5) + 114;
  const rowCount = Math.ceil(titles.length / lanes);
  const firstRow = Math.max(0, Math.floor(size.top / rowHeight) - 2);
  const lastRow = Math.min(rowCount, Math.ceil((size.top + size.height) / rowHeight) + 2);
  const visible = titles.slice(firstRow * lanes, lastRow * lanes);
  const nearEnd = size.top + size.height >= rowCount * rowHeight - rowHeight * 2;
  useEffect(() => {
    if (nearEnd && hasMore && !loading && titles.length && requested.current !== page) {
      requested.current = page;
      onLoadMore();
    }
  }, [nearEnd, hasMore, loading, page, titles.length, onLoadMore]);
  const moveFocus = (index: number) => {
    const next = Math.max(0, Math.min(titles.length - 1, index));
    setFocused(next);
    const el = viewport.current;
    if (!el) return;
    const top = Math.floor(next / lanes) * rowHeight;
    if (top < el.scrollTop) el.scrollTop = top;
    else if (top + rowHeight > el.scrollTop + size.height)
      el.scrollTop = top + rowHeight - size.height;
    requestAnimationFrame(() =>
      el.querySelector<HTMLButtonElement>(`[data-catalog-index="${next}"]`)?.focus(),
    );
  };
  const toggle = (key: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  return (
    <div className="catalogue-result-browser">
      <div className="catalogue-result-options">
        {onBulkWatchlist ? (
          <>
            <span role="status">{tn(selected.size, "{count} selected", "{count} selected")}</span>
            <Button
              size="sm"
              variant="ghost"
              disabled={!titles.length}
              onClick={() => setSelected(new Set(titles.map((title) => title.key)))}
            >
              {t("Select loaded titles")}
            </Button>
            {!!selected.size && (
              <>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={writing}
                  onClick={async () => {
                    setWriting(true);
                    setBulkError(false);
                    try {
                      await onBulkWatchlist([...selected]);
                      setSelected(new Set());
                    } catch {
                      setBulkError(true);
                    } finally {
                      setWriting(false);
                    }
                  }}
                >
                  {t("Add to Watchlist")}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
                  {t("Clear selection")}
                </Button>
              </>
            )}
          </>
        ) : null}
        {list && (
          <details className="catalogue-columns">
            <summary>{t("Columns")}</summary>
            <div>
              {["year", "rating", "runtime", "genres"].map((column) => (
                <label key={column}>
                  <input
                    type="checkbox"
                    checked={columns.includes(column)}
                    onChange={() =>
                      setColumns((prev) =>
                        prev.includes(column)
                          ? prev.filter((v) => v !== column)
                          : [...prev, column],
                      )
                    }
                  />
                  {column === "year"
                    ? t("Year")
                    : column === "rating"
                      ? t("Rating")
                      : column === "runtime"
                        ? t("Runtime")
                        : t("Genres")}
                </label>
              ))}
            </div>
          </details>
        )}
      </div>
      {bulkError && <p role="alert">{t("Some watchlists could not be updated. Try again.")}</p>}
      <div
        className={`catalogue-result-viewport ${list ? "is-list" : "is-grid"}`}
        ref={viewport}
        aria-label={t("Catalog titles")}
        onKeyDown={(event) => {
          if (
            !(event.target instanceof HTMLElement) ||
            !event.target.hasAttribute("data-catalog-index")
          )
            return;
          const delta =
            event.key === "ArrowDown"
              ? lanes
              : event.key === "ArrowUp"
                ? -lanes
                : event.key === "ArrowRight"
                  ? 1
                  : event.key === "ArrowLeft"
                    ? -1
                    : 0;
          if (delta) {
            event.preventDefault();
            moveFocus(focused + delta);
          }
          if (event.key === "Home") {
            event.preventDefault();
            moveFocus(0);
          }
          if (event.key === "End") {
            event.preventDefault();
            moveFocus(titles.length - 1);
          }
        }}
      >
        <div className="catalogue-result-window" style={{ height: rowCount * rowHeight }}>
          {visible.map((title, offset) => {
            const index = firstRow * lanes + offset;
            return (
              <article
                className="catalogue-result-card"
                key={title.key}
                style={{
                  top: Math.floor(index / lanes) * rowHeight,
                  left: `${((index % lanes) * 100) / lanes}%`,
                  width: `${100 / lanes}%`,
                  height: rowHeight,
                }}
                onMouseEnter={() => setPeek(title.key)}
                onMouseLeave={() => setPeek(null)}
              >
                {onBulkWatchlist && (
                  <label className="catalogue-result-select">
                    <input
                      type="checkbox"
                      checked={selected.has(title.key)}
                      onChange={() => toggle(title.key)}
                      aria-label={t("Select {title}", { title: title.title })}
                    />
                  </label>
                )}
                <button
                  className="catalogue-result-open"
                  data-catalog-index={index}
                  tabIndex={index === focused ? 0 : -1}
                  onFocus={() => {
                    setFocused(index);
                    setPeek(title.key);
                  }}
                  onBlur={() => setPeek(null)}
                  onClick={() => onInspect(title.key)}
                  aria-label={t("Inspect {title}", { title: title.title })}
                >
                  <Artwork
                    src={title.poster}
                    alt=""
                    title={title.title}
                    className="catalogue-result-artwork"
                  />
                  <div className="catalogue-result-copy">
                    <h4>{title.title}</h4>
                    {(!list || columns.includes("year")) && (
                      <span>{title.year || t("Year unknown")}</span>
                    )}
                    {(!list || columns.includes("rating")) && !!title.rating && (
                      <span> ★ {fixed(title.rating, 1)}</span>
                    )}
                    {list && columns.includes("runtime") && (
                      <span>
                        {title.runtimeMinutes
                          ? t("{minutes} min", { minutes: title.runtimeMinutes })
                          : "—"}
                      </span>
                    )}
                    {list && columns.includes("genres") && (
                      <span className="catalogue-result-genres">{title.genres?.join(" · ")}</span>
                    )}
                  </div>
                </button>
                {renderAction && (
                  <div className="catalogue-result-action">{renderAction(title.key)}</div>
                )}
                {peek === title.key && title.summary && (
                  <div className="catalogue-result-peek">
                    <strong>{title.title}</strong>
                    <p>{title.summary}</p>
                    <span>{title.genres?.join(" · ")}</span>
                  </div>
                )}
              </article>
            );
          })}
        </div>
        {hasMore && (
          <Button variant="outline" disabled={loading} onClick={onLoadMore}>
            {loading ? t("Loading titles…") : t("Load more")}
          </Button>
        )}
      </div>
    </div>
  );
}
