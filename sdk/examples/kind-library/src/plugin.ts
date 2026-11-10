// Field Recordings — the content-kind proof example (spec §13).
// Deliberately generic fixture content, not a shipped feature.
//
// One kind, three levels (collection → album → track). Albums are
// containers: each track is one member of the album's zip, so resolve
// returns the container pattern first and a {pageNumber} URL template
// second. Pull sync with /changes, live search, a title matcher, one
// setup step, and a tiny OPDS-ish frontend adapter.
type Request = {
  method: string;
  path: string;
  query?: Record<string, string[] | string>;
  headers?: Record<string, string>;
  body?: unknown;
  settings?: Record<string, unknown>;
};
type Item = Record<string, any>;

function baseUrl(req: Request): string {
  let v = req.settings?.sourceUrl;
  if (v === undefined && typeof ether !== "undefined") {
    try { v = ether.settings.get().sourceUrl; } catch { v = undefined; }
  }
  return (typeof v === "string" && v.trim() ? v.trim().replace(/\/+$/, "") : "https://media.example");
}

function loadItems(req: Request): Item[] {
  const base = baseUrl(req);
  return [
    { id: "coast", level: "collection", title: "Coastal", childCount: 1, leafCount: 2 },
    {
      id: "harbour", level: "album", parentId: "coast", index: 0, title: "Harbour at Dawn",
      year: 2024, childCount: 2, leafCount: 2, fields: { genre: "ambient" },
      hints: { zipUrl: base + "/harbour.zip", zipSize: 4096 }, etag: "a1",
    },
    {
      id: "harbour-1", level: "track", parentId: "harbour", index: 0, title: "Gulls",
      playable: true, fields: { genre: "ambient", durationSec: 61 },
      hints: { member: "01-gulls.flac" }, etag: "t1",
    },
    {
      id: "harbour-2", level: "track", parentId: "harbour", index: 1, title: "Fog Horn",
      playable: true, fields: { genre: "ambient", durationSec: 84 },
      hints: { member: "02-fog-horn.flac", pageUrl: base + "/tracks/{pageNumber}.flac" }, etag: "t2",
    },
  ];
}

function bodyOf(req: Request): any {
  if (typeof req.body === "string") {
    try { return JSON.parse(req.body || "{}"); } catch { return {}; }
  }
  return req.body || {};
}

function pageOf(items: Item[], body: any) {
  const limit = Number(body.limit || 200);
  const off = Number(body.cursor || 0);
  const sorted = [...items].sort((a, b) => (a.index || 0) - (b.index || 0));
  const page: any = { items: sorted.slice(off, off + limit) };
  if (off + limit < sorted.length) page.nextCursor = String(off + limit);
  return { status: 200, body: page };
}

const byId = (items: Item[], id: string) => items.find((i) => i.id === id);

function candidateRef(c: any): string {
  if (c.infoHash) return "infohash:" + String(c.infoHash).toLowerCase();
  if (c.magnet) return "magnet:" + c.magnet;
  if (c.nzbUrl) return "nzb:" + c.nzbUrl;
  return String(c.title || "");
}

export function handle(req: Request) {
  const body = bodyOf(req);
  const items = loadItems(req);
  if (req.path === "/ether/configure") return { status: 200, body: { errors: {} } };
  if (req.path === "/ether/health") return { status: 200, body: { status: "ok", reason: "" } };

  if (req.path === "/ether/v1/setup/account/test") {
    const fields = body.fields || {};
    if (!fields.sourceUrl && !fields.apiKey) {
      return { status: 200, body: { ok: true, detail: "$t:missing_url" } };
    }
    return { status: 200, body: { ok: true, detail: "$t:connected" } };
  }

  if (req.path.startsWith("/ether/v1/frontend/feed")) {
    const entries = items
      .filter((i) => i.playable)
      .map((i) =>
        `<entry><id>${i.id}</id><title>${i.title}</title>` +
        `<link rel="http://opds-spec.org/acquisition" href="/ether/v1/kind-media/${encodeURIComponent(i.id)}" type="audio/flac"/></entry>`)
      .join("");
    return {
      status: 200,
      body: `<?xml version="1.0" encoding="utf-8"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Field Recordings</title>${entries}</feed>`,
    };
  }

  if (req.path === "/ether/v1/kinds/recordings/browse") {
    return pageOf(items.filter((i) => i.level === body.level && (i.parentId || "") === (body.parentId || "")), body);
  }
  if (req.path === "/ether/v1/kinds/recordings/search") {
    const q = String(body.query || "").toLowerCase();
    return pageOf(items.filter((i) => String(i.title).toLowerCase().includes(q)), body);
  }
  if (req.path === "/ether/v1/kinds/recordings/changes") {
    return { status: 200, body: { upserts: items, complete: true } };
  }
  if (req.path.startsWith("/ether/v1/kinds/recordings/items/")) {
    const item = byId(items, decodeURIComponent(req.path.split("/").pop() || ""));
    if (!item) return { status: 404, body: { error: { code: "not_found", message: "Item not found." } } };
    return { status: 200, body: item };
  }
  if (req.path === "/ether/v1/kinds/recordings/resolve") {
    const item = body.item || {};
    const hints = item.hints || {};
    const parent = byId(items, item.parentId || "");
    const sources: any[] = [];
    if (parent && hints.member) {
      sources.push({
        id: "zip", pattern: "container", mime: "audio/flac",
        container: { format: "zip", volumes: [{ url: parent.hints.zipUrl, size: parent.hints.zipSize }], member: hints.member },
      });
    }
    if (hints.pageUrl) sources.push({ id: "direct", pattern: "range", mime: "audio/flac", url: hints.pageUrl });
    return { status: 200, body: { sources } };
  }
  if (req.path === "/ether/v1/kinds/recordings/match") {
    const title = String(body.item?.title || "").toLowerCase();
    const matches = (body.candidates || []).map((c: any) => {
      const hit = String(c.title || "").toLowerCase().includes(title);
      return { candidateRef: candidateRef(c), score: hit ? 0.95 : 0.1, reasons: [hit ? "Title matches" : "Title does not match"] };
    }).sort((a: any, b: any) => b.score - a.score);
    return { status: 200, body: { matches } };
  }
  if (req.path === "/ether/view/library") {
    return {
      status: 200,
      body: {
        title: "$t:page_label",
        spec: {
          specVersion: 1,
          layout: "detail",
          header: [{ kind: "page-header", title: "$t:page_label", description: "$t:summary" }],
          body: [
            { kind: "stat-tile", iconKey: "music", accent: "primary", label: "$t:items_synced", value: { $: "count" } },
          ],
        },
        data: { count: String(items.length) },
      },
    };
  }
  return { status: 404, body: { error: { code: "not_found", message: "Route not found." } } };
}

(globalThis as any).handle = handle;
