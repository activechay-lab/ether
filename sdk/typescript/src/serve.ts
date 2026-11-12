import type { RawRequest, RawResponse } from "./types.ts";
import type { PluginInstance } from "./router.ts";
import { isHostedRuntime } from "./host.ts";
import {
  clearSavedCredential,
  CredentialError,
  noCredentialMessage,
  resolveCredential,
  tunnelUrlFromBase,
} from "./pairing.ts";

export interface ServeOptions {
  mode?: "http" | "tunnel";
  host?: string;
  port?: number;
  tunnelUrl?: string;
  credential?: string;
  signal?: AbortSignal;
  /** The server's http(s) base (ETHER_URL). Derives the tunnel URL and pairing. */
  url?: string;
  /** A one-time pairing code (ETHER_PAIRING_CODE). */
  code?: string;
  /** Overrides where the pairing credential is saved. */
  stateDir?: string;
}

export interface ServeHandle {
  mode: "http" | "tunnel" | "hosted";
  url?: string;
  port?: number;
  close(): Promise<void>;
}

const TUNNEL_PROTOCOL = "ether-tunnel-v1";
const DEFAULT_MAX_CHUNK_BYTES = 64 * 1024;

function readEnv(name: string): string | undefined {
  const g = globalThis as { process?: { env?: Record<string, string | undefined> } };
  return g.process?.env?.[name];
}

function serializeResponseBody(body: unknown): { bytes: Uint8Array; isBinary: boolean } {
  if (body instanceof Uint8Array) {
    return { bytes: body, isBinary: true };
  }
  if (typeof body === "string") {
    return { bytes: new TextEncoder().encode(body), isBinary: false };
  }
  const json = JSON.stringify(body ?? {});
  return { bytes: new TextEncoder().encode(json), isBinary: false };
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]!);
  }
  return btoa(binary);
}

function base64ToBytes(b64: string): Uint8Array {
  if (!b64) {
    return new Uint8Array(0);
  }
  const binary = atob(b64);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    out[i] = binary.charCodeAt(i);
  }
  return out;
}

function concatBytes(chunks: Uint8Array[]): Uint8Array {
  let total = 0;
  for (const c of chunks) {
    total += c.byteLength;
  }
  const out = new Uint8Array(total);
  let offset = 0;
  for (const c of chunks) {
    out.set(c, offset);
    offset += c.byteLength;
  }
  return out;
}

function toMultiHeaders(headers: Record<string, string> | undefined): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  if (!headers) {
    return out;
  }
  for (const [k, v] of Object.entries(headers)) {
    out[k] = [v];
  }
  return out;
}

export interface TunnelFrame {
  type:
    | "hello"
    | "welcome"
    | "request"
    | "response"
    | "response_start"
    | "response_chunk"
    | "response_end"
    | "cancel"
    | "error";
  id?: string;
  requestId?: string;
  protocol?: string;
  plugin?: string;
  pluginId?: string;
  connectionId?: string;
  features?: string[];
  maxChunkBytes?: number;
  method?: string;
  path?: string;
  query?: Record<string, string[]>;
  headers?: Record<string, string[]>;
  body?: string;
  data?: string;
  status?: number;
  deadline?: string;
  error?: {
    code: string;
    message: string;
  };
}

export async function dispatchOverTunnelFrame<S>(
  instance: PluginInstance<S>,
  frame: TunnelFrame,
  maxChunkBytes = DEFAULT_MAX_CHUNK_BYTES,
): Promise<TunnelFrame[]> {
  if (frame.type !== "request" || !frame.id) {
    return [];
  }
  const decodedBodyBytes = frame.body ? base64ToBytes(frame.body) : new Uint8Array(0);
  const rawBodyText =
    decodedBodyBytes.length > 0 ? new TextDecoder().decode(decodedBodyBytes) : undefined;
  const headers: Record<string, string | string[]> = {};
  if (frame.headers) {
    for (const [k, v] of Object.entries(frame.headers)) {
      headers[k] = v;
    }
  }
  if (frame.requestId && !headers["X-Ether-Request-ID"]) {
    headers["X-Ether-Request-ID"] = frame.requestId;
  }

  const req: RawRequest = {
    method: frame.method ?? "GET",
    path: frame.path ?? "/",
    query: frame.query,
    headers,
    body: rawBodyText,
  };

  let res: RawResponse;
  try {
    res = await instance.dispatch(req);
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    return [
      {
        type: "error",
        id: frame.id,
        error: { code: "internal_error", message },
      },
    ];
  }

  const { bytes, isBinary } = serializeResponseBody(res.body);
  const multiHeaders = toMultiHeaders(res.headers);

  if (isBinary && (req.path || "").startsWith("/ether/file/")) {
    const out: TunnelFrame[] = [
      {
        type: "response_start",
        id: frame.id,
        status: res.status,
        headers: multiHeaders,
      },
    ];
    const chunkLimit = Math.max(1024, maxChunkBytes);
    for (let offset = 0; offset < bytes.length; offset += chunkLimit) {
      const slice = bytes.subarray(offset, Math.min(bytes.length, offset + chunkLimit));
      out.push({
        type: "response_chunk",
        id: frame.id,
        data: bytesToBase64(slice),
      });
    }
    out.push({
      type: "response_end",
      id: frame.id,
    });
    return out;
  }

  return [
    {
      type: "response",
      id: frame.id,
      status: res.status,
      headers: multiHeaders,
      body: bytes.length > 0 ? bytesToBase64(bytes) : undefined,
    },
  ];
}

