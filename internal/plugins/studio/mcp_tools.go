package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/kit"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/viewspec"
)

// ToolDeps holds dependencies for the Studio and extensibility agent and MCP tools.
type ToolDeps struct {
	Studio       *Studio
	Registry     *extend.Registry
	TextResolver extend.TextResolver
	Manager      *plugins.Manager
	Runner       *Runner
	Engine       *wasm.Engine
}

// RegisterTools registers the Studio and extensibility tools into cat.
func RegisterTools(cat *tools.Catalogue, deps ToolDeps) error {
	if cat == nil {
		return fmt.Errorf("studio: tool catalogue is required")
	}
	return cat.Register(append(Tools(deps), ProjectTools(deps.Studio)...)...)
}

// Tools returns the 8 Studio and extensibility tools (WS11h):
// registry_describe, viewspec_validate, viewspec_render, plugin_scaffold,
// plugin_check, plugin_replay, plugin_shadow_report, and plugin_install.
func Tools(deps ToolDeps) []tools.Tool {
	if deps.Runner == nil {
		deps.Runner = NewRunner(ReplayDeps{
			Manager: deps.Manager,
			Engine:  deps.Engine,
		})
	}
	return []tools.Tool{
		registryDescribeTool(deps),
		viewspecValidateTool(),
		viewspecRenderTool(deps),
		pluginScaffoldTool(deps),
		pluginCheckTool(deps),
		pluginReplayTool(deps),
		pluginShadowReportTool(deps),
		pluginInstallTool(deps),
	}
}

func registryDescribeTool(deps ToolDeps) tools.Tool {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"kind": {
				"type": "string",
				"description": "Optional descriptor kind to filter by (for example rankingStep, ruleField, collectionSource, page, widget, tool)."
			},
			"id": {
				"type": "string",
				"description": "Optional descriptor ID to inspect."
			},
			"owner": {
				"type": "string",
				"description": "Optional owner filter (builtin or a plugin ID)."
			},
			"locale": {
				"type": "string",
				"description": "Locale code for resolving copy, defaulting to en."
			},
			"limit": {
				"type": "integer",
				"minimum": 1,
				"maximum": 200,
				"description": "Maximum number of descriptors to return (1 to 200)."
			}
		},
		"additionalProperties": false
	}`)

	return tools.Tool{
		Name:        "registry_describe",
		Title:       "Describe extension registry",
		Description: "List registered extension descriptors by kind or owner, or inspect one descriptor in detail.",
		InputSchema: schema,
		ReadOnly:    true,
		Audience:    tools.AudienceOwner,
		Owner:       extend.Builtin,
		Execute: func(_ context.Context, _ tools.Principal, input map[string]any) (tools.Result, error) {
			if deps.Registry == nil {
				return tools.Result{}, tools.Userf("The extension registry is not available.")
			}
			kindStr, err := optString(input, "kind", 64)
			if err != nil {
				return tools.Result{}, err
			}
			idStr, err := optString(input, "id", 128)
			if err != nil {
				return tools.Result{}, err
			}
			ownerStr, err := optString(input, "owner", 128)
			if err != nil {
				return tools.Result{}, err
			}
			locale, err := optString(input, "locale", 16)
			if err != nil {
				return tools.Result{}, err
			}
			if locale == "" {
				locale = "en"
			}
			limit, err := optInt(input, "limit", 100, 1, 200)
			if err != nil {
				return tools.Result{}, err
			}

			if kindStr != "" && !extend.Known(extend.Kind(kindStr)) {
				return tools.Result{}, tools.Userf("Unknown descriptor kind %q.", kindStr)
			}

			resolver := deps.TextResolver
			if resolver == nil && deps.Manager != nil {
				resolver = deps.Manager.TextResolver()
			}
			resolveDescriptor := func(d extend.Descriptor) extend.Descriptor {
				if resolver != nil {
					if d.Label.Plugin != "" && d.Label.Key != "" {
						if s, ok := resolver(d.Label.Plugin, d.Label.Key, locale); ok {
							d.Label.Resolved = s
						}
					}
					if d.Hint.Plugin != "" && d.Hint.Key != "" {
						if s, ok := resolver(d.Hint.Plugin, d.Hint.Key, locale); ok {
							d.Hint.Resolved = s
						}
					}
				}
				return d
			}

			if kindStr != "" && idStr != "" {
				d, ok := deps.Registry.Get(extend.Kind(kindStr), idStr)
				if !ok {
					return tools.Result{}, tools.Userf("Descriptor %q of kind %q was not found.", idStr, kindStr)
				}
				return tools.Result{
					Data: map[string]any{
						"descriptor": resolveDescriptor(d),
					},
				}, nil
			}

			var list []extend.Descriptor
			if kindStr != "" {
				list = deps.Registry.List(extend.Kind(kindStr))
			} else {
				list = deps.Registry.List()
			}
			filtered := make([]extend.Descriptor, 0, len(list))
			for _, d := range list {
				if idStr != "" && d.ID != idStr {
					continue
				}
				if ownerStr != "" && d.Owner != ownerStr {
					continue
				}
				filtered = append(filtered, resolveDescriptor(d))
				if len(filtered) >= limit {
					break
				}
			}

			return tools.Result{
				Data: map[string]any{
					"count":       len(filtered),
					"descriptors": filtered,
				},
				Links: []tools.Link{
					{Label: "Open plugins", Route: "/settings/plugins"},
				},
			}, nil
		},
	}
}

func viewspecValidateTool() tools.Tool {
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["spec"],
		"properties": {
			"spec": {
				"description": "The ViewSpec JSON object or JSON string to validate."
			}
		},
		"additionalProperties": false
	}`)

	return tools.Tool{
		Name:        "viewspec_validate",
		Title:       "Validate ViewSpec document",
		Description: "Validate a ViewSpec JSON document against Ether's plugin UI rules and report any field errors.",
		InputSchema: schema,
		ReadOnly:    true,
		Audience:    tools.AudienceOwner,
		Owner:       extend.Builtin,
		Execute: func(_ context.Context, _ tools.Principal, input map[string]any) (tools.Result, error) {
			rawSpec, ok := input["spec"]
			if !ok || rawSpec == nil {
				return tools.Result{}, tools.Userf("Provide a spec object to validate.")
			}
			specBytes, err := encodeBoundedJSON(rawSpec, maxManifestBytes, "spec")
			if err != nil {
				return tools.Result{}, err
			}
			res := viewspec.ValidatePlugin(specBytes)
			errs := res.Errors
			if errs == nil {
				errs = []string{}
			}
			return tools.Result{
				Data: map[string]any{
					"ok":     res.OK,
					"errors": errs,
				},
			}, nil
		},
	}
}

