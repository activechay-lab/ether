import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import { useCatalogAISearch } from "./useCatalogAISearch";

const draft = {
  name: "Rainy night films",
  mediaType: "movie",
  sources: [{ kind: "catalog" }],
  rules: [],
  match: "all",
  sort: "rating",
  limit: 50,
};
const chunks = [
  { type: "start", messageId: "turn-1" },
  { type: "text-start" },
  { type: "text-delta", delta: "Checking cosy, whimsical films." },
  {
    type: "tool-input-available",
    toolCallId: "call-1",
    toolName: "collection_draft",
    input: { spec: draft },
  },
  { type: "tool-output-available", toolCallId: "call-1", output: { spec: draft, total: 1 } },
  { type: "finish" },
];
describe("catalogue AI collection flow", () => {
  it("uses the collection agent and preview, preserving the prompt rather than generating chips", async () => {
    const fetch = vi.fn(async (url: string, _init?: RequestInit) => {
      if (url.endsWith("/agent/status")) return new Response(JSON.stringify({ enabled: true }));
      if (url.endsWith("/agent/turn"))
        return new Response(chunks.map((c) => `data: ${JSON.stringify(c)}\n\n`).join(""));
      if (url.includes("/collections/preview"))
        return new Response(
          JSON.stringify({
            items: [{ key: "1", title: "Amélie", type: "movie" }],
            total: 1,
            warnings: [],
            tookMs: 1,
          }),
        );
      throw new Error("unexpected route " + url);
    });
    vi.stubGlobal("fetch", fetch);
    const { result } = renderHook(() =>
      useCatalogAISearch("query-1", "cute whimsical rainy girls night in", "movie"),
    );
    await waitFor(() => expect(result.current.enabled).toBe(true));
    await act(() => result.current.start());
    expect(result.current.result).toMatchObject({
      busy: false,
      error: false,
      query: "cute whimsical rainy girls night in",
      spec: { name: draft.name },
      preview: { items: [{ title: "Amélie" }] },
    });
    const request = fetch.mock.calls.find(([url]) => url.endsWith("/agent/turn"))!;
    expect(JSON.parse(request[1]!.body as string)).toEqual({
      prompt: "cute whimsical rainy girls night in",
      context: { route: "/catalog/search", mediaType: "movie" },
    });
    expect(fetch.mock.calls.some(([url]) => url.includes("/catalog/interpret"))).toBe(false);
  });
  it("cancels the previous stream and refuses late drafts after the query changes", async () => {
    let cancelled = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string, init?: RequestInit) => {
        if (url.endsWith("/agent/status")) return new Response(JSON.stringify({ enabled: true }));
        if (url.endsWith("/agent/turn"))
          return new Promise<Response>((_resolve, reject) =>
            init!.signal!.addEventListener("abort", () => {
              cancelled = true;
              reject(new DOMException("Aborted", "AbortError"));
            }),
          );
        throw new Error("unexpected route");
      }),
    );
    const { result, rerender } = renderHook(
      ({ query }) => useCatalogAISearch(query, query, "movie"),
      { initialProps: { query: "old search" } },
    );
    await waitFor(() => expect(result.current.enabled).toBe(true));
    act(() => {
      void result.current.start();
    });
    expect(result.current.result?.busy).toBe(true);
    rerender({ query: "new search" });
    await waitFor(() => expect(cancelled).toBe(true));
    expect(result.current.result).toBeNull();
  });
});