async function serveHttp<S>(
  instance: PluginInstance<S>,
  options: ServeOptions,
): Promise<ServeHandle> {
  type NodeHttpMod = typeof import("node:http");
  const nodeHttpSpec = "node:" + "http";
  const httpMod = (await import(nodeHttpSpec)) as NodeHttpMod;
  const host = options.host ?? readEnv("ETHER_PLUGIN_HOST") ?? "127.0.0.1";
  const port =
    options.port ??
    (readEnv("ETHER_PLUGIN_PORT") ? Number(readEnv("ETHER_PLUGIN_PORT")) : 0);

  const server = httpMod.createServer(async (incoming, outgoing) => {
    try {
      const chunks: Uint8Array[] = [];
      for await (const chunk of incoming) {
        chunks.push(chunk instanceof Uint8Array ? chunk : new Uint8Array(chunk));
      }
      const merged = concatBytes(chunks);
      const rawBody = merged.byteLength > 0 ? new TextDecoder().decode(merged) : undefined;
      const headers: Record<string, string | string[]> = {};
      for (const [k, v] of Object.entries(incoming.headers)) {
        if (typeof v === "string" || Array.isArray(v)) {
          headers[k] = v;
        }
      }
      const req: RawRequest = {
        method: incoming.method ?? "GET",
        path: incoming.url ?? "/",
        headers,
        body: rawBody,
      };
      const res = await instance.dispatch(req);
      const { bytes } = serializeResponseBody(res.body);
      const outHeaders: Record<string, string> = {
        "Content-Type": "application/json",
        ...(res.headers ?? {}),
        "Content-Length": String(bytes.byteLength),
      };
      outgoing.writeHead(res.status, outHeaders);
      if ((incoming.method ?? "GET").toUpperCase() === "HEAD") {
        outgoing.end();
      } else {
        outgoing.end(bytes);
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      const bodyBytes = new TextEncoder().encode(
        JSON.stringify({ error: { code: "internal_error", message: msg } }),
      );
      outgoing.writeHead(500, {
        "Content-Type": "application/json",
        "Content-Length": String(bodyBytes.byteLength),
      });
      outgoing.end(bodyBytes);
    }
  });

  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, host, () => {
      server.removeListener("error", reject);
      resolve();
    });
  });

  const addr = server.address();
  const actualPort = typeof addr === "object" && addr ? addr.port : port;
  const url = `http://${host}:${actualPort}`;

  const close = async (): Promise<void> => {
    await new Promise<void>((resolve, reject) => {
      server.close((err?: Error) => (err ? reject(err) : resolve()));
    });
  };

  if (options.signal) {
    if (options.signal.aborted) {
      await close();
    } else {
      options.signal.addEventListener("abort", () => {
        void close();
      });
    }
  }

  return {
    mode: "http",
    url,
    port: actualPort,
    close,
  };
}

/**
 * Probes a tunnel URL with a credential. Ether answers 401 before the WebSocket
 * upgrade when the credential is rejected; any other status means the handshake
 * was allowed to proceed. Transient failures are treated as not-rejected so the
 * existing reconnect/backoff still applies.
 */
async function tunnelCredentialRejected(
  tunnelUrl: string,
  credential: string,
): Promise<boolean> {
  const httpUrl = tunnelUrl.replace(/^ws/, "http");
  try {
    const res = await fetch(httpUrl, {
      method: "GET",
      headers: { "X-Ether-Plugin-Credential": credential },
    });
    return res.status === 401;
  } catch {
    return false;
  }
}