func viewspecRenderTool(deps ToolDeps) tools.Tool {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"spec": {
				"description": "The ViewSpec JSON object or JSON string to validate and prepare for console rendering."
			},
			"data": {
				"description": "Optional data object bound to the ViewSpec document."
			},
			"plugin": {
				"type": "string",
				"description": "Optional installed plugin ID to load a contributed page from."
			},
			"page": {
				"type": "string",
				"description": "Optional contributed page key on the installed plugin."
			}
		},
		"additionalProperties": false
	}`)

	return tools.Tool{
		Name:        "viewspec_render",
		Title:       "Render ViewSpec preview",
		Description: "Validate a ViewSpec document and data payload, and return the validated spec, data, and console preview URL.",
		InputSchema: schema,
		ReadOnly:    true,
		Audience:    tools.AudienceOwner,
		Owner:       extend.Builtin,
		Execute: func(ctx context.Context, _ tools.Principal, input map[string]any) (tools.Result, error) {
			pluginID, err := optString(input, "plugin", 128)
			if err != nil {
				return tools.Result{}, err
			}
			pageKey, err := optString(input, "page", 64)
			if err != nil {
				return tools.Result{}, err
			}

			var specBytes []byte
			var dataObj map[string]any

			if rawSpec, ok := input["spec"]; ok && rawSpec != nil {
				specBytes, err = encodeBoundedJSON(rawSpec, maxManifestBytes, "spec")
				if err != nil {
					return tools.Result{}, err
				}
			}
			if rawData, ok := input["data"]; ok && rawData != nil {
				dataBytes, err := encodeBoundedJSON(rawData, maxManifestBytes, "data")
				if err != nil {
					return tools.Result{}, err
				}
				if err := json.Unmarshal(dataBytes, &dataObj); err != nil {
					return tools.Result{}, tools.Userf("The data field must be a JSON object.")
				}
			}

			if len(specBytes) == 0 {
				if pluginID == "" || pageKey == "" {
					return tools.Result{}, tools.Userf("Provide either spec or both plugin and page.")
				}
				if deps.Manager == nil {
					return tools.Result{}, tools.Userf("The plugin manager is not available.")
				}
				rec, err := deps.Manager.Get(ctx, pluginID)
				if err != nil {
					return tools.Result{}, tools.Userf("Plugin %q is not installed.", pluginID)
				}
				route := ""
				if rec.Manifest.Ether != nil {
					for _, p := range rec.Manifest.Ether.Contributes.Pages {
						if p.Key == pageKey {
							route = p.Route
							break
						}
					}
				}
				if route == "" {
					return tools.Result{}, tools.Userf("Page %q was not found on plugin %q.", pageKey, pluginID)
				}
				resp, err := deps.Manager.Call(ctx, pluginID, plugins.Request{
					PluginID: pluginID,
					Method:   "GET",
					Path:     route,
				})
				if err != nil {
					return tools.Result{}, tools.Userf("Could not load page %q from plugin %q.", pageKey, pluginID)
				}
				body := unwrapResponseBody(resp.Body)
				var pageEnvelope struct {
					Spec json.RawMessage `json:"spec"`
					Data map[string]any  `json:"data"`
				}
				if json.Unmarshal(body, &pageEnvelope) == nil && len(pageEnvelope.Spec) > 0 {
					specBytes = pageEnvelope.Spec
					if dataObj == nil {
						dataObj = pageEnvelope.Data
					}
				} else {
					specBytes = body
				}
			}

			if dataObj == nil {
				dataObj = map[string]any{}
			}

			valRes := viewspec.ValidatePlugin(specBytes)
			if !valRes.OK {
				return tools.Result{
					Data: map[string]any{
						"ok":     false,
						"errors": valRes.Errors,
					},
				}, nil
			}

			var specObj map[string]any
			_ = json.Unmarshal(specBytes, &specObj)

			consoleURL := "/settings/plugins/studio/preview"
			if pluginID != "" && pageKey != "" {
				consoleURL = fmt.Sprintf("/plugins/%s/pages/%s", pluginID, pageKey)
			}

			return tools.Result{
				Data: map[string]any{
					"ok":         true,
					"spec":       specObj,
					"data":       dataObj,
					"consoleUrl": consoleURL,
				},
				Links: []tools.Link{
					{Label: "Open preview in console", Route: consoleURL},
				},
			}, nil
		},
	}
}

// ScaffoldRequest describes a new plugin to generate.
type ScaffoldRequest struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Summary      string   `json:"summary,omitempty"`
}

// ScaffoldResult holds the generated manifest and source files.
type ScaffoldResult struct {
	Manifest plugins.Manifest  `json:"manifest"`
	Files    map[string]string `json:"files"`
}

// Scaffold builds a valid manifest and TypeScript entry file for the requested
// plugin kind and capabilities.
func Scaffold(req ScaffoldRequest) (ScaffoldResult, error) {
	id := strings.TrimSpace(req.ID)
	name := strings.TrimSpace(req.Name)
	if id == "" || len(id) > 120 {
		return ScaffoldResult{}, tools.Userf("Provide a reverse-DNS plugin id of at most 120 characters.")
	}
	if name == "" || len(name) > 80 {
		return ScaffoldResult{}, tools.Userf("Provide a plugin name of at most 80 characters.")
	}
	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		summary = "Custom plugin for Ether."
	}
	if len(summary) > 140 {
		return ScaffoldResult{}, tools.Userf("Summary must be at most 140 characters.")
	}

	kind := plugins.Kind(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = plugins.KindExtension
	}
	var caps []plugins.Capability
	for _, c := range req.Capabilities {
		c = strings.TrimSpace(c)
		if c != "" {
			caps = append(caps, plugins.Capability(c))
		}
	}
	if len(caps) == 0 {
		switch kind {
		case plugins.KindSource:
			caps = []plugins.Capability{plugins.CapStreamSource}
		case plugins.KindNotificationChannel:
			caps = []plugins.Capability{plugins.CapNotify}
		case plugins.KindLiveTVSource:
			caps = []plugins.Capability{plugins.CapLiveTV}
		default:
			caps = []plugins.Capability{plugins.CapRankingStep}
		}
	}

	m := plugins.Manifest{
		ID:          id,
		Version:     "0.1.0",
		Name:        name,
		Description: summary,
		Types:       []string{"movie", "series"},
		Ether: &plugins.EtherBlock{
			Protocol:     plugins.Protocol,
			Kind:         kind,
			Capabilities: caps,
			Summary:      summary,
			Runtime: plugins.RuntimeDecl{
				Kind:  "hosted",
				Entry: "plugin.ts",
			},
		},
	}

	if kind == plugins.KindIntegration {
		m.Ether.Contributes.Integration = &plugins.IntegrationMeta{
			Category: "automation",
			Adds:     []string{"Syncs watch activity."},
			Connect:  "none",
		}
	}

	var codeLines []string
	codeLines = append(codeLines, `type Request = { method: string; path: string; query?: Record<string, string[]>; body?: unknown };`)
	codeLines = append(codeLines, `type Response = { status: number; headers?: Record<string, string>; body?: unknown };`)

	needHandle := false
	for _, cap := range caps {
		switch cap {
		case plugins.CapStreamSource:
			needHandle = true
			m.Ether.Contributes.Source = &plugins.SourceMeta{Class: "direct"}
		case plugins.CapListSource:
			needHandle = true
			m.Catalogs = []plugins.StremioCat{{Type: "movie", ID: "picks", Name: "Picked movies"}}
			m.Ether.Contributes.ListSources = []plugins.ListSourceMeta{
				{Key: "picks", Label: "Picked movies", Catalog: "picks", MaxItems: 100, Refresh: "1h"},
			}
		case plugins.CapLiveTV:
			needHandle = true
			m.Ether.Contributes.Channels = &plugins.ChannelsMeta{Refresh: "1h", Test: true}
		case plugins.CapSubtitles:
			needHandle = true
			m.Ether.Contributes.Subtitles = &plugins.SubtitlesMeta{
				Languages: json.RawMessage(`"any"`),
				Formats:   []string{"srt", "vtt"},
			}
		case plugins.CapMarkers:
			needHandle = true
			m.Ether.Contributes.Markers = &plugins.MarkersMeta{Kinds: []string{"intro", "credits"}}
		case plugins.CapNotify:
			needHandle = true
			m.Ether.Contributes.Notification = &plugins.NotificationMeta{
				SendTest:  true,
				Formats:   []string{"plain"},
				MaxLength: 2000,
			}
		case plugins.CapEvents:
			needHandle = true
			m.Ether.Contributes.Events = &plugins.EventsMeta{
				Subscribe: []string{plugins.EventPlaybackCompleted},
			}
		case plugins.CapRankingStep:
			m.Ether.Contributes.RankingSteps = []plugins.RankingStepMeta{
				{Key: "prefer_custom", Label: "Prefer matching releases"},
			}
		case plugins.CapRuleCondition:
			m.Ether.Contributes.RuleConditions = []plugins.RuleConditionMeta{
				{Key: "custom_match", Label: "Custom release match", Operators: []string{"equals", "contains"}},
			}
		case plugins.CapTools:
			needHandle = true
			m.Ether.Contributes.Tools = []plugins.ToolMeta{
				{
					Name:        "status_summary",
					Title:       "Status summary",
					Description: "Return a short status summary from the plugin.",
					InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
					ReadOnly:    true,
				},
			}
		case plugins.CapUI:
			needHandle = true
			m.Ether.Contributes.Pages = []plugins.PageMeta{
				{Key: "overview", Label: "Overview", Route: "/ether/view/overview"},
			}
		}
	}

	if needHandle || (!slices.Contains(caps, plugins.CapRankingStep) && !slices.Contains(caps, plugins.CapRuleCondition)) {
		codeLines = append(codeLines, `
