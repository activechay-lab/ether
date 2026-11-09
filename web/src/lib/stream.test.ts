import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  backoffDelay,
  createStreamParser,
  ensureStream,
  resetStreamState,
  streamBackoff,
  useStreamEvent,
  type StreamEvent,
} from "./stream";
import { setToken } from "./api";

interface FakeReader {
  read: () => Promise<{ done: boolean; value?: Uint8Array }>;
  cancel: () => Promise<void>;
}

function scriptedBody(chunks: (string | null)[], onCancel?: () => void): unknown {
  const enc = new TextEncoder();
  let i = 0;
  const reader: FakeReader = {
    read: async () => {
      if (i >= chunks.length) return { done: true };
      const chunk = chunks[i++]!;
      if (chunk === null) return { done: true };
      return { done: false, value: enc.encode(chunk) };
    },
    cancel: async () => {
      onCancel?.();
    },
  };
  return { getReader: () => reader };
}

function streamResponse(chunks: (string | null)[]): unknown {
  return { ok: true, status: 200, body: scriptedBody(chunks) };
}

function eventually(fn: () => void, ms = 2000): Promise<void> {
  const start = Date.now();
  return (async function poll(): Promise<void> {
    try {
      fn();
    } catch (err) {
      if (Date.now() - start > ms) throw err;
      await new Promise((r) => setTimeout(r, 10));
      return poll();
    }
  })();
}

afterEach(() => {
  resetStreamState();
  setToken("");
});

describe("createStreamParser", () => {
  it("delivers frames and ignores comments and retry hints", () => {
    const seen: { id: string; type: string; data: string }[] = [];
    const parser = createStreamParser((e) => seen.push(e));
    parser.push("retry: 10000\n\n: heartbeat 123\n\n");
    expect(seen).toEqual([]);
    parser.push('id: 4\nevent: notice\ndata: {"a":1}\n\n');
    expect(seen).toEqual([{ id: "4", type: "notice", data: '{"a":1}' }]);
  });

  it("joins multi-line data blocks and split chunks", () => {
    const seen: { id: string; type: string; data: string }[] = [];
    const parser = createStreamParser((e) => seen.push(e));
    parser.push("id: 5\nevent: plugins.log\ndata: line one\nda");
    parser.push("ta: line two\n\n");
    expect(seen).toEqual([{ id: "5", type: "plugins.log", data: "line one\nline two" }]);
  });

  it("tolerates CRLF line endings", () => {
    const seen: { id: string; type: string; data: string }[] = [];
    const parser = createStreamParser((e) => seen.push(e));
    parser.push("id: 6\r\nevent: notice\r\ndata: {}\r\n\r\n");
    expect(seen).toEqual([{ id: "6", type: "notice", data: "{}" }]);
  });
});

describe("backoffDelay", () => {
  it("grows exponentially within the cap without jitter", () => {
    streamBackoff.baseMs = 100;
    streamBackoff.maxMs = 250;
    streamBackoff.jitterRatio = 0;
    expect(backoffDelay(1)).toBe(100);
    expect(backoffDelay(2)).toBe(200);
    expect(backoffDelay(3)).toBe(250);
    expect(backoffDelay(10)).toBe(250);
  });
});

