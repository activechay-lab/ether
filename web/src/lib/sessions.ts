import { useEffect, useRef } from "react";
import { t } from "../i18n";
import { ApiError, apiFetch } from "./api";
import { normalizeSessions, type NowPlaying } from "./health";
import { useStreamEvent } from "./stream";

/** How often an open Overview or Activity re-reads the live streams. */
export const LIVE_SESSIONS_MS = 10_000;

/**
 * The live streams, as Overview and Activity both show them. They read this
 * one endpoint so the two pages never disagree. A status provider that
 * failed or timed out is an error, never an empty list: "Nothing playing"
 * must mean nothing is playing.
 */
export async function fetchLiveSessions(signal?: AbortSignal): Promise<NowPlaying[]> {
  const body = await apiFetch<{ value: unknown }>("/status/sessions", { signal });
  const value = body?.value ?? [];
  if (!Array.isArray(value)) {
    const message =
      value && typeof value === "object" && typeof (value as { error?: unknown }).error === "string"
        ? (value as { error: string }).error
        : "";
    throw new ApiError(
      503,
      "sessions_unavailable",
      message || t("Request failed ({status})", { status: 503 }),
    );
  }
  return normalizeSessions(value);
}

/**
 * Keep a live-streams view current with one rule on every page: reload when
 * the server says the set of streams changed, as soon as a hidden tab is
 * shown again, and every `ms` while visible as a safety net.
 */
export function useLiveSessionsRefresh(reload: () => unknown, ms: number = LIVE_SESSIONS_MS): void {
  const ref = useRef(reload);
  ref.current = reload;
  useStreamEvent("sessions.changed", () => void ref.current());
  useEffect(() => {
    const visible = () => document.visibilityState !== "hidden";
    const tick = () => {
      if (visible()) void ref.current();
    };
    const timer = window.setInterval(tick, ms);
    const onShow = () => {
      if (visible()) void ref.current();
    };
    document.addEventListener("visibilitychange", onShow);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onShow);
    };
  }, [ms]);
}