function handle(req: Request): Response {
  if (req.path === "/ether/health" || req.path === "/ether/test") {
    return { status: 200, body: { status: "ok" } };
  }
  if (req.path === "/ether/event") {
    return { status: 200, body: { ack: [] } };
  }
  if (req.path === "/ether/channels.json") {
    return { status: 200, body: { channels: [] } };
  }
  return { status: 200, body: { metas: [] } };
}
(globalThis as any).handle = handle;`)
	}
	if slices.Contains(caps, plugins.CapRankingStep) {
		codeLines = append(codeLines, `
function rank_key(candidate: { name: string }, _config: Record<string, unknown>): number {
  return candidate.name ? 0 : 0;
}
(globalThis as any).rank_key = rank_key;`)
	}
	if slices.Contains(caps, plugins.CapRuleCondition) {
		codeLines = append(codeLines, `
function cond_match(candidate: { name: string }, operator: string, value: unknown): boolean {
  const target = typeof value === "string" ? value : "";
  if (operator === "contains" && target !== "") {
    return candidate.name.includes(target);
  }
  return candidate.name === target;
}
(globalThis as any).cond_match = cond_match;`)
	}

	if err := m.Validate(); err != nil {
		return ScaffoldResult{}, tools.Userf("Generated manifest is not valid: %s", err.Error())
	}

	return ScaffoldResult{
		Manifest: m,
		Files: map[string]string{
			"plugin.ts": strings.TrimSpace(strings.Join(codeLines, "\n")) + "\n",
		},
	}, nil
}

func pluginScaffoldTool(_ ToolDeps) tools.Tool {
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["id", "name"],
		"properties": {
			"id": {
				"type": "string",
				"description": "Reverse-DNS plugin ID (for example org.example.dub-ranker)."
			},
			"name": {
				"type": "string",
				"description": "Human-readable plugin name."
			},
			"kind": {
				"type": "string",
				"description": "Plugin kind: source, integration, notification_channel, livetv_source, or extension."
			},
			"capabilities": {
				"type": "array",
				"items": { "type": "string" },
				"description": "Capabilities the plugin contributes."
			},
			"summary": {
				"type": "string",
				"description": "Short one-sentence summary in sentence case."
			}
		},
		"additionalProperties": false
	}`)

	return tools.Tool{
		Name:        "plugin_scaffold",
		Title:       "Scaffold plugin",
		Description: "Generate a valid manifest and TypeScript entry file for a new Ether plugin.",
		InputSchema: schema,
		ReadOnly:    true,
		Audience:    tools.AudienceOwner,
		Owner:       extend.Builtin,
		Execute: func(_ context.Context, _ tools.Principal, input map[string]any) (tools.Result, error) {
			id, err := reqString(input, "id", 120)
			if err != nil {
				return tools.Result{}, err
			}
			name, err := reqString(input, "name", 80)
			if err != nil {
				return tools.Result{}, err
			}
			kind, err := optString(input, "kind", 40)
			if err != nil {
				return tools.Result{}, err
			}
			summary, err := optString(input, "summary", 140)
			if err != nil {
				return tools.Result{}, err
			}
			caps, err := optStringSlice(input, "capabilities", 12, 40)
			if err != nil {
				return tools.Result{}, err
			}

			res, err := Scaffold(ScaffoldRequest{
				ID:           id,
				Name:         name,
				Kind:         kind,
				Capabilities: caps,
				Summary:      summary,
			})
			if err != nil {
				return tools.Result{}, err
			}
			return tools.Result{
				Data: map[string]any{
					"manifest": res.Manifest,
					"files":    res.Files,
				},
			}, nil
		},
	}
}

