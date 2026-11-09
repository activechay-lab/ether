/**
 * Console live stream: GET /api/stream server-sent events (WS2).
 *
 * EventSource cannot send the X-Ether-Admin-Token header, so the console
 * holds one shared fetch() connection with the session header instead. The
 * token never appears in the query string. The connection reconnects with
 * backoff and jitter, pauses while the tab is hidden, and resumes with the
 * last seen event id.
 */
import { useEffect, useRef } from "react";
import { API_BASE, getToken, onTokenChange, SESSION_HEADER } from "./api";

type StreamEventType =
  | "dashboard"
  | "extend.changed"
  | "plugins.changed"
  | "plugins.log"
  | "studio.reload"
  | "agent.approval"
  | "notice"
  | "sessions.changed";

export interface StreamEvent<T = unknown> {
  id: string;
  type: StreamEventType;
  data: T;
}

type Handler = (event: StreamEvent) => void;

const listeners = new Map<StreamEventType, Set<Handler>>();

/** Backoff between reconnect attempts; tests shrink these. */
export const streamBackoff = { baseMs: 1000, maxMs: 30000, jitterRatio: 0.25 };

/** Delay before attempt n (1-based), exponential with jitter. */
export function backoffDelay(attempt: number): number {
  const capped = Math.min(
    streamBackoff.maxMs,
    streamBackoff.baseMs * 2 ** Math.max(0, attempt - 1),
  );
  const j = streamBackoff.jitterRatio;
  return capped * (1 - j + Math.random() * 2 * j);
}

interface ParsedEvent {
  id: string;
  type: string;
  data: string;
}

/**
 * Incremental SSE frame parser. Feed decoded text; complete messages (fields
 * terminated by a blank line) are delivered to onEvent. Comments and the
 * retry field are ignored; multi-line data blocks join with newlines.
 */
export function createStreamParser(onEvent: (e: ParsedEvent) => void): {
  push: (chunk: string) => void;
} {
  let buf = "";
  let id = "";
  let type = "";
  let data: string[] = [];
  const dispatch = () => {
    if (type) onEvent({ id, type, data: data.join("\n") });
    id = "";
    type = "";
    data = [];
  };
  return {
    push(chunk: string) {
      buf += chunk.replace(/\r\n/g, "\n");
      let end: number;
      while ((end = buf.indexOf("\n\n")) >= 0) {
        const block = buf.slice(0, end);
        buf = buf.slice(end + 2);
        for (const line of block.split("\n")) {
          if (line.startsWith(":")) continue;
          else if (line.startsWith("id:")) id = line.slice(3).trim();
          else if (line.startsWith("event:")) type = line.slice(6).trim();
          else if (line.startsWith("data:")) {
            const rest = line.slice(5);
            data.push(rest.startsWith(" ") ? rest.slice(1) : rest);
          }
          // retry: and unknown fields are ignored.
        }
        dispatch();
      }
    },
  };
}

function hasListeners(): boolean {
  for (const set of listeners.values()) if (set.size) return true;
  return false;
}

let lastEventId: string | null = null;
let failures = 0;
let fatalAuth = false;
let running = false;
let epoch = 0;
let currentAbort: AbortController | null = null;
let retryTimer: ReturnType<typeof setTimeout> | null = null;
let retryWake: (() => void) | null = null;
let visHooked = false;

// Clear any 401/403 lockup and reconnect when the owner signs in (including
// finishing /setup on the same page without a reload) or signs out.
onTokenChange((token) => {
  fatalAuth = false;
  failures = 0;
  epoch += 1;
  currentAbort?.abort();
  currentAbort = null;
  if (retryTimer !== null) {
    clearTimeout(retryTimer);
    retryTimer = null;
  }
  retryWake?.();
  retryWake = null;
  if (token && hasListeners() && !tabHidden()) {
    ensureStream();
  }
});

function onVisibility(): void {
  if (document.visibilityState === "hidden") {
    epoch += 1;
    currentAbort?.abort();
    if (retryTimer !== null) {
      clearTimeout(retryTimer);
      retryTimer = null;
    }
    retryWake?.();
    retryWake = null;
  } else {
    failures = 0;
    ensureStream();
  }
}

function hookVisibility(): void {
  if (visHooked || typeof document === "undefined") return;
  visHooked = true;
  document.addEventListener("visibilitychange", onVisibility);
}

function sleepInterruptible(ms: number, myEpoch: number): Promise<void> {
  return new Promise((resolve) => {
    if (myEpoch !== epoch) {
      resolve();
      return;
    }
    retryWake = () => {
      retryWake = null;
      resolve();
    };
    retryTimer = setTimeout(() => {
      retryTimer = null;
      retryWake = null;
      resolve();
    }, ms);
  });
}

