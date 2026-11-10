// Package studio implements Ether's in-console plugin development environment
// (SPEC §3.1, WS11a, WS11b, WS11d): templates, project file storage, TypeScript
// compilation via esbuild, conformance and copy checks, sandboxed dev execution
// with shadow mode and undo journalling, live ViewSpec preview, inspector call
// history with plain-language validation hints, hot reload over /api/stream,
// and direct installation through the plugin host's permission sheet.
package studio

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/braedonsaunders/ether/internal/plugins"
)

// Template describes a starter plugin template in the Develop tab (WS11a).
type Template struct {
	ID              string               `json:"id"`
	Label           string               `json:"label"`
	Summary         string               `json:"summary"`
	Kind            plugins.Kind         `json:"kind"`
	Capabilities    []plugins.Capability `json:"capabilities"`
	DefaultPluginID string               `json:"defaultPluginId"`
	DefaultName     string               `json:"defaultName"`
	DefaultRoute    string               `json:"defaultRoute"`
	DefaultPage     string               `json:"defaultPage,omitempty"`
	SupportsStremio bool                 `json:"supportsStremio,omitempty"`
	SupportsSlot    bool                 `json:"supportsSlot,omitempty"`
	Files           map[string]string    `json:"files"`
}

// CreateProjectOptions customizes a project scaffolded from a template.
type CreateProjectOptions struct {
	Template   string `json:"template"`
	PluginID   string `json:"pluginId,omitempty"`
	Name       string `json:"name,omitempty"`
	Slot       string `json:"slot,omitempty"`
	StremioURL string `json:"stremioUrl,omitempty"`
	// LiveSearch, when set, writes browse.liveSearch on every declared
	// media kind: client searches also ask the plugin live (§15.4).
	LiveSearch *bool `json:"liveSearch,omitempty"`
}

// Templates returns Studio's starter templates in display order, native surfaces first.
func Templates() []Template {
	return []Template{
		templateContentKind(),
		templateChannels(),
		templateVideoSource(),
		templateStremioRemix(),
		templateLocalFiles(),
		templateList(),
		templateNotification(),
		templateRanking(),
		templatePage(),
		templateTool(),
	}
}

// LookupTemplate finds a template by ID.
func LookupTemplate(id string) (Template, bool) {
	for _, t := range Templates() {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
}

func prettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b) + "\n"
}