// CheckRequest holds the manifest and files to validate in Studio.
type CheckRequest struct {
	Manifest json.RawMessage   `json:"manifest"`
	Files    map[string]string `json:"files,omitempty"`
}

// ToolCheckReport is the output of Check.
type ToolCheckReport struct {
	OK      bool                 `json:"ok"`
	Issues  []plugins.FieldError `json:"issues"`
	Exports map[string]bool      `json:"exports,omitempty"`
}

// Check validates a plugin's manifest, copy rules, settings schema, ViewSpec
// files, and hosted TypeScript or JavaScript entry code.
func Check(ctx context.Context, _ *wasm.Engine, req CheckRequest) (ToolCheckReport, error) {
	files := make(map[string][]byte, len(req.Files))
	for name, contents := range req.Files {
		files[name] = []byte(contents)
	}
	rep := kit.Check(ctx, &kit.Package{ManifestRaw: req.Manifest, Files: files})
	out := ToolCheckReport{OK: rep.OK, Issues: []plugins.FieldError{}, Exports: rep.Exports}
	for _, issue := range rep.Errors {
		out.Issues = append(out.Issues, plugins.FieldError{Field: issue.Field, Message: issue.Message})
	}
	return out, nil
}

func pluginCheckTool(deps ToolDeps) tools.Tool {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"plugin": {
				"type": "string",
				"description": "Optional installed plugin ID to check."
			},
			"manifest": {
				"description": "Manifest JSON object or JSON string to check."
			},
			"files": {
				"type": "object",
				"description": "Map of file paths to file contents for the plugin."
			}
		},
		"additionalProperties": false
	}`)

	return tools.Tool{
		Name:        "plugin_check",
		Title:       "Check plugin",
		Description: "Validate a plugin manifest, copy rules, settings schema, ViewSpec documents, and TypeScript or JavaScript entry code.",
		InputSchema: schema,
		ReadOnly:    true,
		Audience:    tools.AudienceOwner,
		Owner:       extend.Builtin,
		Execute: func(ctx context.Context, _ tools.Principal, input map[string]any) (tools.Result, error) {
			pluginID, err := optString(input, "plugin", 128)
			if err != nil {
				return tools.Result{}, err
			}
			var manifestBytes []byte
			files := map[string]string{}

			if pluginID != "" {
				if deps.Manager == nil {
					return tools.Result{}, tools.Userf("The plugin manager is not available.")
				}
				rec, err := deps.Manager.Get(ctx, pluginID)
				if err != nil {
					return tools.Result{}, tools.Userf("Plugin %q is not installed.", pluginID)
				}
				manifestBytes = rec.ManifestRaw
				if len(manifestBytes) == 0 {
					manifestBytes, _ = json.Marshal(rec.Manifest)
				}
				if deps.Runner != nil {
					for k, v := range deps.Runner.loadArtifactFiles(ctx, rec.ID, rec.Version) {
						files[k] = string(v)
					}
				}
			} else {
				rawManifest, ok := input["manifest"]
				if !ok || rawManifest == nil {
					return tools.Result{}, tools.Userf("Provide either an installed plugin id or a manifest.")
				}
				manifestBytes, err = encodeBoundedJSON(rawManifest, maxManifestBytes, "manifest")
				if err != nil {
					return tools.Result{}, err
				}
				files, err = optFileMap(input, "files")
				if err != nil {
					return tools.Result{}, err
				}
			}

			eng := deps.Engine
			rep, err := Check(ctx, eng, CheckRequest{
				Manifest: manifestBytes,
				Files:    files,
			})
			if err != nil {
				return tools.Result{}, err
			}
			return tools.Result{
				Data: map[string]any{
					"ok":      rep.OK,
					"issues":  rep.Issues,
					"exports": rep.Exports,
				},
			}, nil
		},
	}
}

func pluginReplayTool(deps ToolDeps) tools.Tool {
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["kind"],
		"properties": {
			"plugin": {
				"type": "string",
				"description": "Installed plugin ID to replay."
			},
			"devInstance": {
				"type": "object",
				"description": "Unsaved Studio plugin instance with manifest, entry or files, and settings."
			},
			"kind": {
				"type": "string",
				"enum": ["ranking", "events", "list", "channels"],
				"description": "Replay mode to run: ranking, events, list, or channels."
			},
			"params": {
				"type": "object",
				"description": "Mode-specific replay parameters (stepKey, position, limit, days, sourceKey, probeLimit)."
			}
		},
		"additionalProperties": false
	}`)

	return tools.Tool{
		Name:        "plugin_replay",
		Title:       "Replay plugin",
		Description: "Replay recorded resolution history, events, list pages, or channels against an installed plugin or Studio dev instance.",
		InputSchema: schema,
		ReadOnly:    true,
		OpenWorld:   true,
		Audience:    tools.AudienceOwner,
		Owner:       extend.Builtin,
		Execute: func(ctx context.Context, _ tools.Principal, input map[string]any) (tools.Result, error) {
			if deps.Runner == nil {
				return tools.Result{}, tools.Userf("The Studio replay runner is not available.")
			}
			rawInput, err := encodeBoundedJSON(input, maxRequestBytes, "input")
			if err != nil {
				return tools.Result{}, err
			}
			var req ReplayRequest
			if err := json.Unmarshal(rawInput, &req); err != nil {
				return tools.Result{}, tools.Userf("Invalid replay parameters: %s", err.Error())
			}
			report, err := deps.Runner.Replay(ctx, req)
			if err != nil {
				return tools.Result{}, tools.Userf("Replay failed: %s", err.Error())
			}
			dataBytes, _ := json.Marshal(report)
			var dataMap map[string]any
			_ = json.Unmarshal(dataBytes, &dataMap)
			return tools.Result{
				Data: dataMap,
			}, nil
		},
	}
}