function dispatchEvent(type: string, id: string, raw: string): void {
  if (id) lastEventId = id;
  const set = listeners.get(type as StreamEventType);
  if (!set || !set.size) return;
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return;
  }
  const event: StreamEvent = { id, type: type as StreamEventType, data };
  for (const fn of [...set]) {
    try {
      fn(event);
    } catch (err) {
      // One bad subscriber must not kill the shared connection.
      console.error("console stream handler failed", err);
    }
  }
}

/** One connection attempt; returns "auth" when the session is gone. */
async function runOnce(myEpoch: number): Promise<"done" | "auth"> {
  const ctl = new AbortController();
  currentAbort = ctl;
  try {
    const query = lastEventId ? `?lastEventId=${encodeURIComponent(lastEventId)}` : "";
    const headers: Record<string, string> = { Accept: "text/event-stream" };
    const token = getToken();
    if (token) headers[SESSION_HEADER] = token;
    const resp = await fetch(`${API_BASE}/stream/${query}`, {
      headers,
      signal: ctl.signal,
    });
    if (resp.status === 401 || resp.status === 403) return "auth";
    if (!resp.ok || !resp.body) throw new Error(`stream failed (${resp.status})`);
    failures = 0;
    const parser = createStreamParser((e) => {
      if (myEpoch === epoch) dispatchEvent(e.type, e.id, e.data);
    });
    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    for (;;) {
      const { done, value } = await reader.read();
      if (done || myEpoch !== epoch) break;
      parser.push(decoder.decode(value, { stream: true }));
    }
    try {
      await reader.cancel();
    } catch {
      // The server already went away; nothing to cancel.
    }
    return "done";
  } catch (err) {
    if (myEpoch !== epoch) return "done";
    if (err instanceof DOMException && err.name === "AbortError") return "done";
    throw err;
  } finally {
    if (currentAbort === ctl) currentAbort = null;
  }
}

async function pump(myEpoch: number): Promise<void> {
  while (myEpoch === epoch && hasListeners() && !fatalAuth) {
    let outcome: "done" | "auth";
    try {
      outcome = await runOnce(myEpoch);
    } catch {
      outcome = "done";
    }
    if (myEpoch !== epoch || !hasListeners()) return;
    if (outcome === "auth") {
      // Signed out mid-session: stop polling the server until reload.
      fatalAuth = true;
      return;
    }
    failures += 1;
    await sleepInterruptible(backoffDelay(failures), myEpoch);
  }
}

function tabHidden(): boolean {
  return typeof document !== "undefined" && document.visibilityState === "hidden";
}

/** Open the shared connection if anyone listens and it is not open. */
export function ensureStream(): void {
  hookVisibility();
  if (running || fatalAuth || !hasListeners()) return;
  running = true;
  const myEpoch = epoch;
  void pump(myEpoch).finally(() => {
    running = false;
    if (epoch !== myEpoch && hasListeners() && !fatalAuth && !tabHidden()) {
      // A stop raced with new demand; reconnect under a fresh epoch.
      ensureStream();
    }
  });
}

/** Test helper: drop every listener and reset connection state. */
export function resetStreamState(): void {
  epoch += 1;
  currentAbort?.abort();
  currentAbort = null;
  if (retryTimer !== null) {
    clearTimeout(retryTimer);
    retryTimer = null;
  }
  retryWake?.();
  retryWake = null;
  listeners.clear();
  lastEventId = null;
  failures = 0;
  fatalAuth = false;
  running = false;
  streamBackoff.baseMs = 1000;
  streamBackoff.maxMs = 30000;
  streamBackoff.jitterRatio = 0.25;
}

function addListener(type: StreamEventType, fn: Handler): void {
  let set = listeners.get(type);
  if (!set) {
    set = new Set();
    listeners.set(type, set);
  }
  set.add(fn);
  ensureStream();
}

function removeListener(type: StreamEventType, fn: Handler): void {
  const set = listeners.get(type);
  if (!set) return;
  set.delete(fn);
  if (!hasListeners()) {
    epoch += 1;
    currentAbort?.abort();
    if (retryTimer !== null) {
      clearTimeout(retryTimer);
      retryTimer = null;
    }
    retryWake?.();
    retryWake = null;
  }
}

/**
 * Subscribe to one console event type over the shared connection. The
 * handler always sees the latest closure via a ref; resubscribing only
 * happens when the type changes.
 */
export function useStreamEvent<T = unknown>(
  type: StreamEventType,
  handler: (event: StreamEvent<T>) => void,
): void {
  const ref = useRef(handler);
  useEffect(() => {
    ref.current = handler;
  });
  useEffect(() => {
    const wrapped = (event: StreamEvent) => ref.current(event as StreamEvent<T>);
    addListener(type, wrapped);
    return () => removeListener(type, wrapped);
  }, [type]);
}
