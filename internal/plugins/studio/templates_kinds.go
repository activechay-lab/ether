package studio

import "github.com/braedonsaunders/ether/internal/plugins"

// The content-kind template: a whole new library (collection → album →
// track here) served by a catalogue provider, resolved through the
// container pattern, matched by title, with one guided setup step and
// live search. The AI builder reshapes the kind declaration from the
// owner's description; this is the working skeleton it starts from.

func templateContentKind() Template {
	manifest := map[string]any{
		"id":          "community.studio.library",
		"version":     "0.1.0",
		"name":        "Field recordings library",
		"description": "A library of field recordings, grouped into albums of playable tracks.",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"capabilities": []string{"catalog_provider", "kind_resolver", "matcher", "ui"},
			"summary":      "$t:summary",
			"author":       map[string]any{"name": "Server owner"},
			"license":      "MIT",
			"runtime":      map[string]any{"kind": "hosted", "entry": "src/plugin.ts"},
			"permissions": map[string]any{
				"network": []string{"media.example"},
				"sources": "search",
				"secrets": []string{"apiKey"},
			},
			"settings": map[string]any{"$ref": "settings.schema.json"},
			"strings":  map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{
				"mediaKinds": []map[string]any{
					{
						"key":     "recordings",
						"label":   "$t:kind_label",
						"library": map[string]any{"name": "Field Recordings"},
						"levels": []map[string]any{
							{"key": "collection", "label": "Collection"},
							{"key": "album", "label": "Album", "container": true},
							{"key": "track", "label": "Track", "playable": true},
						},
						"fields": []map[string]any{
							{"key": "genre", "type": "string", "label": "Genre", "indexed": true},
							{"key": "durationSec", "type": "duration", "label": "Duration"},
						},
						"present": map[string]any{
							"plex":         map[string]any{"base": "show"},
							"mediaBrowser": map[string]any{"collectionType": "music", "itemTypes": map[string]string{"album": "MusicAlbum", "track": "Audio"}},
						},
						"browse": map[string]any{
							"sorts":  []string{"title", "index"},
							"facets": []string{"genre"},
							"search": true,
							// Live search: client searches also ask this
							// plugin, with a short budget, and Ether keeps
							// what it finds. Turn it off when the source
							// is slow or has no real search.
							"liveSearch": true,
						},
						"sync":  map[string]any{"mode": "pull", "refresh": "1h"},
						"match": map[string]any{"thresholdDefault": 0.8},
					},
				},
				"setup": map[string]any{
					"steps": []map[string]any{
						{
							"key":      "account",
							"label":    "$t:setup_label",
							"hint":     "$t:setup_hint",
							"fields":   []string{"sourceUrl", "apiKey"},
							"test":     "/ether/v1/setup/account/test",
							"required": true,
						},
					},
				},
				"pages": []map[string]any{
					{"key": "library", "label": "$t:page_label", "route": "/ether/view/library"},
				},
			},
		},
	}
	settings := map[string]any{
		"fields": []map[string]any{
			{
				"key":     "sourceUrl",
				"kind":    "url",
				"label":   "$t:source_url",
				"hint":    "$t:source_url_hint",
				"default": "",
			},
			{
				"key":   "apiKey",
				"kind":  "secret",
				"label": "$t:api_key",
				"hint":  "$t:api_key_hint",
			},
		},
	}
	stringsEN := map[string]string{
		"summary":         "A library of field recordings in every app, grouped into albums of tracks.",
		"kind_label":      "Field recordings",
		"page_label":      "Library",
		"setup_label":     "Connect the source",
		"setup_hint":      "Where the recordings live, and the key if it needs one",
		"source_url":      "Source address",
		"source_url_hint": "The service that lists the recordings; leave empty to use the built-in sample set",
		"api_key":         "API key",
		"api_key_hint":    "Sent with each request to the source; leave empty for an open source",
		"connected":       "Connected to the source.",
		"missing_url":     "Using the sample set until a source address is saved.",
		"items_synced":    "Items in the library",
		"live_search":     "Live search",
	}
	code := `// A content-kind plugin: this library appears beside Movies and TV
// Shows in every app. Ether syncs the items below into its own store
// (browse/changes), asks live on client searches (search, because the
// manifest sets browse.liveSearch), and calls resolve only when a
// viewer presses play.
type Request = {
  method: string;
  path: string;
  query?: Record<string, string[] | string>;
  headers?: Record<string, string>;
  body?: unknown;
  settings?: Record<string, unknown>;
};
type Item = Record<string, any>;

// Sample catalogue so the preview works before a source is connected.
// Swap loadItems() for a fetch of your source when ready.
const SAMPLE: Item[] = [
  { id: "coast", level: "collection", title: "Coastal", childCount: 1, leafCount: 2 },
  { id: "harbour", level: "album", parentId: "coast", index: 0, title: "Harbour at Dawn", year: 2024, childCount: 2, leafCount: 2, fields: { genre: "ambient" }, hints: { zipUrl: "https://media.example/harbour.zip", zipSize: 4096 }, etag: "a1" },
  { id: "harbour-1", level: "track", parentId: "harbour", index: 0, title: "Gulls", playable: true, fields: { genre: "ambient", durationSec: 61 }, hints: { member: "01-gulls.flac" }, etag: "t1" },
  { id: "harbour-2", level: "track", parentId: "harbour", index: 1, title: "Fog Horn", playable: true, fields: { genre: "ambient", durationSec: 84 }, hints: { member: "02-fog-horn.flac", pageUrl: "https://media.example/tracks/{pageNumber}.flac" }, etag: "t2" },
];

function loadItems(): Item[] {
  return SAMPLE;
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

export function handle(req: Request) {
  const body = bodyOf(req);
  const items = loadItems();
  if (req.path === "/ether/configure") return { status: 200, body: { errors: {} } };
  if (req.path === "/ether/health") return { status: 200, body: { status: "ok", reason: "" } };
  if (req.path === "/ether/v1/setup/account/test") {
    const fields = body.fields || {};
    if (!fields.sourceUrl && !fields.apiKey) {
      return { status: 200, body: { ok: true, detail: "$t:missing_url" } };
    }
    return { status: 200, body: { ok: true, detail: "$t:connected" } };
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
      const ref = c.infoHash ? "infohash:" + String(c.infoHash).toLowerCase() : c.magnet ? "magnet:" + c.magnet : c.nzbUrl ? "nzb:" + c.nzbUrl : String(c.title || "");
      return { candidateRef: ref, score: hit ? 0.95 : 0.1, reasons: [hit ? "Title matches" : "Title does not match"] };
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
            { kind: "panel", title: "$t:live_search", blocks: [{ kind: "text", content: { $: "liveSearch" } }] },
          ],
        },
        data: { count: String(items.length), liveSearch: "On — searches in apps also ask this plugin, and Ether keeps what it finds." },
      },
    };
  }
  return { status: 404, body: { error: { code: "not_found", message: "Route not found." } } };
}

(globalThis as any).handle = handle;
`
	return Template{
		ID:              "content_kind",
		Label:           "A new library",
		Summary:         "Add a whole library of your own content — books, audio, photos, anything — beside Movies and TV Shows.",
		Kind:            plugins.KindContent,
		Capabilities:    []plugins.Capability{plugins.CapCatalogProvider, plugins.CapKindResolver, plugins.CapMatcher, plugins.CapUI},
		DefaultPluginID: "community.studio.library",
		DefaultName:     "Field recordings library",
		DefaultRoute:    "/ether/view/library",
		DefaultPage:     "library",
		Files: map[string]string{
			"manifest.json":        prettyJSON(manifest),
			"settings.schema.json": prettyJSON(settings),
			"i18n/en.json":         prettyJSON(stringsEN),
			"src/plugin.ts":        code,
		},
	}
}