describe("shared stream connection", () => {
  it("sends the session header and dispatches events to listeners", async () => {
    setToken("test-session");
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        streamResponse(['id: 1\nevent: notice\ndata: {"msg":"hi"}\n\n']) as Response,
      );
    vi.stubGlobal("fetch", fetcher);
    const seen: StreamEvent<{ msg: string }>[] = [];
    const { unmount } = renderHook(() =>
      useStreamEvent<{ msg: string }>("notice", (e) => seen.push(e)),
    );
    await eventually(() => expect(seen).toHaveLength(1));
    expect(seen[0]).toEqual({ id: "1", type: "notice", data: { msg: "hi" } });
    const [url, init] = fetcher.mock.calls[0]!;
    expect(String(url)).toBe("/ether/api/stream/");
    expect(new Headers(init?.headers).get("X-Ether-Admin-Token")).toBe("test-session");
    expect(new Headers(init?.headers).get("Accept")).toBe("text/event-stream");
    expect(String(url)).not.toContain("test-session");
    unmount();
  });

  it("reconnects after a drop and resumes with the last event id", async () => {
    streamBackoff.baseMs = 10;
    streamBackoff.maxMs = 20;
    streamBackoff.jitterRatio = 0;
    const urls: string[] = [];
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async (input) => {
      urls.push(String(input));
      if (urls.length === 1)
        return streamResponse(["id: 7\nevent: notice\ndata: {}\n\n", null]) as Response;
      return streamResponse([]) as unknown as Response;
    });
    vi.stubGlobal("fetch", fetcher);
    const seen: StreamEvent[] = [];
    const { unmount } = renderHook(() => useStreamEvent("notice", (e) => seen.push(e)));
    await eventually(() => expect(urls.length).toBeGreaterThanOrEqual(2));
    expect(urls[0]).toBe("/ether/api/stream/");
    expect(urls[1]).toBe("/ether/api/stream/?lastEventId=7");
    unmount();
  });

  it("stops retrying when the session is gone", async () => {
    streamBackoff.baseMs = 10;
    streamBackoff.maxMs = 20;
    streamBackoff.jitterRatio = 0;
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue({ ok: false, status: 401, body: null } as unknown as Response);
    vi.stubGlobal("fetch", fetcher);
    const { unmount } = renderHook(() => useStreamEvent("notice", () => {}));
    await new Promise((r) => setTimeout(r, 150));
    expect(fetcher).toHaveBeenCalledTimes(1);
    unmount();
  });

  it("recovers from a 401 once setToken stores a new session", async () => {
    streamBackoff.baseMs = 10;
    streamBackoff.maxMs = 20;
    streamBackoff.jitterRatio = 0;
    const tokens: (string | null)[] = [];
    const fetcher = vi.fn<typeof fetch>().mockImplementation(async (_input, init) => {
      const tok = new Headers(init?.headers).get("X-Ether-Admin-Token");
      tokens.push(tok);
      if (!tok) {
        return { ok: false, status: 401, body: null } as unknown as Response;
      }
      return streamResponse(['id: 9\nevent: dashboard\ndata: {"ok":true}\n\n']) as Response;
    });
    vi.stubGlobal("fetch", fetcher);
    const seen: StreamEvent<{ ok: boolean }>[] = [];
    const { unmount } = renderHook(() =>
      useStreamEvent<{ ok: boolean }>("dashboard", (e) => seen.push(e)),
    );
    await eventually(() => expect(fetcher).toHaveBeenCalledTimes(1));
    expect(tokens).toEqual([null]);

    setToken("fresh-session");
    await eventually(() => expect(seen).toHaveLength(1));
    expect(tokens).toEqual([null, "fresh-session"]);
    expect(seen[0]?.data).toEqual({ ok: true });
    unmount();
  });

  it("one bad subscriber does not break the others", async () => {
    const fetcher = vi
      .fn<typeof fetch>()
      .mockResolvedValue(streamResponse(['id: 1\nevent: notice\ndata: {"n":1}\n\n']) as Response);
    vi.stubGlobal("fetch", fetcher);
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});
    const good: StreamEvent[] = [];
    const { unmount } = renderHook(() => {
      useStreamEvent("notice", () => {
        throw new Error("boom");
      });
      useStreamEvent("notice", (e) => good.push(e));
    });
    await eventually(() => expect(good).toHaveLength(1));
    expect(errors).toHaveBeenCalled();
    unmount();
  });

  it("pauses while the tab is hidden and resumes when visible", async () => {
    let visibility: "visible" | "hidden" = "visible";
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      get: () => visibility,
    });
    try {
      const aborted: boolean[] = [];
      const fetcher = vi.fn<typeof fetch>().mockImplementation(
        (_input, init): Promise<Response> =>
          new Promise((_resolve, reject) => {
            const mine = aborted.length;
            aborted.push(false);
            const signal = init?.signal as AbortSignal | undefined;
            // Mirror real fetch: abort rejects the pending body.
            signal?.addEventListener("abort", () => {
              aborted[mine] = true;
              reject(new DOMException("aborted", "AbortError"));
            });
          }),
      );
      vi.stubGlobal("fetch", fetcher);
      const { unmount } = renderHook(() => useStreamEvent("notice", () => {}));
      await eventually(() => expect(fetcher).toHaveBeenCalledTimes(1));

      visibility = "hidden";
      document.dispatchEvent(new Event("visibilitychange"));
      await eventually(() => expect(aborted[0]).toBe(true));

      visibility = "visible";
      document.dispatchEvent(new Event("visibilitychange"));
      await eventually(() => expect(fetcher).toHaveBeenCalledTimes(2));
      unmount();
    } finally {
      delete (document as unknown as Record<string, unknown>).visibilityState;
    }
  });

  it("closes the connection once nobody listens", async () => {
    const signals: (AbortSignal | undefined)[] = [];
    const fetcher = vi.fn<typeof fetch>().mockImplementation(
      (_input, init): Promise<Response> =>
        new Promise(() => {
          signals.push(init?.signal as AbortSignal | undefined);
        }),
    );
    vi.stubGlobal("fetch", fetcher);
    const first = renderHook(() => useStreamEvent("notice", () => {}));
    const second = renderHook(() => useStreamEvent("notice", () => {}));
    await eventually(() => expect(fetcher).toHaveBeenCalledTimes(1));
    first.unmount();
    await new Promise((r) => setTimeout(r, 50));
    // One listener left: the shared connection stays open.
    expect(fetcher).toHaveBeenCalledTimes(1);
    expect(signals[0]?.aborted).toBe(false);
    second.unmount();
    await eventually(() => expect(signals[0]?.aborted).toBe(true));
  });
});

describe("ensureStream", () => {
  it("does nothing without listeners", () => {
    const fetcher = vi.fn<typeof fetch>();
    vi.stubGlobal("fetch", fetcher);
    ensureStream();
    expect(fetcher).not.toHaveBeenCalled();
  });
});