func templateStremioRemix() Template {
	manifest := map[string]any{
		"id":          "community.studio.remix",
		"version":     "0.1.0",
		"name":        "Stremio remix",
		"description": "Adds settings, channel numbers and a console page to a Stremio add-on.",
		"types":       []string{"movie", "series"},
		"resources":   []string{"catalog", "stream"},
		"catalogs": []map[string]any{
			{"type": "movie", "id": "remix_top", "name": "Remixed shelf"},
		},
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "extension",
			"capabilities": []string{"list_source", "ui"},
			"summary":      "$t:summary",
			"author":       map[string]any{"name": "Server owner"},
			"license":      "MIT",
			"runtime":      map[string]any{"kind": "hosted", "entry": "src/plugin.ts"},
			"permissions": map[string]any{
				"network": []string{"v3-cinemeta.strem.io"},
				"catalog": "read",
			},
			"settings": map[string]any{"$ref": "settings.schema.json"},
			"strings":  map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{
				"listSources": []map[string]any{
					{
						"key":      "remix_top",
						"label":    "$t:list_label",
						"hint":     "$t:list_hint",
						"catalog":  "remix_top",
						"maxItems": 100,
						"refresh":  "6h",
					},
				},
				"pages": []map[string]any{
					{
						"key":   "remix",
						"label": "$t:page_label",
						"route": "/ether/view/remix",
					},
				},
			},
		},
	}
	settings := map[string]any{
		"fields": []map[string]any{
			{
				"key":   "upstreamUrl",
				"kind":  "url",
				"label": "Upstream manifest link",
				"hint":  "Paste the Stremio add-on manifest link this remix wraps.",
			},
			{
				"key":   "region",
				"kind":  "select",
				"label": "Region filter",
				"hint":  "Keep items matching your preferred region.",
			},
		},
	}
	stringsEN := map[string]string{
		"summary":        "Wraps a Stremio add-on with settings and a native status page.",
		"list_label":     "Remixed catalogue shelf",
		"list_hint":      "Curated titles pulled and filtered from the upstream add-on.",
		"page_label":     "Remix overview",
		"matched_titles": "Matched titles",
		"remixed_items":  "Remixed items",
	}
	code := `// Stremio remix plugin: wraps an upstream Stremio add-on with settings and a page.
type Request = {
  method: string;
  path: string;
  query?: Record<string, string[]>;
  headers?: Record<string, string>;
  body?: unknown;
};

const UPSTREAM_ITEMS = [
  { id: "tt0133093", type: "movie", name: "The Matrix", year: 1999 },
  { id: "tt0816692", type: "movie", name: "Interstellar", year: 2014 },
  { id: "tt0111161", type: "movie", name: "The Shawshank Redemption", year: 1994 },
];

export function handle(req: Request) {
  if (req.path === "/ether/health") {
    return { status: 200, body: { status: "ok", reason: "" } };
  }
  if (req.path === "/ether/test") {
    return {
      status: 200,
      body: {
        ok: true,
        message: "Upstream Stremio catalog responded.",
        details: ["Matched 3 catalogue items with IMDb ids."],
      },
    };
  }
  if (req.path === "/ether/configure") {
    return { status: 200, body: { errors: {} } };
  }
  if (req.path.startsWith("/catalog/")) {
    return { status: 200, body: { metas: UPSTREAM_ITEMS } };
  }
  if (req.path === "/ether/view/remix") {
    return {
      status: 200,
      body: {
        title: "Remix overview",
        spec: {
          specVersion: 1,
          layout: "detail",
          header: [
            {
              kind: "page-header",
              title: { $: "title" },
              description: { $: "subtitle" },
            },
          ],
          body: [
            {
              kind: "stat-tile",
              iconKey: "layers",
              accent: "primary",
              label: "$t:matched_titles",
              value: { $: "matchedCount" },
            },
            {
              kind: "panel",
              title: "$t:remixed_items",
              blocks: [
                {
                  kind: "text",
                  content: { $: "note" },
                },
              ],
            },
          ],
        },
        data: {
          title: "Remix overview",
          subtitle: "Filtered Stremio add-on items ready for collections and Home rows.",
          matchedCount: "3",
          note: "All 3 upstream items carry valid IMDb ids.",
        },
      },
    };
  }
  return { status: 404, body: { error: { code: "not_found", message: "Route not found." } } };
}

(globalThis as any).handle = handle;
`
	return Template{
		ID:              "stremio_remix",
		Label:           "From a Stremio add-on link",
		Summary:         "Install a Stremio add-on directly, or create a remix with settings and a page.",
		Kind:            plugins.KindExtension,
		Capabilities:    []plugins.Capability{plugins.CapListSource, plugins.CapUI},
		DefaultPluginID: "community.studio.remix",
		DefaultName:     "Stremio remix",
		DefaultRoute:    "/catalog/movie/remix_top.json",
		DefaultPage:     "remix",
		SupportsStremio: true,
		Files: map[string]string{
			"manifest.json":        prettyJSON(manifest),
			"settings.schema.json": prettyJSON(settings),
			"i18n/en.json":         prettyJSON(stringsEN),
			"src/plugin.ts":        code,
		},
	}
}

