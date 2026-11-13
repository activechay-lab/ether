import type {
  ActionResponse,
  CandidateFacts,
  CatalogItem,
  CatalogResponse,
  ConfigureResponse,
  EtherEvent,
  EventResponse,
  FieldOption,
  Manifest,
  MaybePromise,
  MarkersResponse,
  MetaItem,
  MetaResponse,
  RawRequest,
  RawResponse,
  StreamResponse,
  SubtitlesResponse,
  TestResponse,
  ToolResponse,
  ViewResponse,
  ViewSpecDocument,
} from "./types.ts";
import type {
  CatalogTitleSummary,
  EtherHost,
  FolderEntry,
  HostFetchRequest,
  HostFetchResponse,
  ViewerWatchEntry,
} from "./host.ts";
import { plugin, type PluginDefinition, type PluginInstance } from "./router.ts";

export interface RecordedLog {
  level: string;
  message: string;
  attrs?: Record<string, unknown>;
}

export interface MockHostState {
  kv: Map<string, string>;
  secrets: Map<string, string>;
  catalog: Map<string, CatalogTitleSummary>;
  history: Map<number, ViewerWatchEntry[]>;
  watchState: Map<string, boolean>;
  collections: Record<string, unknown>[];
  kinds: Map<string, import("./types.ts").KindItem[]>;
  sources: import("./types.ts").SourceCandidate[];
  blobs: Map<string, Uint8Array>;
  folders: Map<string, FolderEntry[]>;
  notifications: string[];
  logs: RecordedLog[];
  httpHandler: (req: HostFetchRequest) => HostFetchResponse | Promise<HostFetchResponse>;
  /** The most recent frontend.streamUrl call: [viewerId, itemRef]. */
  lastStreamUrl?: [number, string];
}

export interface MockHostClient extends EtherHost {
  readonly state: MockHostState;
}

