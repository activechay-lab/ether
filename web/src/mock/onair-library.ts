import specimenFont from "../../../internal/onair/fonts/data/fraunces.ttf?url";
import clipPreview from "./clip-preview.mp4?url";
import type { LibraryAsset } from "../pages/livetv/onair/studio-model";
const decorate = (a: LibraryAsset) => ({
  ...a,
  url:
    a.kind === "video" || a.kind === "audio"
      ? clipPreview
      : a.kind === "image"
        ? "data:image/svg+xml," +
          encodeURIComponent(
            '<svg xmlns="http://www.w3.org/2000/svg" width="480" height="270"><rect width="480" height="270" fill="#132c44"/><circle cx="240" cy="120" r="65" fill="#55ddce"/><text x="240" y="232" text-anchor="middle" fill="white" font-size="28">MERIDIAN</text></svg>',
          )
        : a.kind === "font"
          ? specimenFont
          : undefined,
  thumbnail:
    a.kind === "image"
      ? "data:image/svg+xml," +
        encodeURIComponent(
          '<svg xmlns="http://www.w3.org/2000/svg" width="480" height="270"><rect width="480" height="270" fill="#132c44"/><circle cx="240" cy="110" r="65" fill="#55ddce"/><text x="240" y="235" text-anchor="middle" fill="white" font-size="28">MERIDIAN</text></svg>',
        )
      : undefined,
});
let serial = 0;
let assets: LibraryAsset[] = [
  {
    digest: "library-logo",
    name: "Meridian station mark",
    kind: "image",
    size: 18400,
    mime: "image/png",
    createdAt: 1793800000,
    usedBy: [{ kind: "style", id: "meridian", name: "Meridian" }],
  },
  {
    digest: "library-bumper",
    name: "Midnight station bumper",
    kind: "video",
    size: 2480000,
    mime: "video/mp4",
    durationMs: 15000,
    width: 1920,
    height: 1080,
    createdAt: 1793800200,
    usedBy: [],
  },
  {
    digest: "library-sting",
    name: "After hours sting",
    kind: "audio",
    size: 284000,
    mime: "audio/mp4",
    durationMs: 4800,
    createdAt: 1793800300,
    usedBy: [],
  },
  {
    digest: "library-font",
    name: "Station display",
    kind: "font",
    size: 92000,
    mime: "font/ttf",
    license: "OFL",
    createdAt: 1793800400,
    usedBy: [],
  },
];
export function libraryMock(
  method: string,
  seg: string[],
  body: unknown,
): { body: unknown; status?: number } | null {
  if (seg[0] !== "onair" || seg[1] !== "assets") return null;
  const id = seg[2],
    action = seg[3];
  let a = assets.find((x) => x.digest === id);
  if (method === "GET")
    return {
      body:
        action === "waveform"
          ? {
              peaks: Array.from(
                { length: 1000 },
                (_, i) => Math.abs(Math.sin(i * 0.17) * Math.cos(i * 0.031)) * 0.8,
              ),
              durationMs: a?.durationMs,
            }
          : id
            ? a && decorate(a)
            : assets.map(decorate),
    };
  if (method === "DELETE") {
    if (a?.usedBy.length) return { body: { error: "Asset is in use" }, status: 409 };
    assets = assets.filter((x) => x.digest !== id);
    return { body: null };
  }
  if (method === "PATCH" && a) {
    Object.assign(a, body);
    return { body: a };
  }
  if (method === "POST") {
    if (action === "trim" && a) {
      a = {
        ...a,
        digest: `library-${++serial}`,
        name: (body as { name: string }).name,
        usedBy: [],
      };
      assets.push(a);
      return { body: a };
    }
    const data = body instanceof FormData ? body : null;
    const file = data?.get("file") as File | null;
    if (action === "replace" && a) {
      assets = assets.filter((x) => x.digest !== id);
      a = { ...a, digest: `library-${++serial}`, name: file?.name ?? a.name };
      assets.push(a);
      return { body: a };
    }
    const kind = (data?.get("kind") ?? "video") as LibraryAsset["kind"];
    a = {
      digest: `library-${++serial}`,
      name: file?.name ?? "Imported media",
      kind,
      size: file?.size ?? 125000,
      mime: file?.type ?? "video/mp4",
      createdAt: Date.now() / 1000,
      usedBy: [],
      license: String(data?.get("license") ?? ""),
    };
    assets.push(a);
    return { body: a };
  }
  return null;
}
