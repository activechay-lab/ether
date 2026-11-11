/**
 * Content-kind handlers (Ether v0.3.8): catalogue providers, resolvers,
 * matchers, frontend adapters and setup tests, plus the dispatch the
 * router calls for /ether/v1/kinds, /ether/v1/setup and
 * /ether/v1/frontend. Wire shapes live in types.ts and are pinned by
 * sdk/conformance/kinds/fixture.json across all three SDKs.
 */
import type { PluginContext } from "./router.ts";
import type {
  KindBrowseRequest,
  KindChangePage,
  KindItem,
  KindMatchRequest,
  KindMatchScore,
  KindPage,
  KindResolveRequest,
  KindResolveResponse,
  MaybePromise,
  RawRequest,
  RawResponse,
  SetupTestRequest,
  SetupTestResult,
  SourceCandidate,
} from "./types.ts";

export interface CatalogProvider<S = Record<string, unknown>> {
  browse(req: KindBrowseRequest, ctx: PluginContext<S>): MaybePromise<KindPage>;
  detail(kind: string, id: string, ctx: PluginContext<S>): MaybePromise<KindItem>;
  /** Optional delta feed for pull sync; without it Ether re-walks. */
  changes?(req: { kind: string; cursor?: string; limit?: number }, ctx: PluginContext<S>): MaybePromise<KindChangePage>;
  /** Optional provider search; required when the kind declares browse.liveSearch. */
  search?(req: KindBrowseRequest, ctx: PluginContext<S>): MaybePromise<KindPage>;
}

export interface KindResolver<S = Record<string, unknown>> {
  resolve(req: KindResolveRequest, ctx: PluginContext<S>): MaybePromise<KindResolveResponse>;
}

export interface KindMatcher<S = Record<string, unknown>> {
  match(req: KindMatchRequest, ctx: PluginContext<S>): MaybePromise<{ matches: KindMatchScore[] }>;
}

/** Spec-name alias. */
export type Matcher<S = Record<string, unknown>> = KindMatcher<S>;

export interface FrontendAdapter {
  handle(
    method: string,
    path: string,
    headers: Record<string, string>,
    body: string,
  ): MaybePromise<{ status: number; headers?: Record<string, string>; body: string }>;
}

export interface SetupTester<S = Record<string, unknown>> {
  testSetup(req: SetupTestRequest, ctx: PluginContext<S>): MaybePromise<SetupTestResult>;
}

/** The stable ref matchers report for a source candidate. */
export function candidateRef(c: SourceCandidate): string {
  if (c.infoHash) return `infohash:${c.infoHash.toLowerCase()}`;
  if (c.magnet) return `magnet:${c.magnet}`;
  if (c.nzbUrl) return `nzb:${c.nzbUrl}`;
  return c.title ?? "";
}

/**
 * Mint a scoped, short-lived Ether stream URL for one of this plugin's own
 * kind items (spec §6.5). The viewer is the one Ether authenticated on this
 * request (X-Ether-Viewer); bytes are served by Ether, never by the plugin.
 */
export async function streamUrl<S = Record<string, unknown>>(
  ctx: PluginContext<S>,
  itemRef: string,
): Promise<string> {
  if (!ctx.ether?.frontend) throw new Error("stream url: no host service available");
  const raw = ctx.headers["X-Ether-Viewer"] ?? ctx.headers["x-ether-viewer"];
  const value = Array.isArray(raw) ? raw[0] : raw;
  const viewerId = Number.parseInt(String(value ?? ""), 10);
  if (!Number.isFinite(viewerId) || viewerId <= 0) {
    throw new Error("stream url: no authenticated viewer on this request");
  }
  return ctx.ether.frontend.streamUrl(viewerId, itemRef);
}

export interface KindHandlers<S = Record<string, unknown>> {
  catalog: Map<string, CatalogProvider<S>>;
  resolve: Map<string, KindResolver<S>>;
  match: Map<string, KindMatcher<S>>;
  frontend: Map<string, FrontendAdapter>;
  setup?: SetupTester<S>;
}

export function createKindHandlers<S = Record<string, unknown>>(): KindHandlers<S> {
  return { catalog: new Map(), resolve: new Map(), match: new Map(), frontend: new Map() };
}

function ok(body: unknown): RawResponse {
  return { status: 200, headers: { "Content-Type": "application/json" }, body };
}

function err(status: number, code: string, message: string): RawResponse {
  return { status, headers: { "Content-Type": "application/json" }, body: { error: { code, message } } };
}