export function createMockHostClient(initial?: Partial<MockHostState>): MockHostClient {
  const state: MockHostState = {
    kv: initial?.kv ?? new Map<string, string>(),
    secrets: initial?.secrets ?? new Map<string, string>(),
    catalog: initial?.catalog ?? new Map<string, CatalogTitleSummary>(),
    history: initial?.history ?? new Map<number, ViewerWatchEntry[]>(),
    watchState: initial?.watchState ?? new Map<string, boolean>(),
    collections: initial?.collections ?? [],
    kinds: initial?.kinds ?? new Map(),
    sources: initial?.sources ?? [],
    blobs: initial?.blobs ?? new Map(),
    folders: initial?.folders ?? new Map<string, FolderEntry[]>(),
    notifications: initial?.notifications ?? [],
    logs: initial?.logs ?? [],
    httpHandler:
      initial?.httpHandler ??
      (() => ({
        status: 200,
        headers: { "Content-Type": "application/json" },
        body: "{}",
      })),
  };

  return {
    state,
    kv: {
      get: (key: string) => state.kv.get(key) ?? null,
      put: (key: string, value: string) => {
        state.kv.set(key, String(value));
      },
      delete: (key: string) => {
        state.kv.delete(key);
      },
      list: (prefix = "") =>
        Array.from(state.kv.keys())
          .filter((k) => k.startsWith(prefix))
          .sort(),
    },
    secret: {
      get: (name: string) => state.secrets.get(name) ?? "",
    },
    http: {
      fetch: (req: HostFetchRequest) => state.httpHandler(req),
    },
    catalog: {
      search: (query: string, limit = 50, offset = 0) => {
        const needle = query.toLowerCase();
        const matches = Array.from(state.catalog.values()).filter((item) =>
          item.title.toLowerCase().includes(needle),
        );
        return { titles: matches.slice(offset, offset + limit) };
      },
      get: (id: string) => state.catalog.get(id) ?? null,
    },
    viewers: {
      granted: () => Array.from(state.history.keys(), (id) => ({ id, name: String(id) })),
      history: (viewerId: number, limit = 50, offset = 0) => {
        const list = state.history.get(viewerId) ?? [];
        return list.slice(offset, offset + limit);
      },
      setWatchState: (viewerId: number, titleId: string, watched: boolean) => {
        state.watchState.set(`${viewerId}:${titleId}`, watched);
      },
    },
    collections: {
      upsert: (collection: Record<string, unknown>) => {
        state.collections.push(collection);
      },
    },
    folders: {
      hashStart: () => { throw new Error("Set a folders.hashStart mock"); },
      hashPoll: () => { throw new Error("Set a folders.hashPoll mock"); },
      hashCancel: () => { throw new Error("Set a folders.hashCancel mock"); },
      hash: () => { throw new Error("Set a folders.hash mock for file identification tests"); },
      list: (root: string, path = "") => {
        const key = path ? `${root}/${path}` : root;
        return state.folders.get(key) ?? [];
      },
      stat: (root: string, path: string) => {
        const list = state.folders.get(root) ?? [];
        const found = list.find((f) => f.path === path || f.name === path);
        return found ?? { name: path, path, dir: false, size: 0 };
      },
    },
    notify: {
      owner: (text: string) => {
        state.notifications.push(text);
      },
    },
    kinds: {
      upsert: (kind, items) => {
        const kept = new Map((state.kinds.get(kind) ?? []).map((it) => [it.id, it]));
        for (const it of items) kept.set(it.id, it);
        state.kinds.set(kind, [...kept.values()]);
        return items.length;
      },
      delete: (kind, externalIds) => {
        const drop = new Set(externalIds);
        state.kinds.set(kind, (state.kinds.get(kind) ?? []).filter((it) => !drop.has(it.id)));
      },
      items: (kind, cursor = "", limit = 200) => {
        const all = state.kinds.get(kind) ?? [];
        const start = cursor ? all.findIndex((it) => it.id === cursor) + 1 : 0;
        const pageItems = all.slice(start, start + limit);
        const page: import("./types.ts").KindPage = { items: pageItems };
        if (pageItems.length && start + limit < all.length) page.nextCursor = pageItems[pageItems.length - 1]?.id;
        return page;
      },
      resolveSources: () => [...state.sources],
    },
    cache: {
      put: async (_libraryKey, _name, data) => {
        const digest = await globalThis.crypto.subtle.digest("SHA-256", data as BufferSource);
        const hex = [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
        const ref = `blob:${hex}`;
        state.blobs.set(ref, new Uint8Array(data));
        return ref;
      },
      open: (blobRef) => {
        const b = state.blobs.get(blobRef);
        if (!b) throw new Error(`blob ${blobRef} not found`);
        return new Uint8Array(b);
      },
    },
    frontend: {
      streamUrl: (viewerId: number, itemRef: string) => {
        state.lastStreamUrl = [viewerId, itemRef];
        return `/ether/v1/kind-media/mock-${viewerId}`;
      },
    },
    log: (level: string, message: string, attrs?: Record<string, unknown>) => {
      state.logs.push({ level, message, attrs });
    },
    abort: (message = "aborted"): never => {
      throw new Error(
        "__ETHER_FAULT__" + JSON.stringify({ code: "aborted", message: String(message) }),
      );
    },
  };
}

export function assertValidViewSpec(doc: ViewSpecDocument): void {
  if (!doc || typeof doc !== "object") {
    throw new Error("ViewSpecDocument must be an object.");
  }
  if (doc.specVersion !== 1) {
    throw new Error(`ViewSpecDocument specVersion must be 1 (got ${String(doc.specVersion)}).`);
  }
  if (doc.layout !== "list" && doc.layout !== "detail" && doc.layout !== "bare") {
    throw new Error(`ViewSpecDocument layout must be list, detail, or bare (got ${String(doc.layout)}).`);
  }
  if (!Array.isArray(doc.header)) {
    throw new Error("ViewSpecDocument header must be an array.");
  }
  if (!Array.isArray(doc.body)) {
    throw new Error("ViewSpecDocument body must be an array.");
  }
  assertNoClassNameFields(doc, "spec");
}

export const assertValidViewPage = assertValidViewSpec;

function assertNoClassNameFields(value: unknown, path: string): void {
  if (!value || typeof value !== "object") {
    return;
  }
  if (Array.isArray(value)) {
    value.forEach((entry, idx) => assertNoClassNameFields(entry, `${path}[${idx}]`));
    return;
  }
  for (const [k, v] of Object.entries(value)) {
    if (k === "className" || k.endsWith("ClassName")) {
      throw new Error(`ViewSpec forbids ${k} at ${path}.${k}.`);
    }
    assertNoClassNameFields(v, `${path}.${k}`);
  }
}

export const fixtures = {
  pickToken(itemId = "ether:movie:1"): string {
    return `pick-v1:${itemId}:sig-test`;
  },
  movie(overrides?: Partial<MetaItem>): MetaItem {
    return {
      id: "tt0133093",
      type: "movie",
      name: "The Matrix",
      releaseInfo: "1999",
      description: "A computer hacker learns about the true nature of reality.",
      genres: ["Action", "Sci-Fi"],
      imdbRating: "8.7",
      ...overrides,
    };
  },
  series(overrides?: Partial<MetaItem>): MetaItem {
    return {
      id: "tt0903747",
      type: "series",
      name: "Breaking Bad",
      releaseInfo: "2008",
      description: "A chemistry teacher diagnosed with inoperable lung cancer turns to manufacturing drugs.",
      genres: ["Crime", "Drama", "Thriller"],
      imdbRating: "9.5",
      ...overrides,
    };
  },
  catalogItem(overrides?: Partial<CatalogItem>): CatalogItem {
    return {
      id: "tt0133093",
      type: "movie",
      name: "The Matrix",
      releaseInfo: "1999",
      ...overrides,
    };
  },
  playbackEvent(
    name: EtherEvent["name"] = "playback.completed",
    overrides?: Partial<EtherEvent>,
  ): EtherEvent {
    return {
      id: "evt_01",
      name,
      at: "2026-11-02T12:00:00Z",
      viewerId: 1,
      payload: {
        titleId: "tt0133093",
        title: "The Matrix",
        progress: 0.95,
      },
      ...overrides,
    };
  },
  candidate(overrides?: Partial<CandidateFacts>): CandidateFacts {
    return {
      releaseId: "rel_01",
      name: "The.Matrix.1999.2160p.UHD.BluRay.x265",
      resolution: 2160,
      hdr: ["DV", "HDR10"],
      videoCodec: "hevc",
      cached: true,
      verified: true,
      ...overrides,
    };
  },
};

export interface TestHarness<S = Record<string, unknown>> {
  readonly instance: PluginInstance<S>;
  readonly host: MockHostClient;
  request(req: RawRequest): Promise<RawResponse>;
  requestHosted(req: RawRequest): MaybePromise<RawResponse>;
  manifest(): Promise<Manifest>;
  health(): Promise<{ status: number; body: unknown }>;
  configure(settings: Record<string, unknown>): Promise<{ status: number; body: ConfigureResponse }>;
  test(): Promise<{ status: number; body: TestResponse }>;
  options(field: string): Promise<{ status: number; body: FieldOption[] }>;
  catalog(
    type: string,
    id: string,
    extra?: Record<string, string>,
  ): Promise<{ status: number; body: CatalogResponse }>;
  meta(type: string, id: string): Promise<{ status: number; body: MetaResponse }>;
  stream(
    type: string,
    id: string,
    extra?: Record<string, string>,
  ): Promise<{ status: number; body: StreamResponse }>;
  subtitles(
    type: string,
    id: string,
    pickToken?: string,
    extra?: Record<string, string>,
  ): Promise<{ status: number; body: SubtitlesResponse }>;
  markers(type: string, id: string): Promise<{ status: number; body: MarkersResponse }>;
  event(events: EtherEvent[]): Promise<{ status: number; body: EventResponse }>;
  view(page?: string, query?: Record<string, string>): Promise<{ status: number; body: ViewResponse }>;
  action(action: string, input?: Record<string, unknown>): Promise<{ status: number; body: ActionResponse }>;
  tool(name: string, input?: Record<string, unknown>): Promise<{ status: number; body: ToolResponse }>;
  rankKey(candidate: CandidateFacts, config?: S): number;
  condMatch(candidate: CandidateFacts, operator: string, value: unknown): boolean;
  file(path: string, range?: string): Promise<RawResponse>;
}

function encodeExtraPath(extra?: Record<string, string>): string {
  if (!extra || Object.keys(extra).length === 0) {
    return "";
  }
  const params = new URLSearchParams(extra);
  return `/${params.toString()}`;
}

export function createTestHarness<S = Record<string, unknown>>(
  defOrInstance: PluginDefinition<S> | PluginInstance<S>,
  mockHost?: MockHostClient,
): TestHarness<S> {
  const host = mockHost ?? createMockHostClient();
  const instance: PluginInstance<S> =
    "dispatch" in defOrInstance && typeof defOrInstance.dispatch === "function"
      ? defOrInstance
      : plugin<S>({
          ...(defOrInstance as PluginDefinition<S>),
          host: (defOrInstance as PluginDefinition<S>).host ?? host,
        });

  return {
    instance,
    host,
    request: (req) => instance.dispatch(req),
    requestHosted: (req) => instance.handle(req),
    async manifest() {
      const res = await instance.dispatch({ method: "GET", path: "/manifest.json" });
      return res.body as Manifest;
    },
    async health() {
      const res = await instance.dispatch({ method: "GET", path: "/ether/health" });
      return { status: res.status, body: res.body };
    },
    async configure(settings) {
      const res = await instance.dispatch({
        method: "POST",
        path: "/ether/configure",
        body: JSON.stringify(settings),
      });
      return { status: res.status, body: res.body as ConfigureResponse };
    },
    async test() {
      const res = await instance.dispatch({ method: "POST", path: "/ether/test" });
      return { status: res.status, body: res.body as TestResponse };
    },
    async options(field) {
      const res = await instance.dispatch({
        method: "GET",
        path: `/ether/options/${encodeURIComponent(field)}`,
      });
      return { status: res.status, body: res.body as FieldOption[] };
    },
    async catalog(type, id, extra) {
      const suffix = encodeExtraPath(extra);
      const res = await instance.dispatch({
        method: "GET",
        path: `/catalog/${encodeURIComponent(type)}/${encodeURIComponent(id)}${suffix}.json`,
      });
      return { status: res.status, body: res.body as CatalogResponse };
    },
    async meta(type, id) {
      const res = await instance.dispatch({
        method: "GET",
        path: `/meta/${encodeURIComponent(type)}/${encodeURIComponent(id)}.json`,
      });
      return { status: res.status, body: res.body as MetaResponse };
    },
    async stream(type, id, extra) {
      const suffix = encodeExtraPath(extra);
      const res = await instance.dispatch({
        method: "GET",
        path: `/stream/${encodeURIComponent(type)}/${encodeURIComponent(id)}${suffix}.json`,
      });
      return { status: res.status, body: res.body as StreamResponse };
    },
    async subtitles(type, id, pickToken, extra) {
      const suffix = encodeExtraPath(extra);
      const headers: Record<string, string> = {};
      if (pickToken !== undefined) {
        headers["X-Ether-Pick"] = pickToken;
      }
      const res = await instance.dispatch({
        method: "GET",
        path: `/subtitles/${encodeURIComponent(type)}/${encodeURIComponent(id)}${suffix}.json`,
        headers,
      });
      return { status: res.status, body: res.body as SubtitlesResponse };
    },
    async markers(type, id) {
      const res = await instance.dispatch({
        method: "GET",
        path: `/ether/markers/${encodeURIComponent(type)}/${encodeURIComponent(id)}.json`,
      });
      return { status: res.status, body: res.body as MarkersResponse };
    },
    async event(events) {
      const res = await instance.dispatch({
        method: "POST",
        path: "/ether/event",
        body: JSON.stringify({ events }),
      });
      return { status: res.status, body: res.body as EventResponse };
    },
    async view(page = "home", query) {
      const cleanPage = page.replace(/^\/+/, "") || "home";
      const res = await instance.dispatch({
        method: "GET",
        path: `/ether/view/${encodeURIComponent(cleanPage)}`,
        query,
      });
      if (res.status === 200) {
        const viewRes = res.body as ViewResponse;
        assertValidViewSpec(viewRes.spec);
      }
      return { status: res.status, body: res.body as ViewResponse };
    },
    async action(action, input = {}) {
      const res = await instance.dispatch({
        method: "POST",
        path: `/ether/action/${encodeURIComponent(action)}`,
        body: JSON.stringify(input),
      });
      return { status: res.status, body: res.body as ActionResponse };
    },
    async tool(name, input = {}) {
      const res = await instance.dispatch({
        method: "POST",
        path: `/ether/tool/${encodeURIComponent(name)}`,
        body: JSON.stringify(input),
      });
      return { status: res.status, body: res.body as ToolResponse };
    },
    rankKey: (candidate, config) => instance.rank_key(candidate, config),
    condMatch: (candidate, operator, value) => instance.cond_match(candidate, operator, value),
    async file(path, range) {
      const headers: Record<string, string> = {};
      if (range) {
        headers.Range = range;
      }
      return instance.dispatch({
        method: "GET",
        path: `/ether/file/${path.replace(/^\/+/, "")}`,
        headers,
      });
    },
  };
}
