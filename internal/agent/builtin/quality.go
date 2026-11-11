package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/core"
)

func qualityProfilesTool(d Deps) tools.Tool {
	return tool(tools.Tool{
		Name: "quality_profiles", Title: "Quality profiles", ReadOnly: true,
		Description: "Read quality profiles, their media, quality order (best first), custom format scores, language settings and resolution limits. Qualities in one group tie; existing Proper/Repack handling applies before scores. Use before quality_order_set or format_scores_set. Language scores cannot outrank separate higher quality groups.",
		InputSchema: schema(map[string]any{}),
		Execute: func(ctx context.Context, _ tools.Principal, _ map[string]any) (tools.Result, error) {
			profiles, err := d.Profiles.Profiles(ctx)
			if err != nil {
				return tools.Result{}, err
			}
			out := make([]any, 0, len(profiles))
			for _, p := range profiles {
				order := p.QualityOrder
				if order == nil {
					order = []core.QualityItem{}
				}
				out = append(out, map[string]any{
					"name": p.Name, "media": p.Media,
					"qualityOrder": order, "scores": p.Scores,
					"language": p.Language, "preferredLanguages": p.PreferredLanguages,
					"maxHeight": p.MaxHeight, "minHeight": p.MinHeight,
					"allowedQualities": p.AllowedQualities,
				})
			}
			return tools.Result{Data: map[string]any{"profiles": out}, Links: []tools.Link{{Label: "Open quality profiles", Route: "/quality/profiles"}}}, nil
		},
	})
}

func qualityOrderTool(d Deps) tools.Tool {
	return tool(tools.Tool{
		Name: "quality_order_set", Title: "Change quality order",
		Description: "Propose replacing one profile's quality order, best first. Qualities in one group rank equally; existing Proper/Repack handling applies before custom format scores. To let a viewer-language score prefer 1080p over 4K, put those qualities in the same group, preserving other groups. Read quality_profiles first. This does not change language scores, allowed qualities, device gates or resolution limits. An empty order ranks all qualities equally. Changes require owner approval.",
		InputSchema: schema(map[string]any{
			"profile": pString("Profile name; defaults to the first profile", 80),
			"media":   pEnum("Which media's profile, when a movie and a TV profile share the name", "movie", "tv"),
			"qualityOrder": map[string]any{"type": "array", "maxItems": 64, "description": "Quality groups, best first; an empty list makes every quality equal", "items": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"qualities"}, "properties": map[string]any{
					"name":      pString("Optional group name", 80),
					"qualities": map[string]any{"type": "array", "minItems": 1, "maxItems": 64, "items": pString("Quality id from quality_profiles, for example webdl-1080", 40)},
				},
			}},
		}, "qualityOrder"),
		Execute: func(ctx context.Context, _ tools.Principal, in map[string]any) (tools.Result, error) {
			raw, ok := in["qualityOrder"]
			if !ok || raw == nil {
				return tools.Result{}, tools.Userf("qualityOrder must be a list of quality groups")
			}
			b, err := json.Marshal(raw)
			if err != nil {
				return tools.Result{}, tools.Userf("qualityOrder must be a list of quality groups")
			}
			var order []core.QualityItem
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&order); err != nil || order == nil {
				return tools.Result{}, tools.Userf("qualityOrder must be a list of quality groups with qualities")
			}
			if msg := core.QualityOrderError(order); msg != "" {
				return tools.Result{}, tools.Userf("qualityOrder: %s", msg)
			}
			for _, item := range order {
				if len(item.Name) > 80 {
					return tools.Result{}, tools.Userf("quality group name is too long (at most 80 characters)")
				}
			}
			profile, err := d.profile(ctx, in)
			if err != nil {
				return tools.Result{}, err
			}
			return propose("Change quality order", fmt.Sprintf("Change the quality order on the %s profile (%s).", profile.Name, profile.Media),
				[]tools.ProposalDetail{{Label: "Profile", Value: profile.Name}, {Label: "Media", Value: profile.Media}, {Label: "Before", Value: describeQualityOrder(profile.QualityOrder)}, {Label: "After", Value: describeQualityOrder(order)}},
				map[string]any{"profile": profile.Name, "media": profile.Media, "qualityOrder": order},
				func(ctx context.Context) (tools.Result, error) {
					if err := d.Profiles.SetQualityOrder(ctx, profile.Name, profile.Media, order); err != nil {
						return tools.Result{}, err
					}
					return tools.Result{Data: map[string]any{"updated": true, "profile": profile.Name, "media": profile.Media}, Links: []tools.Link{{Label: "Open quality profiles", Route: "/quality/profiles"}}}, nil
				}), nil
		},
	})
}

func describeQualityOrder(order []core.QualityItem) string {
	if len(order) == 0 {
		return "All qualities rank equally"
	}
	groups := make([]string, 0, len(order))
	for _, item := range order {
		label := strings.Join(item.Qualities, " = ")
		if item.Name != "" {
			label = item.Name + " (" + label + ")"
		}
		groups = append(groups, label)
	}
	return strings.Join(groups, " > ")
}
