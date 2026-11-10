package kit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// checkMediaKinds adds the content-kind conformance notes that
// Manifest.Validate does not cover (spec §9.4). Manifest rule errors
// themselves come from Validate in Check, so the kit and the host
// always report the same field errors (lint parity); this pass only
// adds warnings about declarations that are valid but noteworthy.
func checkMediaKinds(rep *Report, man plugins.Manifest) {
	if man.Ether == nil {
		return
	}
	c := man.Ether.Contributes
	for i, k := range c.MediaKinds {
		base := fmt.Sprintf("ether.contributes.mediaKinds[%d]", i)
		if man.HasCapability(plugins.CapKindResolver) && !man.HasCapability(plugins.CapCatalogProvider) {
			rep.addWarn("kinds", base, "kind is resolver-only: nothing syncs items into its library")
		}
		if k.Present.Plex != nil && (k.Present.Plex.Base == "show" || k.Present.Plex.Base == "artist") && len(k.Levels) > 3 {
			rep.addWarn("kinds", base+".present.plex", "Plex nests three levels; deeper levels are flattened with their indexes kept")
		}
		if k.Browse.LiveSearch && !k.Browse.Search {
			rep.addWarn("kinds", base+".browse.liveSearch", "live search results are stored like synced items, but local search is off, so earlier live results will not be found again locally")
		}
		if k.Sync.Mode == plugins.KindSyncPull && k.Sync.Refresh == "" {
			rep.addWarn("kinds", base+".sync.refresh", "pull kind declares no refresh, so it syncs once at install and then only on demand")
		}
	}
	if man.HasCapability(plugins.CapFrontendAdapter) && man.Ether.Runtime.Kind == "hosted" {
		rep.addWarn("kinds", "ether.contributes.frontendAdapters", "hosted frontend adapters can only serve small responses; media bytes must come from Ether stream URLs")
	}
	if man.HasCapability(plugins.CapMatcher) && !man.HasCapability(plugins.CapKindResolver) {
		rep.addWarn("kinds", "ether.capabilities", "matcher without kind_resolver scores matches nothing resolves")
	}
}

// CheckKindUpgrade reports the non-additive declaration changes
// between two manifests (spec §2.5) as kit issues, for the Studio
// save path and catalogue CI.
func CheckKindUpgrade(prev, next plugins.Manifest) []Issue {
	kindsOf := func(m plugins.Manifest) []plugins.MediaKindMeta {
		if m.Ether == nil {
			return nil
		}
		return m.Ether.Contributes.MediaKinds
	}
	err := plugins.ValidateMediaKindsUpgrade(kindsOf(prev), kindsOf(next))
	if err == nil {
		return nil
	}
	var out []Issue
	var ve *plugins.ValidationError
	if errors.As(err, &ve) {
		for _, f := range ve.Fields {
			out = append(out, Issue{Rule: "kind-upgrade", Field: f.Field, Message: f.Message})
		}
		return out
	}
	return []Issue{{Rule: "kind-upgrade", Message: err.Error()}}
}

// ValidateKindPage checks one provider page against its kind
// declaration: page size, per-item envelope rules, and that item ids
// are not repeated inside the page. It is the offline half of the
// browse/detail conformance checks (§9.4).
func ValidateKindPage(decl plugins.MediaKindMeta, page core.KindPage, limit int) []Issue {
	var out []Issue
	if limit > 0 && len(page.Items) > limit {
		out = append(out, Issue{Rule: "kinds", Field: "items", Message: fmt.Sprintf("page holds %d items, more than the %d requested", len(page.Items), limit)})
	}
	seen := map[string]bool{}
	for i, it := range page.Items {
		if seen[it.ID] && it.ID != "" {
			out = append(out, Issue{Rule: "kinds", Field: fmt.Sprintf("items[%d].id", i), Message: "item id repeats inside one page"})
		}
		seen[it.ID] = true
		if msg := plugins.CheckKindItem(decl, it); msg != "" {
			out = append(out, Issue{Rule: "kinds", Field: fmt.Sprintf("items[%d]", i), Message: msg})
		}
	}
	return out
}