func pluginShadowReportTool(deps ToolDeps) tools.Tool {
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["plugin"],
		"properties": {
			"plugin": {
				"type": "string",
				"description": "Installed plugin ID to inspect."
			},
			"limit": {
				"type": "integer",
				"minimum": 1,
				"maximum": 500,
				"description": "Maximum number of shadow journal entries to return (1 to 500)."
			}
		},
		"additionalProperties": false
	}`)

	return tools.Tool{
		Name:        "plugin_shadow_report",
		Title:       "Read plugin shadow report",
		Description: "Read the recorded shadow-mode journal entries for an installed plugin to see what it would have changed.",
		InputSchema: schema,
		ReadOnly:    true,
		Audience:    tools.AudienceOwner,
		Owner:       extend.Builtin,
		Execute: func(ctx context.Context, _ tools.Principal, input map[string]any) (tools.Result, error) {
			if deps.Manager == nil {
				return tools.Result{}, tools.Userf("The plugin manager is not available.")
			}
			pluginID, err := reqString(input, "plugin", 128)
			if err != nil {
				return tools.Result{}, err
			}
			limit, err := optInt(input, "limit", 100, 1, 500)
			if err != nil {
				return tools.Result{}, err
			}
			rec, err := deps.Manager.Get(ctx, pluginID)
			if err != nil {
				return tools.Result{}, tools.Userf("Plugin %q is not installed.", pluginID)
			}
			entries, err := deps.Manager.Journal(ctx, pluginID, limit, true)
			if err != nil {
				return tools.Result{}, tools.Userf("Could not read shadow journal for %q.", pluginID)
			}
			if entries == nil {
				entries = []plugins.JournalEntry{}
			}
			return tools.Result{
				Data: map[string]any{
					"plugin":  pluginID,
					"shadow":  rec.Shadow,
					"count":   len(entries),
					"entries": entries,
				},
				Links: []tools.Link{
					{Label: "Open plugin", Route: "/settings/plugins/" + pluginID},
				},
			}, nil
		},
	}
}

func pluginInstallTool(deps ToolDeps) tools.Tool {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {
				"type": "string",
				"description": "Manifest or repository URL for remote or GitHub plugins."
			},
			"kind": {
				"type": "string",
				"description": "Source kind: url, github, or studio."
			},
			"lan": {
				"type": "boolean",
				"description": "Allow private LAN addresses for remote plugins."
			},
			"manifest": {
				"description": "Manifest JSON object or JSON string when installing from Studio."
			},
			"files": {
				"type": "object",
				"description": "Map of file paths to file contents when installing from Studio."
			},
			"settings": {
				"type": "object",
				"description": "Initial plugin settings."
			},
			"shadow": {
				"type": "boolean",
				"description": "Start the plugin in shadow mode so writes are recorded without applying."
			},
			"autoUpdate": {
				"type": "boolean",
				"description": "Enable automatic updates when permissions do not increase."
			}
		},
		"additionalProperties": false
	}`)

	return tools.Tool{
		Name:        "plugin_install",
		Title:       "Install plugin",
		Description: "Preview a plugin and create an owner approval proposal to install it.",
		InputSchema: schema,
		ReadOnly:    false,
		Destructive: true,
		OpenWorld:   true,
		Audience:    tools.AudienceOwner,
		Owner:       extend.Builtin,
		Execute: func(ctx context.Context, _ tools.Principal, input map[string]any) (tools.Result, error) {
			if deps.Manager == nil {
				return tools.Result{}, tools.Userf("The plugin manager is not available.")
			}
			rawURL, err := optString(input, "url", 2048)
			if err != nil {
				return tools.Result{}, err
			}
			kindStr, err := optString(input, "kind", 32)
			if err != nil {
				return tools.Result{}, err
			}
			lan, _ := input["lan"].(bool)
			shadow, _ := input["shadow"].(bool)
			autoUpdate, _ := input["autoUpdate"].(bool)
			settings, _ := input["settings"].(map[string]any)

			src := plugins.Source{
				Kind: kindStr,
				URL:  rawURL,
				LAN:  lan,
			}
			if rawManifest, ok := input["manifest"]; ok && rawManifest != nil {
				manifestBytes, err := encodeBoundedJSON(rawManifest, maxManifestBytes, "manifest")
				if err != nil {
					return tools.Result{}, err
				}
				fileMap, err := optFileMap(input, "files")
				if err != nil {
					return tools.Result{}, err
				}
				byteFiles := make(map[string][]byte, len(fileMap))
				for k, v := range fileMap {
					byteFiles[k] = []byte(v)
				}
				src.Kind = "studio"
				src.Manifest = manifestBytes
				src.Files = byteFiles
			} else if rawURL == "" {
				return tools.Result{}, tools.Userf("Provide either a plugin URL or a Studio manifest.")
			}

			preview, err := deps.Manager.PreviewInstall(ctx, src)
			if err != nil {
				return tools.Result{}, tools.Userf("Plugin preview failed: %s", err.Error())
			}

			// Owner-facing copy: what it adds and what it can reach, in plain
			// words. Identifiers and runtimes stay out of the approval card.
			details := []tools.ProposalDetail{{Label: "What it adds", Value: plainContributions(preview.Manifest)}}
			access := "Nothing beyond its own storage"
			if len(preview.PermissionLines) > 0 {
				access = strings.Join(preview.PermissionLines, "; ")
			}
			details = append(details, tools.ProposalDetail{Label: "What it can access", Value: access})
			details = append(details, tools.ProposalDetail{Label: "Version", Value: preview.Manifest.Version})

			opts := plugins.InstallOptions{
				Grants:     plugins.Grants{LAN: lan},
				Settings:   settings,
				AutoUpdate: autoUpdate,
				Shadow:     shadow,
			}

			proposal := &tools.Proposal{
				Category:    "Install plugin",
				Description: fmt.Sprintf("Add %s to Ether.", preview.Manifest.Name),
				Details:     details,
				Input:       input,
				Apply: func(applyCtx context.Context) (tools.Result, error) {
					rec, err := deps.Manager.Install(applyCtx, preview, opts)
					if err != nil {
						return tools.Result{}, tools.Userf("Could not install plugin: %s", err.Error())
					}
					deps.Studio.NotifyInstalled(applyCtx, rec)
					return tools.Result{
						Data: map[string]any{
							"id":      rec.ID,
							"name":    rec.Name,
							"version": rec.Version,
							"enabled": rec.Enabled,
							"shadow":  rec.Shadow,
						},
						Links: []tools.Link{
							{Label: "Open plugin", Route: "/settings/plugins/" + rec.ID},
						},
					}, nil
				},
			}

			return tools.Result{
				Data: map[string]any{
					"id":          preview.Manifest.ID,
					"name":        preview.Manifest.Name,
					"version":     preview.Manifest.Version,
					"permissions": preview.PermissionLines,
					"verified":    preview.Verified,
				},
				Proposal: proposal,
			}, nil
		},
	}
}

