package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/textutil"
	"net/url"
	"strings"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/releasepolicy"
	"github.com/braedonsaunders/ether/internal/resolver"
)

// propose wraps a write as an approval card. Nothing has changed when this
// returns; apply runs only after the owner approves.
func propose(category, description string, details []tools.ProposalDetail, input map[string]any, apply func(context.Context) (tools.Result, error)) tools.Result {
	return tools.Result{
		Data: map[string]any{"status": "proposed", "change": description},
		Proposal: &tools.Proposal{
			Category: category, Description: description, Details: details,
			Input: input, Apply: apply,
		},
	}
}

func writeTools(d Deps) []tools.Tool {
	steps := strings.Join(core.DefaultRankingSteps, ", ")
	return []tools.Tool{
		tool(tools.Tool{
			Name: "livetv_guide_match", Title: "Match with AI", OpenWorld: true,
			Description: "Estimate or propose AI-assisted EPG channel matches using the owner's configured LLM. Proposals never change mappings. Apply only explicitly reviewed channel ids and proposal keys. XMLTV, Xtream and Plex guides share this flow. Run estimate before propose to show calls and token budget.",
			InputSchema: schema(map[string]any{
				"guideSourceId": pString("Optional guide source to match within", 160),
				"operation":     pEnum("estimate is free; propose may call the provider; apply requires review", "estimate", "propose", "apply"),
				"channelIds":    map[string]any{"type": "array", "maxItems": 200, "items": pString("Reviewed channel id", 160)},
				"proposalKeys":  map[string]any{"type": "object", "maxProperties": 200, "additionalProperties": map[string]any{"type": "string", "maxLength": 64}},
			}, "operation"),
			Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				svc, ok := d.LiveTV.(interface {
					MatchGuides(context.Context, livetv.GuideMatchRequest) (livetv.GuideMatchResult, error)
				})
				if !ok {
					return tools.Result{}, tools.Userf("guide matching unavailable")
				}
				op, e := reqStr(in, "operation", 16)
				if e != nil {
					return tools.Result{}, e
				}
				if op != "estimate" && op != "propose" && op != "apply" {
					return tools.Result{}, tools.Userf("invalid operation")
				}
				b, e := json.Marshal(in)
				if e != nil {
					return tools.Result{}, e
				}
				var args struct {
					GuideSourceID string            `json:"guideSourceId"`
					ChannelIDs    []string          `json:"channelIds"`
					ProposalKeys  map[string]string `json:"proposalKeys"`
				}
				if e = json.Unmarshal(b, &args); e != nil {
					return tools.Result{}, e
				}
				if len(args.ChannelIDs) > 200 || len(args.ProposalKeys) > 200 {
					return tools.Result{}, tools.Userf("too many channels")
				}
				req := livetv.GuideMatchRequest{AI: op, GuideSourceID: args.GuideSourceID, ChannelIDs: args.ChannelIDs, ProposalKeys: args.ProposalKeys, Reviewed: op == "apply", DryRun: op != "apply"}
				run := func(ctx context.Context) (tools.Result, error) {
					r, e := svc.MatchGuides(ctx, req)
					if e != nil {
						return tools.Result{}, e
					}
					b, _ := json.Marshal(r)
					var data map[string]any
					_ = json.Unmarshal(b, &data)
					return tools.Result{Data: data, Links: []tools.Link{{Label: "Live TV", Route: "/livetv"}}}, nil
				}
				if op == "estimate" {
					return run(ctx)
				}
				previewReq := req
				previewReq.AI = "estimate"
				previewReq.DryRun = true
				preview, e := svc.MatchGuides(ctx, previewReq)
				if e != nil {
					return tools.Result{}, e
				}
				var details []tools.ProposalDetail
				if op == "propose" {
					if preview.AI == nil || !preview.AI.Configured {
						return tools.Result{}, tools.Userf("Set up AI in Integrations → LLM to match channels.")
					}
					details = append(details, tools.ProposalDetail{Label: "Estimated calls", Value: fmt.Sprint(preview.AI.EstimatedCalls)}, tools.ProposalDetail{Label: "Estimated input tokens", Value: fmt.Sprint(preview.AI.InputTokens)})
				} else {
					if len(args.ChannelIDs) == 0 {
						return tools.Result{}, tools.Userf("select reviewed channels first")
					}
					byID := map[string]livetv.GuideMatchChange{}
					for _, p := range preview.Changes {
						byID[p.ChannelID] = p
					}
					for _, id := range args.ChannelIDs {
						p, ok := byID[id]
						if !ok || p.ProposalKey != args.ProposalKeys[id] || p.ProposalKey == "" {
							return tools.Result{}, tools.Userf("proposal changed; review again")
						}
						details = append(details, tools.ProposalDetail{Label: p.ChannelName, Value: fmt.Sprintf("%s · %.0f%% · %s", p.GuideName, p.Confidence*100, p.Reason)})
					}
				}
				return propose("Match with AI", fmt.Sprintf("%s guide matches for %d selected channels", op, len(args.ChannelIDs)), details, in, run), nil
			},
		}),
		tool(tools.Tool{
			Name: "collection_create_from_prompt", Title: "Create a collection", OpenWorld: true,
			Description: "Draft a smart collection from a plain-language request and propose creating it. The draft is generated for the approval card; nothing is saved until the owner approves. Needs a language model configured in Ether.",
			InputSchema: schema(map[string]any{
				"prompt":    pString("Describe the collection, for example: 90s action movies rated above 7", 500),
				"mediaType": pEnum("What kind of titles", "movie", "show", "mixed"),
				// Filled by Ether with the drafted collection when an
				// approval is executed, so the owner gets the draft they
				// approved. Models must not send it.
				"spec": map[string]any{
					"type": "object", "additionalProperties": true,
					"description": "Set by Ether on approval; never send this",
				},
			}, "prompt"),
			Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				prompt, err := reqStr(in, "prompt", 500)
				if err != nil {
					return tools.Result{}, err
				}
				mediaType, err := optStr(in, "mediaType", 16)
				if err != nil {
					return tools.Result{}, err
				}
				// On approval re-execution the stored input carries the
				// drafted spec, so the owner approves what they saw.
				var spec *core.CollectionSpec
				var note string
				if raw, ok := in["spec"]; ok && raw != nil {
					encoded, err := json.Marshal(raw)
					if err != nil {
						return tools.Result{}, err
					}
					spec = &core.CollectionSpec{}
					if err := json.Unmarshal(encoded, spec); err != nil {
						return tools.Result{}, tools.Userf("the stored collection draft could not be read")
					}
				} else {
					spec, note, err = d.Collections.Generate(ctx, prompt, mediaType)
					if err != nil {
						var nc interface{ NotConfigured() bool }
						if errors.As(err, &nc) && nc.NotConfigured() {
							return tools.Result{}, tools.Userf("no language model is configured, so a collection cannot be drafted. The owner can add one under Language model in the console.")
						}
						return tools.Result{}, err
					}
					if spec == nil {
						return tools.Result{}, tools.Userf("the draft came back empty; try describing the collection differently")
					}
				}
				specJSON, err := json.Marshal(spec)
				if err != nil {
					return tools.Result{}, err
				}
				var specMap map[string]any
				if err := json.Unmarshal(specJSON, &specMap); err != nil {
					return tools.Result{}, err
				}
				desc := fmt.Sprintf("Create the collection %q from the request: %s", spec.Name, prompt)
				details := []tools.ProposalDetail{
					{Label: "Name", Value: spec.Name},
					{Label: "Request", Value: truncate(prompt, 200)},
				}
				if note != "" {
					details = append(details, tools.ProposalDetail{Label: "Notes", Value: truncate(note, 300)})
				}
				saved := *spec
				return propose("Create a collection", desc, details,
					map[string]any{"prompt": prompt, "mediaType": mediaType, "spec": specMap},
					func(ctx context.Context) (tools.Result, error) {
						created, err := d.Collections.CreateSpec(ctx, saved)
						if err != nil {
							return tools.Result{}, err
						}
						return tools.Result{Data: map[string]any{
							"created": true, "name": created.Name, "id": created.ID,
						}, Links: []tools.Link{{Label: "Open Collections", Route: "/collections"}}}, nil
					}), nil
			},
		}),
		tool(tools.Tool{
			Name: "release_blocklist", Title: "Blocklist a release", Destructive: true, OpenWorld: true,
			Description: "Propose blocklisting one release so Ether never picks it again. Give a releaseId from resolve_trace and a short reason.",
			InputSchema: schema(map[string]any{
				"releaseId": pString("A release id from resolve_trace", 128),
				"reason":    pString("Why it should never be picked again", 200),
			}, "releaseId"),
			Execute: func(_ context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				id, err := reqStr(in, "releaseId", 128)
				if err != nil {
					return tools.Result{}, err
				}
				reason, err := optStr(in, "reason", 200)
				if err != nil {
					return tools.Result{}, err
				}
				rel, ok := d.Resolver.LookupRelease(id)
				if !ok {
					return tools.Result{}, tools.Userf("that release is not among recent search results; find it again with resolve_trace")
				}
				req := resolver.BlockRequest{ReleaseID: id, Name: rel.Name, Indexer: rel.Indexer}
				if reason == "" {
					reason = "Blocklisted at the owner's request"
				}
				return propose("Blocklist a release",
					fmt.Sprintf("Never pick %s again.", truncate(rel.Name, 160)),
					[]tools.ProposalDetail{
						{Label: "Release", Value: truncate(rel.Name, 200)},
						{Label: "Source", Value: rel.Indexer},
						{Label: "Reason", Value: reason},
					},
					map[string]any{"releaseId": id, "reason": reason},
					func(ctx context.Context) (tools.Result, error) {
						if _, err := d.Resolver.Block(ctx, req, reason); err != nil {
							return tools.Result{}, err
						}
						return tools.Result{Data: map[string]any{"blocklisted": true, "releaseId": id}}, nil
					}), nil
			},
		}),
		tool(tools.Tool{
			Name: "release_pin", Title: "Pin a release", OpenWorld: true,
			Description: "Propose pinning one release as the pick for a title, so Ether uses it first every time. Give titleKey and releaseId.",
			InputSchema: schema(map[string]any{
				"titleKey":  pString("The title to pin for", 32),
				"releaseId": pString("A release id from resolve_trace", 128),
				"note":      pString("Optional note for the owner", 200),
			}, "titleKey", "releaseId"),
			Execute: func(_ context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				key, err := keyArg(in, "titleKey")
				if err != nil {
					return tools.Result{}, err
				}
				id, err := reqStr(in, "releaseId", 128)
				if err != nil {
					return tools.Result{}, err
				}
				note, err := optStr(in, "note", 200)
				if err != nil {
					return tools.Result{}, err
				}
				rel, ok := d.Resolver.LookupRelease(id)
				if !ok {
					return tools.Result{}, tools.Userf("that release is not among recent search results; find it again with resolve_trace")
				}
				return propose("Pin a release",
					fmt.Sprintf("Always pick %s first for this title.", truncate(rel.Name, 160)),
					[]tools.ProposalDetail{
						{Label: "Release", Value: truncate(rel.Name, 200)},
						{Label: "Source", Value: rel.Indexer},
					},
					map[string]any{"titleKey": fmt.Sprint(int64(key)), "releaseId": id, "note": note},
					func(ctx context.Context) (tools.Result, error) {
						if err := d.Resolver.PinRelease(ctx, key, id, note); err != nil {
							return tools.Result{}, err
						}
						return tools.Result{Data: map[string]any{"pinned": true, "releaseId": id}}, nil
					}), nil
			},
		}),
		tool(tools.Tool{
			Name: "ranking_reorder", Title: "Reorder release ranking",
			Description: fmt.Sprintf("Propose a new order for a quality profile's ranking steps. Send the full list. Built-in steps: %s. Prefer-rule steps look like rule:<id>. The list must include connection and verified. Omit profile for the first (default) profile.", steps),
			InputSchema: schema(map[string]any{
				"profile": pString("Profile name; defaults to the first profile", 80),
				"media":   pEnum("Which media's profile, when a movie and a TV profile share the name", "movie", "tv"),
				"ranking": pStrings("The full ranking order, most important first", 32, 80),
			}, "ranking"),
			Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				ranking, err := stringsArg(in, "ranking", 32, 80)
				if err != nil {
					return tools.Result{}, err
				}
				if len(ranking) == 0 {
					return tools.Result{}, tools.Userf("ranking is required")
				}
				profile, err := d.profile(ctx, in)
				if err != nil {
					return tools.Result{}, err
				}
				if err := core.ValidateRanking(ranking, profile.Rules); err != nil {
					return tools.Result{}, tools.Userf("that ranking is not valid: %s", err.Error())
				}
				current := profile.Ranking
				if len(current) == 0 {
					current = core.DefaultRanking(profile.Rules)
				}
				return propose("Reorder release ranking",
					fmt.Sprintf("Change what matters most on the %s profile.", profile.Name),
					[]tools.ProposalDetail{
						{Label: "Profile", Value: profile.Name},
						{Label: "Now", Value: strings.Join(current, " → ")},
						{Label: "Proposed", Value: strings.Join(ranking, " → ")},
					},
					map[string]any{"profile": profile.Name, "ranking": ranking},
					func(ctx context.Context) (tools.Result, error) {
						if err := d.Profiles.SetRanking(ctx, profile.Name, profile.Media, ranking); err != nil {
							return tools.Result{}, err
						}
						return tools.Result{Data: map[string]any{"updated": true, "profile": profile.Name},
							Links: []tools.Link{{Label: "Open Releases", Route: "/releases"}}}, nil
					}), nil
			},
		}),
		tool(tools.Tool{
			Name: "rule_add", Title: "Add a release rule", OpenWorld: true,
			Description: "Propose adding one release rule to a quality profile. Action: require (only releases matching), avoid (never matching) or prefer (rank matching higher). Conditions test release fields such as resolution, codec, source, group, language or name. The rule is checked before it is proposed.",
			InputSchema: schema(map[string]any{
				"profile":  pString("Profile name; defaults to the first profile", 80),
				"media":    pEnum("Which media's profile, when a movie and a TV profile share the name", "movie", "tv"),
				"name":     pString("A short name for the rule", 80),
				"action":   pEnum("What the rule does", "require", "prefer", "avoid"),
				"match":    pEnum("all: every condition; any: at least one", "all", "any"),
				"disabled": pBool("Add the rule switched off"),
				"conditions": map[string]any{
					"type": "array", "maxItems": 8, "minItems": 1,
					"description": "Tests on a release",
					"items": map[string]any{
						"type": "object", "additionalProperties": false, "required": []string{"field"},
						"properties": map[string]any{
							"field":    pString("Release field, for example resolution, codec, source, group, language, name, size", 40),
							"operator": pEnum("How to compare", "equals", "contains", "regex", "range"),
							"value":    pString("Value to compare with", 120),
							"min":      map[string]any{"type": "number", "description": "Range low end"},
							"max":      map[string]any{"type": "number", "description": "Range high end"},
						},
					},
				},
			}, "name", "action", "conditions"),
			Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				name, err := reqStr(in, "name", 80)
				if err != nil {
					return tools.Result{}, err
				}
				action, err := reqStr(in, "action", 16)
				if err != nil {
					return tools.Result{}, err
				}
				switch action {
				case "require", "prefer", "avoid":
				default:
					return tools.Result{}, tools.Userf("action must be require, prefer or avoid")
				}
				match, _ := optStr(in, "match", 8)
				if match != "" && match != "all" && match != "any" {
					return tools.Result{}, tools.Userf("match must be all or any")
				}
				conditions, err := conditionsArg(in)
				if err != nil {
					return tools.Result{}, err
				}
				disabled, err := boolArg(in, "disabled", false)
				if err != nil {
					return tools.Result{}, err
				}
				profile, err := d.profile(ctx, in)
				if err != nil {
					return tools.Result{}, err
				}
				rule := core.ReleaseRule{
					ID: slug(name), Name: name, Action: action, Match: match,
					Conditions: conditions, Disabled: disabled,
				}
				rules := append(append([]core.ReleaseRule{}, profile.Rules...), rule)
				if _, err := releasepolicy.Compile(rules); err != nil {
					return tools.Result{}, tools.Userf("that rule is not valid: %s", err.Error())
				}
				conds := make([]any, 0, len(conditions))
				labels := make([]string, 0, len(conditions))
				for _, c := range conditions {
					labels = append(labels, strings.TrimSpace(c.Field+" "+c.Operator+" "+c.Value))
					conds = append(conds, map[string]any{
						"field": c.Field, "operator": c.Operator, "value": c.Value, "min": c.Min, "max": c.Max,
					})
				}
				return propose("Add a release rule",
					fmt.Sprintf("Add the rule %q (%s) to the %s profile.", name, action, profile.Name),
					[]tools.ProposalDetail{
						{Label: "Profile", Value: profile.Name},
						{Label: "Rule", Value: name},
						{Label: "Action", Value: action},
						{Label: "When", Value: strings.Join(labels, "; ")},
					},
					map[string]any{"profile": profile.Name, "name": name, "action": action, "match": match, "disabled": disabled, "conditions": conds},
					func(ctx context.Context) (tools.Result, error) {
						if err := d.Profiles.AddRule(ctx, profile.Name, profile.Media, rule); err != nil {
							return tools.Result{}, err
						}
						return tools.Result{Data: map[string]any{"added": true, "profile": profile.Name},
							Links: []tools.Link{{Label: "Open Releases", Route: "/releases"}}}, nil
					}), nil
			},
		}),
		tool(tools.Tool{
			Name: "source_add_stremio", Title: "Add a plugin from Stremio", OpenWorld: true,
			Description: "Ask the owner for a Stremio add-on URL and add it as a source. The URL is collected through a secret card because add-on URLs can carry account keys; never ask for it in chat. Over MCP this waits for approval and a secret card in the console.",
			InputSchema: schema(map[string]any{}),
			Execute: func(_ context.Context, _ tools.Principal, _ map[string]any) (tools.Result, error) {
				return tools.Result{
					Data: map[string]any{"status": "secret_required"},
					Secret: &tools.SecretRequest{
						ProviderLabel:   "Stremio add-on",
						CredentialLabel: "Add-on URL",
						Purpose:         "Adds this Stremio add-on as a release source. The address is stored securely and never shown in chat.",
						Store: func(ctx context.Context, value string) error {
							value = strings.TrimSpace(value)
							u, err := url.Parse(value)
							if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
								return tools.Userf("that is not a web address")
							}
							_, err = d.Sources.AddStremio(ctx, value)
							return err
						},
					},
				}, nil
			},
		}),
		tool(tools.Tool{
			Name: "source_add_m3u", Title: "Add a playlist source", OpenWorld: true,
			Description: "Propose adding an M3U playlist (and optional XMLTV guide) as a Live TV source. Avoid pasting addresses that contain passwords; propose only what the owner gave you.",
			InputSchema: schema(map[string]any{
				"name":        pString("A name for the source", 80),
				"playlistUrl": pString("The M3U playlist address", 600),
				"guideUrl":    pString("Optional XMLTV guide address", 600),
			}, "name", "playlistUrl"),
			Execute: func(_ context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				name, err := reqStr(in, "name", 80)
				if err != nil {
					return tools.Result{}, err
				}
				playlistURL, err := reqStr(in, "playlistUrl", 600)
				if err != nil {
					return tools.Result{}, err
				}
				guideURL, err := optStr(in, "guideUrl", 600)
				if err != nil {
					return tools.Result{}, err
				}
				for _, raw := range []string{playlistURL, guideURL} {
					if raw == "" {
						continue
					}
					u, err := url.Parse(raw)
					if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
						return tools.Result{}, tools.Userf("addresses must be http or https web addresses")
					}
				}
				src := livetv.Source{Name: name, PlaylistURL: playlistURL, GuideURL: guideURL}
				return propose("Add a playlist source",
					fmt.Sprintf("Add the Live TV source %q from %s.", name, hostOf(playlistURL)),
					[]tools.ProposalDetail{
						{Label: "Name", Value: name},
						{Label: "Playlist host", Value: hostOf(playlistURL)},
						{Label: "Guide host", Value: hostOf(guideURL)},
					},
					map[string]any{"name": name, "playlistUrl": playlistURL, "guideUrl": guideURL},
					func(ctx context.Context) (tools.Result, error) {
						added, err := d.LiveTV.ImportSource(ctx, src)
						if err != nil {
							return tools.Result{}, err
						}
						return tools.Result{Data: map[string]any{
							"added": true, "sourceId": added.ID, "channels": added.ChannelCount,
						}, Links: []tools.Link{{Label: "Open Live TV", Route: "/livetv"}}}, nil
					}), nil
			},
		}),
		tool(tools.Tool{
			Name: "frontend_enable", Title: "Turn a frontend on or off",
			Description: "Propose turning a client frontend (such as Jellyfin, Emby or Stremio) on or off. Call with no frontendId to list the frontends and their state first.",
			InputSchema: schema(map[string]any{
				"frontendId": pString("A frontend id, for example jellyfin", 40),
				"enabled":    pBool("True to turn it on, false to turn it off"),
			}),
			Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				frontends, err := d.Frontends.Frontends(ctx)
				if err != nil {
					return tools.Result{}, err
				}
				id, err := optStr(in, "frontendId", 40)
				if err != nil {
					return tools.Result{}, err
				}
				if id == "" {
					out := make([]any, 0, len(frontends))
					for _, f := range frontends {
						out = append(out, map[string]any{"id": f.ID, "name": f.Name, "enabled": f.Enabled})
					}
					return tools.Result{Data: map[string]any{"frontends": out}}, nil
				}
				enabled, err := boolArg(in, "enabled", true)
				if err != nil {
					return tools.Result{}, err
				}
				var found *FrontendInfo
				for i := range frontends {
					if strings.EqualFold(frontends[i].ID, id) {
						found = &frontends[i]
						break
					}
				}
				if found == nil {
					return tools.Result{}, tools.Userf("no frontend with that id; call frontend_enable with no frontendId to list them")
				}
				if found.Enabled == enabled {
					return tools.Result{Data: map[string]any{"changed": false, "enabled": enabled}}, nil
				}
				onOff := map[bool]string{true: "on", false: "off"}[enabled]
				feID := found.ID
				return propose("Turn a frontend "+onOff,
					fmt.Sprintf("Turn the %s frontend %s.", found.Name, onOff),
					[]tools.ProposalDetail{{Label: "Frontend", Value: found.Name}, {Label: "Turn", Value: onOff}},
					map[string]any{"frontendId": feID, "enabled": enabled},
					func(ctx context.Context) (tools.Result, error) {
						if err := d.Frontends.SetFrontendEnabled(ctx, feID, enabled); err != nil {
							return tools.Result{}, err
						}
						return tools.Result{Data: map[string]any{"changed": true, "enabled": enabled}}, nil
					}), nil
			},
		}),
		tool(tools.Tool{
			Name: "plugin_enable", Title: "Turn a plugin on or off",
			Description: "Propose turning an installed plugin on or off.",
			InputSchema: schema(map[string]any{
				"pluginId": pString("A plugin id from plugins_list", 128),
				"enabled":  pBool("True to turn it on, false to turn it off"),
			}, "pluginId"),
			Execute: func(_ context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				if d.Plugins == nil {
					return tools.Result{}, tools.Userf(pluginsUnavailable)
				}
				id, err := reqStr(in, "pluginId", 128)
				if err != nil {
					return tools.Result{}, err
				}
				enabled, err := boolArg(in, "enabled", true)
				if err != nil {
					return tools.Result{}, err
				}
				onOff := map[bool]string{true: "on", false: "off"}[enabled]
				return propose("Turn a plugin "+onOff,
					fmt.Sprintf("Turn the plugin %s %s.", id, onOff),
					[]tools.ProposalDetail{{Label: "Plugin", Value: id}, {Label: "Turn", Value: onOff}},
					map[string]any{"pluginId": id, "enabled": enabled},
					func(ctx context.Context) (tools.Result, error) {
						if err := d.Plugins.SetPluginEnabled(ctx, id, enabled); err != nil {
							return tools.Result{}, err
						}
						return tools.Result{Data: map[string]any{"changed": true, "enabled": enabled}}, nil
					}), nil
			},
		}),
		tool(tools.Tool{
			Name: "server_restart", Title: "Restart the server", Destructive: true,
			Description: "Propose restarting Ether. Prefer whenIdle so nobody's playback is interrupted. The server comes back on its own; this tool's approval happens first.",
			InputSchema: schema(map[string]any{
				"whenIdle": pBool("Wait until nothing is playing"),
			}),
			Execute: func(_ context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
				whenIdle, err := boolArg(in, "whenIdle", true)
				if err != nil {
					return tools.Result{}, err
				}
				desc := "Restart Ether now."
				if whenIdle {
					desc = "Restart Ether once nothing is playing."
				}
				return propose("Restart the server", desc, nil,
					map[string]any{"whenIdle": whenIdle},
					func(ctx context.Context) (tools.Result, error) {
						if err := d.Server.Restart(ctx, whenIdle); err != nil {
							return tools.Result{}, err
						}
						return tools.Result{Data: map[string]any{"restarting": true, "whenIdle": whenIdle}}, nil
					}), nil
			},
		}),
	}
}

