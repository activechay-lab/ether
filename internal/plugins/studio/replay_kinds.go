package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/kindstream"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/kit"
)

// KindReplayReport is the Studio kind preview (§9.3): for every kind
// the draft declares, the sampled hierarchy, how each protocol will
// show it, the resolver dry-run for one playable leaf, live-search
// state, and the kit conformance issues. It is plain data the LiveStage
// renders; no bytes are fetched and nothing is stored.
type KindReplayReport struct {
	Kinds []KindPreview `json:"kinds"`
}

// KindPreview is one declared kind's preview.
type KindPreview struct {
	Key         string             `json:"key"`
	Label       string             `json:"label"`
	LibraryName string             `json:"libraryName"`
	Levels      []string           `json:"levels"`
	SyncMode    string             `json:"syncMode"`
	LiveSearch  bool               `json:"liveSearch"`
	Sample      []KindSampleItem   `json:"sample"`
	Projections []KindProjection   `json:"projections"`
	DryRun      *KindResolveDryRun `json:"dryRun,omitempty"`
	Issues      []kit.Issue        `json:"issues,omitempty"`
}

// KindSampleItem is one browsed item flattened with its depth.
type KindSampleItem struct {
	ID       string `json:"id"`
	Level    string `json:"level"`
	ParentID string `json:"parentId,omitempty"`
	Title    string `json:"title"`
	Playable bool   `json:"playable,omitempty"`
}

// KindProjection says, in owner words, how one protocol shows the kind.
type KindProjection struct {
	Protocol string `json:"protocol"` // plex|mediaBrowser|stremio|xtream|silo|adapter
	Summary  string `json:"summary"`
}

// KindResolveDryRun resolves one playable sample leaf without fetching.
type KindResolveDryRun struct {
	ItemID  string             `json:"itemId"`
	Title   string             `json:"title"`
	Sources []KindDryRunSource `json:"sources"`
}

// KindDryRunSource is one resolver source with its template expanded.
type KindDryRunSource struct {
	ID       string `json:"id,omitempty"`
	Pattern  string `json:"pattern"`
	MIME     string `json:"mime"`
	URL      string `json:"url,omitempty"`      // as returned (may carry placeholders)
	Expanded string `json:"expanded,omitempty"` // placeholders filled for this item
	Member   string `json:"member,omitempty"`   // container member, when named
}

const maxKindSampleItems = 25

func (r *Runner) replayKinds(ctx context.Context, target *resolvedTarget, _ ReplayParams) (*KindReplayReport, error) {
	eb := target.manifest.EffectiveEther()
	var decls []plugins.MediaKindMeta
	if eb != nil {
		decls = eb.Contributes.MediaKinds
	}
	if len(decls) == 0 {
		return nil, fmt.Errorf("this plugin declares no media kinds")
	}
	hasResolver := target.manifest.HasCapability(plugins.CapKindResolver)
	report := &KindReplayReport{}
	for _, decl := range decls {
		preview := KindPreview{
			Key: decl.Key, Label: decl.Label, LibraryName: decl.Library.Name,
			SyncMode: decl.Sync.Mode, LiveSearch: decl.Browse.LiveSearch,
			Projections: kindProjections(decl, target.manifest),
		}
		for _, lv := range decl.Levels {
			preview.Levels = append(preview.Levels, lv.Key)
		}
		call := func(ctx context.Context, req plugins.Request) (plugins.Response, error) {
			resp, _, err := target.call(ctx, req)
			return resp, err
		}
		preview.Issues = kit.CheckKindProvider(ctx, decl, hasResolver, call)
		sample, playable := r.sampleKind(ctx, target, decl)
		preview.Sample = sample
		if hasResolver && playable != nil {
			preview.DryRun = r.dryRunResolve(ctx, target, decl, *playable)
		}
		report.Kinds = append(report.Kinds, preview)
	}
	return report, nil
}