func reqString(input map[string]any, key string, maxLen int) (string, error) {
	v, err := optString(input, key, maxLen)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", tools.Userf("Field %q is required.", key)
	}
	return v, nil
}

func optString(input map[string]any, key string, maxLen int) (string, error) {
	raw, ok := input[key]
	if !ok || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", tools.Userf("Field %q must be a string.", key)
	}
	s = strings.TrimSpace(s)
	if maxLen > 0 && len(s) > maxLen {
		return "", tools.Userf("Field %q must be at most %d characters.", key, maxLen)
	}
	return s, nil
}

func optInt(input map[string]any, key string, def, minVal, maxVal int) (int, error) {
	raw, ok := input[key]
	if !ok || raw == nil {
		return def, nil
	}
	var n int
	switch v := raw.(type) {
	case float64:
		n = int(v)
	case int:
		n = v
	case int64:
		n = int(v)
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, tools.Userf("Field %q must be an integer.", key)
		}
		n = int(i)
	default:
		return 0, tools.Userf("Field %q must be an integer.", key)
	}
	if n < minVal || n > maxVal {
		return 0, tools.Userf("Field %q must be between %d and %d.", key, minVal, maxVal)
	}
	return n, nil
}

func optStringSlice(input map[string]any, key string, maxItems, maxItemLen int) ([]string, error) {
	raw, ok := input[key]
	if !ok || raw == nil {
		return nil, nil
	}
	var out []string
	switch v := raw.(type) {
	case []string:
		out = v
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, tools.Userf("Every entry in %q must be a string.", key)
			}
			out = append(out, s)
		}
	default:
		return nil, tools.Userf("Field %q must be an array of strings.", key)
	}
	if len(out) > maxItems {
		return nil, tools.Userf("Field %q allows at most %d items.", key, maxItems)
	}
	for _, s := range out {
		if len(s) > maxItemLen {
			return nil, tools.Userf("Entries in %q must be at most %d characters.", key, maxItemLen)
		}
	}
	return out, nil
}