// profile resolves the optional "profile" argument to a configured profile.
func (d Deps) profile(ctx context.Context, in map[string]any) (ProfileInfo, error) {
	name, err := optStr(in, "profile", 80)
	if err != nil {
		return ProfileInfo{}, err
	}
	profiles, err := d.Profiles.Profiles(ctx)
	if err != nil {
		return ProfileInfo{}, err
	}
	if len(profiles) == 0 {
		return ProfileInfo{}, tools.Userf("no quality profiles are configured")
	}
	media, err := optStr(in, "media", 8)
	if err != nil {
		return ProfileInfo{}, err
	}
	if name == "" && media == "" {
		return profiles[0], nil
	}
	var matches []ProfileInfo
	for _, p := range profiles {
		if (name == "" || strings.EqualFold(p.Name, name)) && (media == "" || p.Media == media) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 1 || (name == "" && len(matches) > 0) {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return ProfileInfo{}, tools.Userf("%q exists for movies and for TV shows; set media to \"movie\" or \"tv\"", name)
	}
	names := make([]string, len(profiles))
	for i, p := range profiles {
		names[i] = p.Name
	}
	return ProfileInfo{}, tools.Userf("no profile named %q; profiles are: %s", name, strings.Join(names, ", "))
}

func conditionsArg(in map[string]any) ([]core.ReleaseCondition, error) {
	raw, ok := in["conditions"].([]any)
	if !ok || len(raw) == 0 {
		encoded, err := json.Marshal(in["conditions"])
		if err != nil {
			return nil, tools.Userf("conditions must be a list of tests")
		}
		var conds []core.ReleaseCondition
		if err := json.Unmarshal(encoded, &conds); err != nil || len(conds) == 0 {
			return nil, tools.Userf("conditions must be a list of tests with a field each")
		}
		return validConditions(conds)
	}
	if len(raw) > 8 {
		return nil, tools.Userf("a rule can have at most 8 conditions")
	}
	out := make([]core.ReleaseCondition, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, tools.Userf("each condition must be an object with a field")
		}
		field, err := reqStr(m, "field", 40)
		if err != nil {
			return nil, err
		}
		c := core.ReleaseCondition{Field: field}
		c.Operator, _ = optStr(m, "operator", 16)
		c.Value, err = optStr(m, "value", 120)
		if err != nil {
			return nil, err
		}
		if f, ok, err := num(m, "min"); err == nil && ok {
			c.Min = f
		} else if err != nil {
			return nil, err
		}
		if f, ok, err := num(m, "max"); err == nil && ok {
			c.Max = f
		} else if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return validConditions(out)
}

func validConditions(conds []core.ReleaseCondition) ([]core.ReleaseCondition, error) {
	if len(conds) > 8 {
		return nil, tools.Userf("a rule can have at most 8 conditions")
	}
	for _, c := range conds {
		if strings.TrimSpace(c.Field) == "" || len(c.Field) > 40 || len(c.Value) > 120 {
			return nil, tools.Userf("each condition needs a field and a short value")
		}
		switch c.Operator {
		case "", "equals", "contains", "regex", "range":
		default:
			return nil, tools.Userf("condition operator must be equals, contains, regex or range")
		}
	}
	return conds, nil
}

func slug(name string) string {
	return textutil.Slug(name, "rule", 48)
}

// hostOf returns a bare host for display; "" for empty input. Credentials
// and paths (which can carry keys) are dropped.
func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(unreadable address)"
	}
	return u.Host
}