func templateLocalFiles() Template {
	manifest := map[string]any{
		"id":          "community.studio.localfiles",
		"version":     "0.1.0",
		"name":        "Local folders",
		"description": "Scans owner-picked folders and attaches local media files to catalogue titles.",
		"types":       []string{"movie", "series"},
		"resources":   []string{"stream"},
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "source",
			"capabilities": []string{"stream_source", "ui"},
			"summary":      "$t:summary",
			"author":       map[string]any{"name": "Server owner"},
			"license":      "MIT",
			"runtime":      map[string]any{"kind": "hosted", "entry": "src/plugin.ts"},
			"permissions": map[string]any{
				"folders": "read",
				"catalog": "read",
			},
			"settings": map[string]any{"$ref": "settings.schema.json"},
			"strings":  map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{
				"source": map[string]any{
					"class":     "direct",
					"abilities": []string{"movies", "series"},
				},
				"pages": []map[string]any{
					{
						"key":   "folders",
						"label": "$t:folders_label",
						"route": "/ether/view/folders",
					},
				},
			},
		},
	}
	settings := map[string]any{
		"fields": []map[string]any{
			{
				"key":   "rootFolder",
				"kind":  "folder",
				"label": "Media folder",
				"hint":  "Choose a local folder on this server to scan for video files.",
			},
		},
	}
	stringsEN := map[string]string{
		"summary":            "Attaches files in folders you choose as direct releases on catalogue titles.",
		"folders_label":      "Unmatched folders",
		"matched_files":      "Matched files",
		"match_folder_title": "Match a folder to a title",
		"match_folder":       "Match folder",
		"search_title":       "Search for a movie or show",
	}
	code := `// Local files source plugin (SPEC D6 / WS11a Local files template).
type Request = {
  method: string;
  path: string;
  query?: Record<string, string[]>;
  headers?: Record<string, string>;
  body?: unknown;
};

export function handle(req: Request) {
  if (req.path === "/ether/configure") { return { status: 200, body: { errors: {} } }; }
  if (req.path === "/ether/health") {
    return { status: 200, body: { status: "ok", reason: "" } };
  }
  if (req.path === "/ether/test") {
    return {
      status: 200,
      body: {
        ok: true,
        message: "Scanned picked folders.",
        details: ["2 local video files matched to TMDB titles."],
      },
    };
  }
  if (req.path.startsWith("/stream/")) {
    return {
      status: 200,
      body: {
        streams: [
          {
            name: "Local folder",
            title: "Movie.2024.2160p.REMUX.mkv",
            ether: {
              id: "local-1",
              releaseName: "Movie.2024.2160p.UHD.BluRay.REMUX.HEVC.TrueHD.7.1",
              size: 42949672960,
              languages: ["en"],
              file: "movies/Movie (2024)/Movie.2024.2160p.REMUX.mkv",
            },
          },
        ],
      },
    };
  }
  if (req.path === "/ether/view/folders") {
    return {
      status: 200,
      body: {
        title: "Unmatched folders",
        spec: {
          specVersion: 1,
          layout: "detail",
          header: [
            {
              kind: "page-header",
              title: { $: "title" },
              description: { $: "subtitle" },
            },
          ],
          body: [
            {
              kind: "stat-tile",
              iconKey: "folder",
              accent: "primary",
              label: "$t:matched_files",
              value: { $: "matchedCount" },
            },
            {
              kind: "panel",
              title: "$t:match_folder_title",
              blocks: [
                {
                  kind: "widget",
                  widget: "ether.titleMatch",
                  props: {
                    action: "matchFolder",
                    label: "$t:match_folder",
                    placeholder: "$t:search_title",
                  },
                },
              ],
            },
          ],
        },
        data: {
          title: "Unmatched folders",
          subtitle: "Files attach directly to catalogue titles as local releases.",
          matchedCount: "2",
        },
      },
    };
  }
  if (req.path === "/ether/action/matchFolder") {
    return { status: 200, body: { toast: "Folder matched to catalogue title.", refresh: true } };
  }
  return { status: 404, body: { error: { code: "not_found", message: "Route not found." } } };
}

(globalThis as any).handle = handle;
`
	return Template{
		ID:              "local_files",
		Label:           "Local files",
		Summary:         "Attach files from owner-picked folders to catalogue titles as direct releases.",
		Kind:            plugins.KindSource,
		Capabilities:    []plugins.Capability{plugins.CapStreamSource, plugins.CapUI},
		DefaultPluginID: "community.studio.localfiles",
		DefaultName:     "Local folders",
		DefaultRoute:    "/stream/movie/tt0133093.json",
		DefaultPage:     "folders",
		Files: map[string]string{
			"manifest.json":        prettyJSON(manifest),
			"settings.schema.json": prettyJSON(settings),
			"i18n/en.json":         prettyJSON(stringsEN),
			"src/plugin.ts":        code,
		},
	}
}

