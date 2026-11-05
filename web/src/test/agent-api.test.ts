import { describe, expect, it } from "vitest";
import {
  extractNavigateRoute,
  humanizeToolName,
  inferRecord,
  matchRoutePattern,
  parseAgentStatus,
  parseMessages,
  registrySuggestions,
  toolLabelsFromDescriptors,
} from "../components/agent/agentApi";
import type { Descriptor } from "../lib/extend";
import { createAgentMock, setMockAgentEnabled } from "../mock/extend/agent";

describe("agent API helpers", () => {
  it("reads every disabled spelling as no model", () => {
    expect(parseAgentStatus({ enabled: false }).enabled).toBe(false);
    expect(parseAgentStatus({ disabled: true }).enabled).toBe(false);
    expect(parseAgentStatus({ status: "disabled" }).enabled).toBe(false);
    expect(parseAgentStatus({ enabled: true, model: "m" })).toEqual({
      enabled: true,
      model: "m",
    });
  });

  it("parses wrapped and bare message pages", () => {
    const message = { id: "1", role: "user" as const, parts: [] };
    expect(parseMessages({ messages: [message], hasMore: true, running: true })).toEqual({
      messages: [message],
      hasOlder: true,
      running: true,
    });
    expect(parseMessages([message])).toEqual({
      messages: [message],
      hasOlder: false,
      running: false,
    });
  });

  it("infers only record routes", () => {
    expect(inferRecord("/viewers/3/taste")).toEqual({ type: "viewer", id: "3" });
    expect(inferRecord("/viewers")).toBeUndefined();
    expect(inferRecord("/collections/abc")).toEqual({ type: "collection", id: "abc" });
    expect(inferRecord("/collections/new")).toBeUndefined();
    expect(inferRecord("/releases/history")).toBeUndefined();
  });

  it("matches route patterns", () => {
    expect(matchRoutePattern("/releases", "/releases/history")).toBe(true);
    expect(matchRoutePattern("/releases", "/releasesx")).toBe(false);
    expect(matchRoutePattern("/livetv/*", "/livetv/guide")).toBe(true);
    expect(matchRoutePattern("/", "/")).toBe(true);
    expect(matchRoutePattern("/", "/sources")).toBe(false);
  });

  it("takes suggestions only from flagged commands on this route", () => {
    const descriptors: Descriptor[] = [
      {
        kind: "command",
        id: "why",
        owner: "builtin",
        label: { source: "Why this release?" },
        meta: { agentSuggestion: true, routePatterns: ["/releases/*"] },
      },
      {
        kind: "command",
        id: "plain",
        owner: "builtin",
        label: { source: "Open releases" },
        meta: {},
      },
      {
        kind: "command",
        id: "channels",
        owner: "builtin",
        label: { source: "Which channels?" },
        meta: { agentSuggestion: true, routePatterns: ["/livetv"] },
      },
    ];
    expect(registrySuggestions(descriptors, "/releases/history")).toEqual(["Why this release?"]);
    expect(registrySuggestions(descriptors, "/livetv")).toEqual(["Which channels?"]);
  });

  it("humanizes tool names and prefers served titles", () => {
    expect(humanizeToolName("resolve_trace")).toBe("Resolve trace");
    const labels = toolLabelsFromDescriptors([
      {
        kind: "tool",
        id: "tool/resolve_trace",
        owner: "builtin",
        label: { source: "Why this release was picked" },
      },
    ]);
    expect(labels.resolve_trace).toBe("Why this release was picked");
  });

  it("extracts a navigate route once", () => {
    const messages = [
      {
        id: "a1",
        role: "assistant" as const,
        parts: [
          {
            type: "dynamic-tool",
            toolName: "navigate",
            toolCallId: "c1",
            state: "output-available",
            output: { navigate: "/sources" },
          },
        ],
      },
    ];
    const found = extractNavigateRoute(messages, new Set());
    expect(found?.route).toBe("/sources");
    expect(extractNavigateRoute(messages, new Set(found ? [found.key] : []))).toBeNull();
  });
});

describe("mock agent stream", () => {
  it("streams AI SDK chunks and stores the approval only in the transcript", async () => {
    setMockAgentEnabled(true);
    const mock = createAgentMock();
    const response = await mock.handle("POST", ["agent", "turn"], new URLSearchParams(), {
      prompt: "Blocklist this release",
      context: { route: "/releases" },
    });
    expect(response).not.toBeNull();
    expect(response!.headers.get("x-vercel-ai-ui-message-stream")).toBe("v1");
    const threadId = response!.headers.get("x-ether-thread-id");
    expect(threadId).toBeTruthy();
    const streamed = await response!.text();
    const chunks = streamed
      .split("\n\n")
      .filter((line) => line.startsWith("data: ") && !line.includes("[DONE]"))
      .map((line) => JSON.parse(line.slice(6)) as { type: string });
    expect(chunks[0]!.type).toBe("start");
    expect(chunks.some((chunk) => chunk.type === "text-delta")).toBe(true);
    expect(chunks.some((chunk) => chunk.type === "tool-output-available")).toBe(true);
    expect(chunks.some((chunk) => chunk.type === "approval-request")).toBe(false);
    expect(streamed).toContain("data: [DONE]");

    const stored = await mock.handle(
      "GET",
      ["agent", "threads", threadId!, "messages"],
      new URLSearchParams(),
      undefined,
    );
    const body = (await stored!.json()) as { messages: { parts: { type: string }[] }[] };
    const types = body.messages.flatMap((m) => m.parts.map((p) => p.type));
    expect(types).toContain("approval-request");
  });
});
