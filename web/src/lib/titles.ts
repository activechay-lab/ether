import { useEffect, useState } from "react";
import { api } from "./api";
import type { TitleDetail } from "./contract";

const cache = new Map<string, Promise<TitleDetail | null>>();

/** Title metadata (artwork) by rating key, cached for the session; null when unknown. */
function loadTitle(key: string): Promise<TitleDetail | null> {
  let p = cache.get(key);
  if (!p) {
    p = api.title(key).catch(() => null);
    cache.set(key, p);
  }
  return p;
}

export function useTitle(key: string | undefined): TitleDetail | null {
  const [t, setT] = useState<TitleDetail | null>(null);
  useEffect(() => {
    if (!key) {
      setT(null);
      return;
    }
    let live = true;
    void loadTitle(key).then((v) => {
      if (live) setT(v);
    });
    return () => {
      live = false;
    };
  }, [key]);
  return t;
}

/**
 * A title's poster: TMDB's when it has a path, else Ether's generated poster
 * (the same image every frontend shows for a title with no artwork).
 */
export function titlePosterURL(
  title: { key?: string | number; title?: string; year?: number; posterPath?: string },
  width: 92 | 185 | 342,
): string | undefined {
  if (title.posterPath) return `https://image.tmdb.org/t/p/w${width}${title.posterPath}`;
  if (title.key == null || !title.title) return undefined;
  const q = new URLSearchParams({ k: String(title.key), t: title.title });
  if (title.year) q.set("y", String(title.year));
  q.set("w", String(width));
  return `/ether/api/image/generated?${q.toString()}`;
}