// KindCaller invokes one plugin route for CheckKindProvider: the same
// shape Manager.Call wraps, supplied by Studio preview or a test fake.
type KindCaller func(ctx context.Context, req plugins.Request) (plugins.Response, error)

var urlPlaceholderRe = regexp.MustCompile(`\{[a-zA-Z]+\}`)

var knownURLPlaceholders = map[string]bool{
	"{itemIndex}": true, "{pageNumber}": true, "{byteStart}": true, "{byteEnd}": true, "{byteRange}": true,
}

// CheckKindProvider runs a declared kind's provider through the
// scripted conformance calls (§9.4): browse must paginate and finish,
// pages must fit the limit, detail must round-trip, a declared live
// search must answer, /changes may decline (404/501 = re-walk), and a
// resolver's sources must use known patterns and URL placeholders.
// It never touches the network itself; call is the caller's transport.
func CheckKindProvider(ctx context.Context, decl plugins.MediaKindMeta, hasResolver bool, call KindCaller) []Issue {
	var out []Issue
	add := func(field, msg string) { out = append(out, Issue{Rule: "kinds", Field: field, Message: msg}) }
	post := func(path string, body any, v any) (int, bool) {
		raw, err := json.Marshal(body)
		if err != nil {
			add(path, err.Error())
			return 0, false
		}
		resp, err := call(ctx, plugins.Request{Method: http.MethodPost, Path: path, Body: raw})
		if err != nil {
			add(path, "call failed: "+err.Error())
			return 0, false
		}
		if resp.Status >= 200 && resp.Status < 300 && v != nil && len(resp.Body) > 0 {
			if err := json.Unmarshal(resp.Body, v); err != nil {
				add(path, "response is not the expected JSON: "+err.Error())
				return resp.Status, false
			}
		}
		return resp.Status, true
	}
	kindPath := "/ether/v1/kinds/" + decl.Key

	// Browse the root level with a small limit until the cursors run out.
	const limit = 3
	seenCursors := map[string]bool{}
	var roots []core.KindItem
	cursor := ""
	for pages := 0; pages < 20; pages++ {
		var page core.KindPage
		status, ok := post(kindPath+"/browse", core.KindBrowseRequest{Level: levelKey(decl, 0), Cursor: cursor, Limit: limit}, &page)
		if !ok {
			return out
		}
		if status < 200 || status >= 300 {
			add(kindPath+"/browse", fmt.Sprintf("browse answered status %d", status))
			return out
		}
		out = append(out, ValidateKindPage(decl, page, limit)...)
		roots = append(roots, page.Items...)
		if page.NextCursor == "" {
			break
		}
		if seenCursors[page.NextCursor] {
			add(kindPath+"/browse", "browse cursor repeats, so the walk never finishes")
			return out
		}
		seenCursors[page.NextCursor] = true
		cursor = page.NextCursor
		if pages == 19 {
			add(kindPath+"/browse", "browse still paginating after 20 pages at the root level")
		}
	}
	if len(roots) == 0 {
		add(kindPath+"/browse", "provider returned no root items; the conformance run needs at least one")
		return out
	}
	first := roots[0]

	// Detail of the first root.
	var detail core.KindItem
	if status, ok := post(kindPath+"/items/"+first.ID, map[string]any{}, &detail); ok {
		if status < 200 || status >= 300 {
			add(kindPath+"/items", fmt.Sprintf("detail answered status %d", status))
		} else if detail.ID != first.ID {
			add(kindPath+"/items", "detail returned a different item id than requested")
		}
	}

	// Declared live search must answer (§15.4); undeclared search may 501.
	if decl.Browse.LiveSearch {
		word := strings.Fields(first.Title)
		query := ""
		if len(word) > 0 {
			query = word[0]
		}
		var page core.KindPage
		if status, ok := post(kindPath+"/search", core.KindBrowseRequest{Query: query, Limit: limit}, &page); ok && (status < 200 || status >= 300) {
			add(kindPath+"/search", fmt.Sprintf("browse.liveSearch is set but search answered status %d", status))
		} else if ok {
			out = append(out, ValidateKindPage(decl, page, limit)...)
		}
	}

	// Changes may decline; if it answers, its upserts must validate.
	var changes core.KindChangePage
	if status, ok := post(kindPath+"/changes", map[string]any{"cursor": "", "limit": 500}, &changes); ok && status >= 200 && status < 300 {
		out = append(out, ValidateKindPage(decl, core.KindPage{Items: changes.Upserts}, 0)...)
	}

	// Resolve the first playable item seen, walking down the declared
	// levels through the first item of each level until one is playable.
	playable := findPlayable(roots)
	parent := first
	for depth := 1; playable == nil && depth < len(decl.Levels); depth++ {
		var page core.KindPage
		status, ok := post(kindPath+"/browse", core.KindBrowseRequest{ParentID: parent.ID, Level: levelKey(decl, depth), Limit: limit}, &page)
		if !ok || status < 200 || status >= 300 || len(page.Items) == 0 {
			break
		}
		out = append(out, ValidateKindPage(decl, page, limit)...)
		playable = findPlayable(page.Items)
		parent = page.Items[0]
	}
	if hasResolver {
		if playable == nil {
			add(kindPath+"/resolve", "no playable item found in the browsed sample to resolve")
		} else {
			var res core.KindResolveResponse
			body := core.KindResolveRequest{Item: *playable}
			if status, ok := post(kindPath+"/resolve", body, &res); ok {
				if status < 200 || status >= 300 {
					add(kindPath+"/resolve", fmt.Sprintf("resolve answered status %d", status))
				}
				for i, src := range res.Sources {
					out = append(out, checkPlayableSource(kindPath, i, src)...)
				}
			}
		}
	}
	return out
}