func templateList() Template {
	manifest := map[string]any{
		"id":          "community.studio.list",
		"version":     "0.1.0",
		"name":        "Festival winners",
		"description": "Feeds a curated list into Collection Studio and Home rows.",
		"types":       []string{"movie"},
		"resources":   []string{"catalog"},
		"catalogs": []map[string]any{
			{"type": "movie", "id": "festival_picks", "name": "Festival winners"},
		},
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "extension",
			"capabilities": []string{"list_source", "ui"},
			"summary":      "$t:summary",
			"author":       map[string]any{"name": "Server owner"},
			"license":      "MIT",
			"runtime":      map[string]any{"kind": "hosted", "entry": "src/plugin.ts"},
			"permissions": map[string]any{
				"catalog":     "read",
				"collections": "write",
			},
			"strings": map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{
				"listSources": []map[string]any{
					{
						"key":      "festival_picks",
						"label":    "$t:list_label",
						"hint":     "$t:list_hint",
						"catalog":  "festival_picks",
						"maxItems": 100,
						"refresh":  "12h",
					},
				},
				"pages": []map[string]any{
					{
						"key":   "shelf",
						"label": "$t:shelf_label",
						"route": "/ether/view/shelf",
					},
				},
			},
		},
	}
	stringsEN := map[string]string{
		"summary":           "Curated festival winners for Collection Studio and Home shelves.",
		"list_label":        "Festival winners",
		"list_hint":         "Award-winning films refreshed twice a day.",
		"shelf_label":       "Festival shelf",
		"catalogue_matches": "Catalogue matches",
		"collection_sync":   "Collection sync",
		"sync_collection":   "Sync collection now",
	}
	code := `// List source plugin (SPEC §5.1.3 / WS11a List template).
type Request = {
  method: string;
  path: string;
  query?: Record<string, string[]>;
  headers?: Record<string, string>;
  body?: unknown;
};

const ITEMS = [
  { id: "tt6751668", type: "movie", name: "Parasite", year: 2019 },
  { id: "tt17009710", type: "movie", name: "Anatomy of a Fall", year: 2023 },
  { id: "tt8367814", type: "movie", name: "The Gentlemen", year: 2019 },
];

export function handle(req: Request) {
  if (req.path === "/ether/configure") { return { status: 200, body: { errors: {} } }; }
  if (req.path === "/ether/health") {
    return { status: 200, body: { status: "ok", reason: "" } };
  }
  if (req.path.startsWith("/catalog/")) {
    return { status: 200, body: { metas: ITEMS } };
  }
  if (req.path === "/ether/action/syncShelf" || req.path === "/ether/jobs/sync") {
    ether.collections.upsert({
      id: "festival-winners",
      name: "Festival winners",
      items: ITEMS.map((i) => i.id),
    });
    return { status: 200, body: { toast: "Collection updated.", refresh: true } };
  }
  if (req.path === "/ether/view/shelf") {
    return {
      status: 200,
      body: {
        title: "Festival shelf",
        spec: {
          specVersion: 1,
          layout: "detail",
          header: [
            {
              kind: "page-header",
              title: { $: "title" },
              description: { $: "subtitle" },
            },
          ],
          body: [
            {
              kind: "stat-tile",
              iconKey: "sparkles",
              accent: "primary",
              label: "$t:catalogue_matches",
              value: { $: "count" },
            },
            {
              kind: "panel",
              title: "$t:collection_sync",
              blocks: [
                {
                  kind: "widget",
                  widget: "ether.actionButton",
                  props: {
                    label: "$t:sync_collection",
                    action: "syncShelf",
                  },
                },
              ],
            },
          ],
        },
        data: {
          title: "Festival shelf",
          subtitle: "Titles matched against your catalogue by IMDb id.",
          count: String(ITEMS.length),
        },
      },
    };
  }
  return { status: 404, body: { error: { code: "not_found", message: "Route not found." } } };
}

(globalThis as any).handle = handle;
`
	return Template{
		ID:              "list",
		Label:           "List",
		Summary:         "Feed a list of IMDb or TMDB titles into Collection Studio and Home rows.",
		Kind:            plugins.KindExtension,
		Capabilities:    []plugins.Capability{plugins.CapListSource, plugins.CapUI},
		DefaultPluginID: "community.studio.list",
		DefaultName:     "Festival winners",
		DefaultRoute:    "/catalog/movie/festival_picks.json",
		DefaultPage:     "shelf",
		Files: map[string]string{
			"manifest.json": prettyJSON(manifest),
			"i18n/en.json":  prettyJSON(stringsEN),
			"src/plugin.ts": code,
		},
	}
}