// sampleKind browses roots and the first root's children, bounded.
func (r *Runner) sampleKind(ctx context.Context, target *resolvedTarget, decl plugins.MediaKindMeta) ([]KindSampleItem, *core.KindItem) {
	browse := func(parentID, level string) []core.KindItem {
		body, _ := json.Marshal(core.KindBrowseRequest{ParentID: parentID, Level: level, Limit: 10})
		resp, _, err := target.call(ctx, plugins.Request{
			Method: http.MethodPost, Path: "/ether/v1/kinds/" + decl.Key + "/browse", Body: body,
		})
		if err != nil || resp.Status < 200 || resp.Status >= 300 {
			return nil
		}
		var page core.KindPage
		if json.Unmarshal(resp.Body, &page) != nil {
			return nil
		}
		return page.Items
	}
	var out []KindSampleItem
	var playable *core.KindItem
	add := func(items []core.KindItem) {
		for i := range items {
			if len(out) >= maxKindSampleItems {
				return
			}
			it := items[i]
			out = append(out, KindSampleItem{ID: it.ID, Level: it.Level, ParentID: it.ParentID, Title: it.Title, Playable: it.Playable})
			if playable == nil && it.Playable {
				cp := it
				playable = &cp
			}
		}
	}
	if len(decl.Levels) == 0 {
		return out, nil
	}
	roots := browse("", decl.Levels[0].Key)
	add(roots)
	if len(decl.Levels) > 1 && len(roots) > 0 {
		children := browse(roots[0].ID, decl.Levels[1].Key)
		add(children)
		if len(decl.Levels) > 2 && len(children) > 0 && playable == nil {
			add(browse(children[0].ID, decl.Levels[2].Key))
		}
	}
	return out, playable
}

func (r *Runner) dryRunResolve(ctx context.Context, target *resolvedTarget, decl plugins.MediaKindMeta, item core.KindItem) *KindResolveDryRun {
	body, _ := json.Marshal(core.KindResolveRequest{Item: item})
	resp, _, err := target.call(ctx, plugins.Request{
		Method: http.MethodPost, Path: "/ether/v1/kinds/" + decl.Key + "/resolve", Body: body,
	})
	if err != nil || resp.Status < 200 || resp.Status >= 300 {
		return nil
	}
	var res core.KindResolveResponse
	if json.Unmarshal(resp.Body, &res) != nil {
		return nil
	}
	out := &KindResolveDryRun{ItemID: item.ID, Title: item.Title}
	for _, src := range res.Sources {
		ds := KindDryRunSource{ID: src.ID, Pattern: src.Pattern, MIME: src.MIME, URL: src.URL}
		if src.URL != "" {
			if tmpl, err := kindstream.ParseURLTemplate(src.URL, nil); err == nil {
				if expanded, err := tmpl.Expand(kindstream.TemplateVars{ItemIndex: item.Index}, nil); err == nil {
					ds.Expanded = expanded
				}
			}
		}
		if src.Container != nil {
			ds.Member = src.Container.Member
			if ds.Member == "" && src.Container.MemberIndex != nil {
				ds.Member = fmt.Sprintf("member %d", *src.Container.MemberIndex)
			}
		}
		out.Sources = append(out.Sources, ds)
	}
	return out
}

// kindProjections turns a Presentations declaration into owner words.
func kindProjections(decl plugins.MediaKindMeta, man plugins.Manifest) []KindProjection {
	var out []KindProjection
	levels := len(decl.Levels)
	p := decl.Present
	if p.Plex != nil {
		summary := fmt.Sprintf("Shows as a %s library", plexBaseName(p.Plex.Base))
		if (p.Plex.Base == "show" || p.Plex.Base == "artist") && levels > 3 {
			summary += fmt.Sprintf("; Plex nests three levels, so level %s and deeper are flattened with their order kept", decl.Levels[3].Key)
		}
		out = append(out, KindProjection{Protocol: "plex", Summary: summary})
	}
	if p.MediaBrowser != nil {
		out = append(out, KindProjection{Protocol: "mediaBrowser",
			Summary: fmt.Sprintf("Shows as a %s library in Jellyfin and Emby apps", p.MediaBrowser.CollectionType)})
	}
	if p.Stremio != nil {
		out = append(out, KindProjection{Protocol: "stremio", Summary: fmt.Sprintf("Adds a %s catalogue to the Stremio add-on", p.Stremio.Type)})
	}
	if p.Xtream != nil {
		out = append(out, KindProjection{Protocol: "xtream", Summary: fmt.Sprintf("Adds a %s category for Xtream apps", p.Xtream.Category)})
	}
	if p.Silo != nil {
		out = append(out, KindProjection{Protocol: "silo", Summary: fmt.Sprintf("Shows as a %s library in Silo", p.Silo.Type)})
	}
	if p.AdapterOnly || (len(out) == 0 && man.HasCapability(plugins.CapFrontendAdapter)) {
		out = append(out, KindProjection{Protocol: "adapter", Summary: "Served only through the plugin's own app surface"})
	}
	if len(out) == 0 {
		out = append(out, KindProjection{Protocol: "adapter", Summary: "Not shown in apps yet; pick where this library appears"})
	}
	return out
}

func plexBaseName(base string) string {
	switch strings.ToLower(base) {
	case "movie":
		return "Movies"
	case "show":
		return "TV Shows"
	case "artist":
		return "Music"
	case "photo":
		return "Photos"
	default:
		return base
	}
}