func levelKey(decl plugins.MediaKindMeta, i int) string {
	if i >= 0 && i < len(decl.Levels) {
		return decl.Levels[i].Key
	}
	return ""
}

func findPlayable(items []core.KindItem) *core.KindItem {
	for i := range items {
		if items[i].Playable {
			return &items[i]
		}
	}
	return nil
}

func checkPlayableSource(kindPath string, i int, src core.PlayableSource) []Issue {
	var out []Issue
	field := fmt.Sprintf("%s/resolve sources[%d]", kindPath, i)
	switch src.Pattern {
	case "range", "download", "direct":
		if src.URL == "" && src.BlobRef == "" {
			out = append(out, Issue{Rule: "kinds", Field: field, Message: "pattern " + src.Pattern + " needs a url or blobRef"})
		}
	case "container":
		if src.Container == nil || len(src.Container.Volumes) == 0 {
			out = append(out, Issue{Rule: "kinds", Field: field, Message: "container pattern needs container volumes"})
		} else if src.Container.Member == "" && src.Container.MemberIndex == nil {
			out = append(out, Issue{Rule: "kinds", Field: field, Message: "container pattern needs a member name or memberIndex"})
		}
	case "release":
		if src.Release == nil {
			out = append(out, Issue{Rule: "kinds", Field: field, Message: "release pattern needs a release hint"})
		}
	default:
		out = append(out, Issue{Rule: "kinds", Field: field, Message: "unknown pattern " + src.Pattern})
	}
	for _, ph := range urlPlaceholderRe.FindAllString(src.URL, -1) {
		if !knownURLPlaceholders[ph] {
			out = append(out, Issue{Rule: "kinds", Field: field, Message: "url placeholder " + ph + " is not one Ether substitutes"})
		}
	}
	return out
}