func templateNotification() Template {
	manifest := map[string]any{
		"id":          "community.studio.notify",
		"version":     "0.1.0",
		"name":        "Custom webhook",
		"description": "Delivers Ether alerts and weekly digests to a custom webhook endpoint.",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "notification_channel",
			"capabilities": []string{"notify"},
			"summary":      "$t:summary",
			"author":       map[string]any{"name": "Server owner"},
			"license":      "MIT",
			"runtime":      map[string]any{"kind": "hosted", "entry": "src/plugin.ts"},
			"permissions": map[string]any{
				"network": []string{"hooks.example.com"},
				"secrets": []string{"webhook_token"},
			},
			"settings": map[string]any{"$ref": "settings.schema.json"},
			"strings":  map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{
				"notification": map[string]any{
					"sendTest":  true,
					"formats":   []string{"text", "markdown"},
					"maxLength": 2000,
				},
			},
		},
	}
	settings := map[string]any{
		"fields": []map[string]any{
			{
				"key":   "endpoint",
				"kind":  "url",
				"label": "Webhook endpoint",
				"hint":  "HTTPS endpoint that receives notification payloads.",
			},
			{
				"key":   "webhook_token",
				"kind":  "secret",
				"label": "Signing token",
				"hint":  "Bearer token sent in the Authorization header.",
			},
		},
	}
	stringsEN := map[string]string{
		"summary": "Sends Ether alerts and digests to your custom webhook.",
	}
	code := `// Notification channel plugin (SPEC §5.1.3 / WS11a Notification channel template).
type Request = {
  method: string;
  path: string;
  query?: Record<string, string[]>;
  headers?: Record<string, string>;
  body?: unknown;
};

export function handle(req: Request) {
  if (req.path === "/ether/health") {
    return { status: 200, body: { status: "ok", reason: "" } };
  }
  if (req.path === "/ether/test" || req.path === "/ether/notify") {
    ether.log("info", "delivered notification payload");
    return {
      status: 200,
      body: {
        ok: true,
        delivered: true,
        message: "Test notification delivered to webhook.",
      },
    };
  }
  if (req.path === "/ether/configure") {
    return { status: 200, body: { errors: {} } };
  }
  return { status: 404, body: { error: { code: "not_found", message: "Route not found." } } };
}

(globalThis as any).handle = handle;
`
	return Template{
		ID:              "notification",
		Label:           "Notification channel",
		Summary:         "Deliver server alerts and digests to a custom webhook or messaging service.",
		Kind:            plugins.KindNotificationChannel,
		Capabilities:    []plugins.Capability{plugins.CapNotify},
		DefaultPluginID: "community.studio.notify",
		DefaultName:     "Custom webhook",
		DefaultRoute:    "/ether/notify",
		Files: map[string]string{
			"manifest.json":        prettyJSON(manifest),
			"settings.schema.json": prettyJSON(settings),
			"i18n/en.json":         prettyJSON(stringsEN),
			"src/plugin.ts":        code,
		},
	}
}