async function serveTunnel<S>(
  instance: PluginInstance<S>,
  options: ServeOptions,
): Promise<ServeHandle> {
  let tunnelUrl = options.tunnelUrl ?? readEnv("ETHER_TUNNEL_URL");
  const base = options.url ?? readEnv("ETHER_URL") ?? "";
  if (!tunnelUrl && base) {
    tunnelUrl = tunnelUrlFromBase(base);
  }
  if (!tunnelUrl) {
    throw new Error("Tunnel mode requires tunnelUrl, ETHER_TUNNEL_URL or ETHER_URL.");
  }

  let credential = "";
  let fromFile = false;
  let stateDir = "";
  try {
    const resolved = await resolveCredential(instance.manifest.id, {
      credential: options.credential,
      url: base,
      code: options.code,
      stateDir: options.stateDir,
    });
    credential = resolved.credential;
    fromFile = resolved.fromFile;
    stateDir = resolved.stateDir;
  } catch (err) {
    // A low-level caller may pass only a tunnel URL for testing; the pairing
    // flow always has ETHER_URL, so the clear line still applies to sidecars.
    if (base) {
      throw err;
    }
  }

  // A saved credential the server no longer accepts is dead: clear the file and
  // fail rather than reconnect with it forever. The tunnel returns 401 before
  // the upgrade, so a plain request detects it.
  if (fromFile && credential) {
    const rejected = await tunnelCredentialRejected(tunnelUrl, credential);
    if (rejected) {
      await clearSavedCredential(stateDir);
      throw new CredentialError(noCredentialMessage());
    }
  }

  const WebSocketCtor = (globalThis as unknown as { WebSocket?: typeof WebSocket }).WebSocket;
  if (!WebSocketCtor) {
    throw new Error("Global WebSocket constructor is required for tunnel mode.");
  }

  const protocols = [TUNNEL_PROTOCOL];
  if (credential) {
    protocols.push(`ether-credential.${credential}`);
  }

  const ws = new WebSocketCtor(tunnelUrl, protocols);
  let maxChunkBytes = DEFAULT_MAX_CHUNK_BYTES;
  let closed = false;

  await new Promise<void>((resolve, reject) => {
    const onOpen = () => {
      const hello: TunnelFrame = {
        type: "hello",
        protocol: TUNNEL_PROTOCOL,
        plugin: instance.manifest.id,
        features: ["range", "stream"],
      };
      ws.send(JSON.stringify(hello));
    };

    const onMessage = async (event: MessageEvent) => {
      try {
        const text = typeof event.data === "string" ? event.data : String(event.data);
        const frame = JSON.parse(text) as TunnelFrame;
        if (frame.type === "welcome") {
          if (frame.maxChunkBytes && frame.maxChunkBytes > 0) {
            maxChunkBytes = frame.maxChunkBytes;
          }
          resolve();
          return;
        }
        if (frame.type === "request") {
          const replies = await dispatchOverTunnelFrame(instance, frame, maxChunkBytes);
          for (const reply of replies) {
            if (!closed && ws.readyState === ws.OPEN) {
              ws.send(JSON.stringify(reply));
            }
          }
        }
      } catch (err) {
        const id = extractFrameId(event.data);
        if (id) {
          const msg = err instanceof Error ? err.message : String(err);
          ws.send(
            JSON.stringify({
              type: "error",
              id,
              error: { code: "internal_error", message: msg },
            }),
          );
        }
      }
    };

    const onError = (ev: Event) => {
      reject(new Error(`Tunnel WebSocket error: ${String(ev)}`));
    };

    ws.addEventListener("open", onOpen);
    ws.addEventListener("message", (ev) => {
      void onMessage(ev);
    });
    ws.addEventListener("error", onError, { once: true });
  });

  const close = async (): Promise<void> => {
    if (closed) {
      return;
    }
    closed = true;
    if (ws.readyState === ws.OPEN || ws.readyState === ws.CONNECTING) {
      ws.close(1000, "Plugin shutting down");
    }
  };

  if (options.signal) {
    if (options.signal.aborted) {
      await close();
    } else {
      options.signal.addEventListener("abort", () => {
        void close();
      });
    }
  }

  return {
    mode: "tunnel",
    url: tunnelUrl,
    close,
  };
}

function extractFrameId(raw: unknown): string | undefined {
  try {
    const parsed = JSON.parse(typeof raw === "string" ? raw : String(raw)) as { id?: string };
    return parsed.id;
  } catch {
    return undefined;
  }
}

export async function serve<S>(
  instance: PluginInstance<S>,
  options: ServeOptions = {},
): Promise<ServeHandle> {
  if (isHostedRuntime()) {
    const g = globalThis as Record<string, unknown>;
    g.handle = instance.handle;
    g.rank_key = instance.rank_key;
    g.cond_match = instance.cond_match;
    return {
      mode: "hosted",
      close: async () => {},
    };
  }

  const requestedMode =
    options.mode ??
    (options.tunnelUrl || readEnv("ETHER_TUNNEL_URL") || readEnv("ETHER_URL")
      ? "tunnel"
      : "http");
  if (requestedMode === "tunnel") {
    return serveTunnel(instance, options);
  }
  return serveHttp(instance, options);
}