func optFileMap(input map[string]any, key string) (map[string]string, error) {
	raw, ok := input[key]
	if !ok || raw == nil {
		return map[string]string{}, nil
	}
	out := map[string]string{}
	total := 0
	switch m := raw.(type) {
	case map[string]string:
		if len(m) > maxFilesCount {
			return nil, tools.Userf("Field %q allows at most %d files.", key, maxFilesCount)
		}
		for k, v := range m {
			total += len(v)
			if total > maxFileBytes {
				return nil, tools.Userf("Files in %q exceed the size limit.", key)
			}
			out[k] = v
		}
	case map[string]any:
		if len(m) > maxFilesCount {
			return nil, tools.Userf("Field %q allows at most %d files.", key, maxFilesCount)
		}
		for k, v := range m {
			s, ok := v.(string)
			if !ok {
				return nil, tools.Userf("File %q in %q must be a string.", k, key)
			}
			total += len(s)
			if total > maxFileBytes {
				return nil, tools.Userf("Files in %q exceed the size limit.", key)
			}
			out[k] = s
		}
	default:
		return nil, tools.Userf("Field %q must be an object mapping file names to strings.", key)
	}
	return out, nil
}

func encodeBoundedJSON(v any, maxBytes int, field string) ([]byte, error) {
	switch val := v.(type) {
	case string:
		if len(val) > maxBytes {
			return nil, tools.Userf("Field %q exceeds the maximum size of %d bytes.", field, maxBytes)
		}
		return []byte(val), nil
	case json.RawMessage:
		if len(val) > maxBytes {
			return nil, tools.Userf("Field %q exceeds the maximum size of %d bytes.", field, maxBytes)
		}
		return val, nil
	case []byte:
		if len(val) > maxBytes {
			return nil, tools.Userf("Field %q exceeds the maximum size of %d bytes.", field, maxBytes)
		}
		return val, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, tools.Userf("Field %q is not valid JSON.", field)
		}
		if len(b) > maxBytes {
			return nil, tools.Userf("Field %q exceeds the maximum size of %d bytes.", field, maxBytes)
		}
		return b, nil
	}
}

// plainContributions says where an installed plugin shows up, for owners.
func plainContributions(m plugins.Manifest) string {
	var parts []string
	e := m.EffectiveEther()
	for _, c := range e.Capabilities {
		switch c {
		case plugins.CapLiveTV:
			parts = append(parts, "live channels in the Live TV guide")
		case plugins.CapStreamSource:
			parts = append(parts, "playable sources on catalogue titles")
		case plugins.CapListSource:
			parts = append(parts, "Home rows and collections")
		case plugins.CapSubtitles:
			parts = append(parts, "subtitles in the player")
		case plugins.CapMetadata:
			parts = append(parts, "title details")
		case plugins.CapRankingStep:
			parts = append(parts, "a step in release ranking")
		case plugins.CapNotify:
			parts = append(parts, "notifications")
		}
	}
	if len(e.Contributes.Pages) > 0 {
		parts = append(parts, fmt.Sprintf("%d console page(s)", len(e.Contributes.Pages)))
	}
	if len(parts) == 0 {
		return "Background behaviour only"
	}
	out := strings.Join(parts, ", ")
	return strings.ToUpper(out[:1]) + out[1:]
}