func templateRanking() Template {
	manifest := map[string]any{
		"id":          "community.studio.ranking",
		"version":     "0.1.0",
		"name":        "Language release boost",
		"description": "Scores release candidates higher when they include your preferred audio language.",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "extension",
			"capabilities": []string{"ranking_step"},
			"summary":      "$t:summary",
			"author":       map[string]any{"name": "Server owner"},
			"license":      "MIT",
			"runtime":      map[string]any{"kind": "hosted", "entry": "src/plugin.ts"},
			"strings":      map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{
				"rankingSteps": []map[string]any{
					{
						"key":   "prefer_language",
						"label": "$t:step_label",
						"config": map[string]any{
							"fields": []map[string]any{
								{
									"key":   "language",
									"kind":  "language",
									"label": "Preferred audio language",
									"hint":  "Candidates with this audio track receive a positive score boost.",
								},
							},
						},
					},
				},
			},
		},
	}
	stringsEN := map[string]string{
		"summary":    "Boosts release candidates that carry your preferred audio track.",
		"step_label": "Prefer audio language",
	}
	code := `// Hot-path ranking step plugin (SPEC §5.2.1 / WS11a Ranking step template).
// Runs in-process inside the sandboxed WASM runtime with zero network or disk I/O.
export type CandidateFacts = {
  releaseId: string;
  name: string;
  resolution?: string;
  hdr?: string;
  audioCodec?: string;
  languages?: string[];
  cached?: boolean;
  verified?: boolean;
};

export function rank_key(
  candidate: CandidateFacts,
  config?: { language?: string }
): number {
  const target = (config?.language || "fr").toLowerCase();
  const langs = (candidate.languages || []).map((l) => l.toLowerCase());
  let score = 0;
  if (langs.includes(target)) {
    score += 500;
  }
  if (candidate.cached) {
    score += 100;
  }
  return score;
}

(globalThis as any).rank_key = rank_key;
`
	return Template{
		ID:              "ranking",
		Label:           "Ranking step",
		Summary:         "Score release candidates in-process during profile ranking with a fast TypeScript function.",
		Kind:            plugins.KindExtension,
		Capabilities:    []plugins.Capability{plugins.CapRankingStep},
		DefaultPluginID: "community.studio.ranking",
		DefaultName:     "Language release boost",
		DefaultRoute:    "hot:rank_key",
		Files: map[string]string{
			"manifest.json": prettyJSON(manifest),
			"i18n/en.json":  prettyJSON(stringsEN),
			"src/plugin.ts": code,
		},
	}
}