function parseBody(body: unknown): Record<string, unknown> {
  if (typeof body === "string" && body.trim()) {
    const parsed = JSON.parse(body) as unknown;
    return parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : {};
  }
  if (body && typeof body === "object") return body as Record<string, unknown>;
  return {};
}

/** Dispatch one /ether/v1/* request; returns null when the path is not a kind route. */
export async function dispatchKinds<S = Record<string, unknown>>(
  handlers: KindHandlers<S>,
  req: RawRequest,
  ctx: PluginContext<S>,
): Promise<RawResponse | null> {
  const path = (req.path || "/").split("?")[0] ?? "/";
  if (!path.startsWith("/ether/v1/")) return null;
  const parts = path.slice("/ether/v1/".length).split("/").map((p) => decodeURIComponent(p));
  let body: Record<string, unknown>;
  try {
    body = parseBody(req.body);
  } catch {
    return err(400, "invalid_json", "Request body must be valid JSON.");
  }

  if (parts.length === 3 && parts[0] === "setup" && parts[2] === "test") {
    if (!handlers.setup) return err(404, "not_found", "Setup test handler is not registered.");
    const input = { step: String(body.step || parts[1]), fields: body.fields as Record<string, unknown> | undefined };
    return ok(await handlers.setup.testSetup(input, ctx));
  }

  if (parts.length >= 2 && parts[0] === "frontend") {
    const adapter = handlers.frontend.get(parts[1] ?? "");
    if (!adapter) return err(404, "not_found", `No frontend adapter is registered for "${parts[1]}".`);
    const headers: Record<string, string> = {};
    for (const [k, v] of Object.entries(req.headers ?? {})) headers[k] = Array.isArray(v) ? (v[0] ?? "") : v;
    const res = await adapter.handle((req.method || "GET").toUpperCase(), `/${parts.slice(2).join("/")}`, headers, typeof req.body === "string" ? req.body : "");
    if (res.body.length > 10 * 1024 * 1024) return err(502, "adapter_error", "Frontend adapter response is larger than 10 MiB; serve media through Ether stream URLs.");
    return { status: res.status, headers: res.headers ?? {}, body: res.body };
  }

  if (parts.length < 3 || parts[0] !== "kinds") return null;
  const kind = parts[1] ?? "";
  const tail = parts.slice(2);
  const browseReq = { ...body, kind } as unknown as KindBrowseRequest;
  const page = async (p: MaybePromise<KindPage>): Promise<RawResponse> => {
    const result = { ...(await p) };
    const limit = Number(browseReq.limit || 200);
    result.items = (result.items ?? []).slice(0, limit);
    if (!result.nextCursor) delete result.nextCursor;
    return ok(result);
  };

  if (tail.length === 1 && (tail[0] === "browse" || tail[0] === "search")) {
    const provider = handlers.catalog.get(kind);
    if (!provider) return err(404, "not_found", `No catalogue provider is registered for kind "${kind}".`);
    if (tail[0] === "search") {
      if (!provider.search) return err(501, "not_implemented", "This provider does not search.");
      return page(provider.search(browseReq, ctx));
    }
    return page(provider.browse(browseReq, ctx));
  }
  if (tail.length === 2 && tail[0] === "items") {
    const provider = handlers.catalog.get(kind);
    if (!provider) return err(404, "not_found", `No catalogue provider is registered for kind "${kind}".`);
    return ok(await provider.detail(kind, tail[1] ?? "", ctx));
  }
  if (tail.length === 1 && tail[0] === "changes") {
    const provider = handlers.catalog.get(kind);
    if (!provider?.changes) return err(501, "not_implemented", "This provider has no changes feed.");
    const result = { ...(await provider.changes({ kind, cursor: body.cursor as string | undefined, limit: body.limit as number | undefined }, ctx)) };
    result.upserts = result.upserts ?? [];
    return ok(result);
  }
  if (tail.length === 1 && tail[0] === "resolve") {
    const resolver = handlers.resolve.get(kind);
    if (!resolver) return err(404, "not_found", `No resolver is registered for kind "${kind}".`);
    const result = { ...(await resolver.resolve({ ...body, kind } as unknown as KindResolveRequest, ctx)) };
    result.sources = result.sources ?? [];
    return ok(result);
  }
  if (tail.length === 1 && tail[0] === "match") {
    const matcher = handlers.match.get(kind);
    if (!matcher) return err(404, "not_found", `No matcher is registered for kind "${kind}".`);
    const result = { ...(await matcher.match({ ...body, kind } as unknown as KindMatchRequest, ctx)) };
    result.matches = result.matches ?? [];
    return ok(result);
  }
  return err(404, "not_found", "No kind route matched.");
}
