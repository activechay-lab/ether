import { API_BASE, ApiError, apiFetch, sessionXhrSetup } from "../../lib/api";
import { t } from "../../i18n";

export type DeviceKind = "tv" | "mobile" | "desktop" | "web";
type PrerollKind = "movie" | "episode";
export type AudienceMode = "everyone" | "only" | "except";
export type NetworkMode = "any" | "home" | "away";
export type FrequencyMode = "always" | "daily" | "hours" | "title";
export type PrerollOrder = "before_trailers" | "after_trailers";
export type PrerollRules = {
  kinds: PrerollKind[];
  firstEpisodeOnly: boolean;
  bingeGapMinutes: number;
  genres: string[];
  excludeGenres: string[];
  collections: string[];
  maxContentRating: string;
  /** "MM-DD"; empty plays all year. */
  dateFrom: string;
  dateTo: string;
  audience: { mode: AudienceMode; viewers: number[] };
  network: NetworkMode;
  devices: DeviceKind[];
};
export type PrerollInput = {
  name: string;
  enabled: boolean;
  rules: PrerollRules;
  frequency: { mode: FrequencyMode; hours: number };
  priority: number;
  order: PrerollOrder;
};
export type VideoProblem = "" | "unreadable" | "too_long" | "convert_failed";
export type Preroll = PrerollInput & {
  id: string;
  video: {
    /** "" until a video is added. */
    state: "" | "processing" | "ready" | "failed";
    durationMs: number;
    width: number;
    height: number;
    thumbUrl: string;
    previewUrl: string;
    problem: VideoProblem;
    source: "upload" | "path";
  };
  plays30d: number;
  createdAt: string;
  updatedAt: string;
};
export type Selection = "priority" | "shuffle";
type Reason = { code: string; ok: boolean };
export type Candidate = { id: string; name: string; plays: boolean; reasons: Reason[] };
export type Simulation = { winner: { id: string; name: string } | null; candidates: Candidate[] };
type SimulationRequest = {
  viewerId: number;
  ratingKey: string;
  device: DeviceKind;
  away: boolean;
  resume: boolean;
  at?: string;
};
export type Play = {
  viewerId: number;
  viewerName: string;
  title: string;
  ratingKey: string;
  at: string;
  client: string;
};

const root = "/admin/prerolls";
const json = (method: string, body: unknown): RequestInit => ({
  method,
  body: JSON.stringify(body),
});
const item = (id: string) => `${root}/${encodeURIComponent(id)}`;

/**
 * The browser URL for a server-issued media path. The console API lives
 * under /ether/api; a path written relative to the server root
 * ("/api/admin/…") is mapped there.
 */
export function mediaUrl(path: string): string {
  if (!path) return "";
  return path.startsWith("/api/") ? `/ether${path}` : path;
}

/** Uploads one file and reports progress (0–100). */
type UploadTransport = (
  url: string,
  form: FormData,
  onProgress: (percent: number) => void,
  signal?: AbortSignal,
) => Promise<Preroll>;

function failure(status: number, text: string): ApiError {
  let err: { code?: string; message?: string } | undefined;
  try {
    err = (JSON.parse(text) as { error?: { code?: string; message?: string } }).error;
  } catch {
    err = undefined;
  }
  return new ApiError(
    status,
    err?.code ?? "http",
    err?.message ?? t("Request failed ({status})", { status }),
  );
}

/** XHR, because fetch() can't report upload progress. */
const xhrUpload: UploadTransport = (url, form, onProgress, signal) =>
  new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", url);
    xhr.setRequestHeader("X-Ether-Request", "1");
    sessionXhrSetup(xhr, url);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(Math.round((e.loaded / e.total) * 100));
    };
    xhr.onerror = () => reject(new ApiError(0, "network", t("The upload was interrupted.")));
    xhr.onabort = () => reject(new DOMException("Aborted", "AbortError"));
    xhr.onload = () => {
      if (xhr.status < 200 || xhr.status >= 300) reject(failure(xhr.status, xhr.responseText));
      else resolve(JSON.parse(xhr.responseText) as Preroll);
    };
    signal?.addEventListener("abort", () => xhr.abort());
    xhr.send(form);
  });

/** fetch() transport for the in-browser mock and tests, which stub fetch. */
export const fetchUpload: UploadTransport = async (url, form, onProgress, signal) => {
  const res = await fetch(url, {
    method: "POST",
    body: form,
    headers: { "X-Ether-Request": "1" },
    signal,
  });
  const text = await res.text();
  if (!res.ok) throw failure(res.status, text);
  onProgress(100);
  return JSON.parse(text) as Preroll;
};

let transport: UploadTransport = xhrUpload;

/** Swap the upload transport (mock mode and tests); no argument restores XHR. */
export function setUploadTransport(next?: UploadTransport): void {
  transport = next ?? xhrUpload;
}

export const prerollsApi = {
  list: () => apiFetch<{ prerolls: Preroll[] | null; selection: Selection }>(root),
  selection: (selection: Selection) =>
    apiFetch<void>(`${root}/selection`, json("PUT", { selection })),
  create: (data: PrerollInput) => apiFetch<Preroll>(root, json("POST", data)),
  update: (id: string, data: PrerollInput) => apiFetch<Preroll>(item(id), json("PUT", data)),
  remove: (id: string) => apiFetch<void>(item(id), { method: "DELETE" }),
  path: (id: string, path: string) =>
    apiFetch<Preroll>(`${item(id)}/video`, json("POST", { path })),
  upload: (id: string, file: File, onProgress: (percent: number) => void, signal?: AbortSignal) => {
    const form = new FormData();
    form.append("file", file);
    return transport(`${API_BASE}${item(id)}/video`, form, onProgress, signal);
  },
  plays: (id: string, signal?: AbortSignal) =>
    apiFetch<{ plays: Play[] | null }>(`${item(id)}/plays?limit=50`, { signal }),
  simulate: (data: SimulationRequest) =>
    apiFetch<Simulation>(`${root}/simulate`, json("POST", data)),
  importPms: (paths: string) =>
    apiFetch<{ created: Preroll[] | null }>(`${root}/import/pms`, json("POST", { paths })),
};

/** Only what the create and update endpoints accept. */
export function prerollInput(p: PrerollInput): PrerollInput {
  return {
    name: p.name.trim(),
    enabled: p.enabled,
    rules: p.rules,
    frequency: p.frequency,
    priority: p.priority,
    order: p.order,
  };
}