func templatePage() Template {
	manifest := map[string]any{
		"id":          "community.studio.page",
		"version":     "0.1.0",
		"name":        "Curator notes",
		"description": "Adds a ViewSpec v1 page and slot panel to the console.",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "extension",
			"capabilities": []string{"ui"},
			"summary":      "$t:summary",
			"author":       map[string]any{"name": "Server owner"},
			"license":      "MIT",
			"runtime":      map[string]any{"kind": "hosted", "entry": "src/plugin.ts"},
			"permissions": map[string]any{
				"catalog":     "read",
				"collections": "write",
			},
			"strings": map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{
				"pages": []map[string]any{
					{
						"key":   "overview",
						"label": "$t:page_label",
						"route": "/ether/view/overview",
					},
				},
			},
		},
	}
	stringsEN := map[string]string{
		"summary":            "Adds a native ViewSpec v1 page and slot panel to the console.",
		"page_label":         "Curator notes",
		"slot_label":         "Curator panel",
		"featured_picks":     "Featured picks",
		"curator_commentary": "Curator commentary",
		"publish_highlight":  "Publish highlight shelf",
	}
	code := `// ViewSpec v1 UI page and slot plugin (SPEC §5.6 / WS11a Page template).
type Request = {
  method: string;
  path: string;
  query?: Record<string, string[]>;
  headers?: Record<string, string>;
  body?: unknown;
};

export function handle(req: Request) {
  if (req.path === "/ether/configure") { return { status: 200, body: { errors: {} } }; }
  if (req.path === "/ether/health") {
    return { status: 200, body: { status: "ok", reason: "" } };
  }
  if (req.path === "/ether/action/createHighlight") {
    ether.collections.upsert({
      id: "curator-highlights",
      name: "Curator highlights",
      items: ["tt0133093"],
    });
    return {
      status: 200,
      body: { toast: "Saved curator highlight collection.", refresh: true },
    };
  }
  if (req.path.startsWith("/ether/view/")) {
    return {
      status: 200,
      body: {
        title: "Curator notes",
        spec: {
          specVersion: 1,
          layout: "detail",
          header: [
            {
              kind: "page-header",
              title: { $: "title" },
              description: { $: "subtitle" },
            },
          ],
          body: [
            {
              kind: "stat-tile",
              iconKey: "sparkles",
              accent: "primary",
              label: "$t:featured_picks",
              value: { $: "featuredCount" },
            },
            {
              kind: "panel",
              title: "$t:curator_commentary",
              blocks: [
                {
                  kind: "text",
                  content: { $: "commentary" },
                },
                {
                  kind: "widget",
                  widget: "ether.actionButton",
                  props: {
                    label: "$t:publish_highlight",
                    action: "createHighlight",
                  },
                },
              ],
            },
          ],
        },
        data: {
          title: "Curator notes",
          subtitle: "Staff picks and screening notes rendered through ViewSpec v1.",
          featuredCount: "4",
          commentary: "Every Friday night features a restored 4K master in the main theatre.",
        },
      },
    };
  }
  return { status: 404, body: { error: { code: "not_found", message: "Route not found." } } };
}

(globalThis as any).handle = handle;
`
	return Template{
		ID:              "page",
		Label:           "Page",
		Summary:         "Build a native ViewSpec v1 console page or attach a panel to a console slot.",
		Kind:            plugins.KindExtension,
		Capabilities:    []plugins.Capability{plugins.CapUI},
		DefaultPluginID: "community.studio.page",
		DefaultName:     "Curator notes",
		DefaultRoute:    "/ether/view/overview",
		DefaultPage:     "overview",
		SupportsSlot:    true,
		Files: map[string]string{
			"manifest.json": prettyJSON(manifest),
			"i18n/en.json":  prettyJSON(stringsEN),
			"src/plugin.ts": code,
		},
	}
}

func templateTool() Template {
	manifest := map[string]any{
		"id":          "community.studio.tool",
		"version":     "0.1.0",
		"name":        "Room lights tool",
		"description": "Exposes an agent tool to Ask Ether and paired MCP clients.",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "extension",
			"capabilities": []string{"tools"},
			"summary":      "$t:summary",
			"author":       map[string]any{"name": "Server owner"},
			"license":      "MIT",
			"runtime":      map[string]any{"kind": "hosted", "entry": "src/plugin.ts"},
			"strings":      map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{
				"tools": []map[string]any{
					{
						"name":        "check_room_lights",
						"title":       "$t:tool_title",
						"description": "Checks the theatre room lighting scene before playback starts.",
						"readOnly":    true,
						"inputSchema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"room": map[string]any{
									"type":        "string",
									"description": "Room name such as Living room or Theatre.",
								},
							},
						},
					},
				},
			},
		},
	}
	stringsEN := map[string]string{
		"summary":    "Adds a theatre room lighting check tool to Ask Ether.",
		"tool_title": "Check room lighting scene",
	}
	code := `// Agent tool plugin (SPEC §5.1.3 / WS11a Agent tool template).
type Request = {
  method: string;
  path: string;
  query?: Record<string, string[]>;
  headers?: Record<string, string>;
  body?: unknown;
};

export function handle(req: Request) {
  if (req.path === "/ether/configure") { return { status: 200, body: { errors: {} } }; }
  if (req.path === "/ether/health") {
    return { status: 200, body: { status: "ok", reason: "" } };
  }
  if (req.path.startsWith("/ether/tool/")) {
    return {
      status: 200,
      body: {
        scene: "Cinema dim",
        brightnessPct: 15,
        readyForPlayback: true,
      },
    };
  }
  return { status: 404, body: { error: { code: "not_found", message: "Route not found." } } };
}

(globalThis as any).handle = handle;
`
	return Template{
		ID:              "tool",
		Label:           "Agent tool",
		Summary:         "Add a read or proposal tool to Ask Ether and paired MCP clients.",
		Kind:            plugins.KindExtension,
		Capabilities:    []plugins.Capability{plugins.CapTools},
		DefaultPluginID: "community.studio.tool",
		DefaultName:     "Room lights tool",
		DefaultRoute:    "/ether/tool/check_room_lights",
		Files: map[string]string{
			"manifest.json": prettyJSON(manifest),
			"i18n/en.json":  prettyJSON(stringsEN),
			"src/plugin.ts": code,
		},
	}
}

