import { useCallback, useEffect, useRef, useState } from "react";
import { fetchAgentStatus, cancelAgentTurn } from "../../../components/agent/agentApi";
import { api, apiStream } from "../../../lib/api";
import type { CollectionPreview, CollectionSpec } from "../../../lib/contract";
import { normalizeSpec } from "../../../lib/rules";
import { useAsync } from "../../../lib/hooks";
import { readAgentStream } from "../../plugins/LiveStage";

export interface AISearchResult {
  key: string;
  query: string;
  busy: boolean;
  text: string;
  spec?: CollectionSpec;
  preview?: CollectionPreview;
  error: boolean;
}

/** The collection builder's draft/evaluate/refine loop, as a temporary search.
 * No spec is saved and no catalogue filter chips are created. */
export function useCatalogAISearch(key: string, query: string, mediaType: string) {
  const capability = useAsync(() => fetchAgentStatus(), []);
  const [result, setResult] = useState<AISearchResult | null>(null);
  const current = useRef<{ controller: AbortController; turn?: string } | null>(null);
  const stop = useCallback(() => {
    const run = current.current;
    if (!run) return;
    run.controller.abort();
    if (run.turn) void cancelAgentTurn(run.turn).catch(() => {});
    current.current = null;
    setResult((previous) => (previous ? { ...previous, busy: false } : null));
  }, []);
  useEffect(() => {
    stop();
    setResult(null);
    return stop;
  }, [key, stop]);

  const start = useCallback(async () => {
    if (!capability.data?.enabled || !query.trim()) return;
    stop();
    const run: { controller: AbortController; turn?: string } = {
      controller: new AbortController(),
    };
    current.current = run;
    const signal = run.controller.signal;
    const live = () => current.current === run && !signal.aborted;
    const update = (patch: Partial<AISearchResult>) => {
      if (live())
        setResult((previous) => (previous?.key === key ? { ...previous, ...patch } : previous));
    };
    setResult({ key, query, busy: true, text: "", error: false });
    let draftNumber = 0;
    let preview: Promise<void> | undefined;
    let streamError = false;
    let text = "";
    const tools = new Map<string, string>();
    try {
      const response = await apiStream("/agent/turn", {
        method: "POST",
        signal,
        body: JSON.stringify({
          prompt: query,
          context: { route: "/catalog/search", mediaType: mediaType || "movie" },
        }),
      });
      if (!response.ok || !response.body) throw new Error("AI search unavailable");
      await readAgentStream(response, (chunk) => {
        if (!live()) return;
        if (chunk.type === "start") run.turn = chunk.messageId;
        if (chunk.type === "text-start") text = "";
        if (chunk.type === "text-delta") {
          text = (text + (chunk.delta || "")).slice(-4000);
          update({ text });
        }
        if (chunk.type === "error") streamError = true;
        if (chunk.type === "tool-input-available" && chunk.toolCallId && chunk.toolName)
          tools.set(chunk.toolCallId, chunk.toolName);
        if (
          chunk.type === "tool-output-available" &&
          chunk.toolCallId &&
          tools.get(chunk.toolCallId) === "collection_draft" &&
          chunk.output?.spec
        ) {
          const spec = normalizeSpec(chunk.output.spec as CollectionSpec);
          const version = ++draftNumber;
          update({ spec });
          // Same preview path as DescribeWall; only the newest draft may land.
          preview = api
            .previewCollection(spec, signal, 200)
            .then((value) => {
              if (version === draftNumber) update({ preview: value, error: false });
            })
            .catch(() => {
              if (version === draftNumber) update({ error: true });
            });
        }
      });
      await preview;
      if (streamError || draftNumber === 0) update({ error: true });
    } catch {
      if (!signal.aborted) update({ error: true });
    } finally {
      update({ busy: false });
      if (current.current === run) current.current = null;
    }
  }, [key, query, mediaType, capability.data?.enabled, stop]);
  return {
    enabled: capability.data?.enabled === true,
    result: result?.key === key ? result : null,
    start,
    stop,
  };
}
