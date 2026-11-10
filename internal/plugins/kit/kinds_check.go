package kit

import (
	"errors"
	"fmt"

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
func ValidateKindPage(decl plugins.MediaKindMeta, page plugins.KindPage, limit int) []Issue {
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