// applyProjectOptions customizes a template's files for the given PluginID, Name, Slot, or StremioURL.
func applyProjectOptions(tpl Template, opts CreateProjectOptions) (pluginID, name string, files map[string]string) {
	files = make(map[string]string, len(tpl.Files))
	for k, v := range tpl.Files {
		files[k] = v
	}
	pluginID = strings.TrimSpace(opts.PluginID)
	if pluginID == "" {
		pluginID = tpl.DefaultPluginID
	}
	name = strings.TrimSpace(opts.Name)
	if name == "" {
		name = tpl.DefaultName
	}

	var man map[string]any
	if err := json.Unmarshal([]byte(files["manifest.json"]), &man); err != nil {
		return pluginID, name, files
	}
	man["id"] = pluginID
	man["name"] = name

	etherObj, _ := man["ether"].(map[string]any)
	contributes, _ := etherObj["contributes"].(map[string]any)

	if slot := strings.TrimSpace(opts.Slot); slot != "" && etherObj != nil && contributes != nil {
		ensureUICap(etherObj)
		var en map[string]string
		if json.Unmarshal([]byte(files["i18n/en.json"]), &en) == nil && en != nil {
			if en["slot_label"] == "" {
				en["slot_label"] = "Plugin action"
				files["i18n/en.json"] = prettyJSON(en)
			}
		}
		switch slot {
		case "page":
			contributes["pages"] = []map[string]any{
				{
					"key":   "overview",
					"label": "$t:page_label",
					"route": "/ether/view/overview",
				},
			}
		case "command":
			contributes["commands"] = []map[string]any{
				{
					"key":    "run_command",
					"label":  "$t:slot_label",
					"action": "createHighlight",
				},
			}
		default:
			entry := map[string]any{"slot": slot, "key": "custom_slot", "label": "$t:slot_label"}
			if strings.HasPrefix(slot, "actionMenu.") {
				action := "createHighlight"
				switch tpl.ID {
				case "list":
					action = "syncShelf"
				case "local_files":
					action = "matchFolder"
				}
				entry["action"] = action
			} else {
				entry["route"] = "/ether/view/overview"
			}
			contributes["slots"] = []map[string]any{entry}
		}
	}

	// Add-on links often carry a debrid key in the path, so only the host is
	// written into source; the link itself is never stored with the draft.
	if u, err := url.Parse(strings.TrimSpace(opts.StremioURL)); err == nil && u.Host != "" {
		if src, ok := files["src/plugin.ts"]; ok {
			files["src/plugin.ts"] = fmt.Sprintf("// Upstream Stremio add-on host: %s\n", u.Host) + src
		}
	}

	if opts.LiveSearch != nil && contributes != nil {
		if kinds, ok := contributes["mediaKinds"].([]any); ok {
			for _, k := range kinds {
				if kind, ok := k.(map[string]any); ok {
					browse, _ := kind["browse"].(map[string]any)
					if browse == nil {
						browse = map[string]any{}
						kind["browse"] = browse
					}
					browse["liveSearch"] = *opts.LiveSearch
				}
			}
		}
	}

	files["manifest.json"] = prettyJSON(man)
	return pluginID, name, files
}

func ensureUICap(etherObj map[string]any) {
	raw, _ := etherObj["capabilities"].([]any)
	for _, item := range raw {
		if item == "ui" {
			return
		}
	}
	if strs, ok := etherObj["capabilities"].([]string); ok {
		for _, s := range strs {
			if s == "ui" {
				return
			}
		}
		etherObj["capabilities"] = append(strs, "ui")
		return
	}
	etherObj["capabilities"] = append(raw, "ui")
}
