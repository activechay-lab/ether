package frontend

import (
	"context"
	"hash/fnv"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plexmeta"
)

// This file holds the session/experience host contracts protocol adapters
// (Plex included) use instead of importing internal/sessions or
// internal/experience. Implementations of the pure policy helpers live here;
// stateful services stay in their domain packages and are wired by the app.

// AccessPolicy adds owner-defined restrictions to the mandatory title guard.
type AccessPolicy interface {
	Allowed(*core.User, *core.Title) bool
}

// TitleAllowed applies the mandatory content guards and the owner's policy.
func TitleAllowed(policy AccessPolicy, viewer *core.User, title *core.Title) bool {
	if title == nil || title.Adult {
		return false
	}
	if viewer != nil && contentrating.Exceeds(title.ContentRating, viewer.MaxContentRating) {
		return false
	}
	return policy == nil || policy.Allowed(viewer, title)
}

// SectionAllowed applies a viewer's library restrictions to an existing section.
// An empty restriction list means all sections, as it does for an owner.
func SectionAllowed(viewer *core.User, id int) bool {
	if viewer == nil || viewer.IsOwner || len(viewer.AllowedSections) == 0 {
		return true
	}
	for _, allowed := range viewer.AllowedSections {
		if allowed == id {
			return true
		}
	}
	return false
}

// Visibility applies content, library and inherited show restrictions. The
// section lookup returns false for media types with no configured library.
type Visibility struct {
	Policy          AccessPolicy
	SectionFor      func(core.MediaType) (int, bool)
	GetTitle        func(context.Context, core.RatingKey) (*core.Title, error)
	FilterAvailable func(context.Context, []core.Title) []core.Title
	// JudgedOnFacts returns, for one request, whether FilterAvailable judges
	// a title on its full facts rather than its row. Nil with FilterAvailable
	// set means every title may be.
	JudgedOnFacts func(context.Context) func(core.Title) bool
	Sessions      func() []*core.PlaySession
}

// TitleFacts is what the access policy reads of a title beyond its row for
// viewer (core.TitleFacts). A policy that does not say may read any of them.
func (v Visibility) TitleFacts(viewer *core.User) core.TitleFacts {
	if v.Policy == nil {
		return 0
	}
	if p, ok := v.Policy.(interface {
		TitleFacts(*core.User) core.TitleFacts
	}); ok {
		return p.TitleFacts(viewer)
	}
	return core.VisibilityFacts
}

// FactJudge is JudgedOnFacts for one request: nil when no title is,
// false when the visibility cannot tell.
func (v Visibility) FactJudge(ctx context.Context) (func(core.Title) bool, bool) {
	if v.FilterAvailable == nil {
		return nil, true
	}
	if v.JudgedOnFacts == nil {
		return nil, false
	}
	return v.JudgedOnFacts(ctx), true
}

func (v Visibility) TitleAllowed(viewer *core.User, title *core.Title) bool {
	if !TitleAllowed(v.Policy, viewer, title) {
		return false
	}
	if v.SectionFor == nil {
		return true
	}
	id, ok := v.SectionFor(title.Type)
	return !ok || SectionAllowed(viewer, id)
}

// Playable checks inherited show policy without requiring discovery evidence.
func (v Visibility) Playable(ctx context.Context, viewer *core.User, title *core.Title) bool {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if !v.TitleAllowed(viewer, title) {
		return false
	}
	if title.ShowKey == 0 {
		return true
	}
	if v.GetTitle == nil {
		return false
	}
	show, err := v.GetTitle(ctx, title.ShowKey)
	return err == nil && v.TitleAllowed(viewer, show)
}

// Visible includes availability in addition to the playback policy.
func (v Visibility) Visible(ctx context.Context, viewer *core.User, title *core.Title) bool {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if !v.TitleAllowed(viewer, title) {
		return false
	}
	if len(v.Available(ctx, viewer, []core.Title{*title})) == 0 {
		return false
	}
	return v.Playable(ctx, viewer, title)
}

// Available filters discovery evidence, retaining a viewer's open sources.
// Callers must separately enforce title policy.
func (v Visibility) Available(ctx context.Context, viewer *core.User, titles []core.Title) []core.Title {
	if policy, ok := v.Policy.(interface {
		DiscoveryAllowed(*core.User, *core.Title) bool
	}); ok && !core.ExplicitSearch(ctx) {
		out := make([]core.Title, 0, len(titles))
		for _, title := range titles {
			if policy.DiscoveryAllowed(viewer, &title) {
				out = append(out, title)
			}
		}
		titles = out
	}

	ctx = core.WithCatalogViewer(ctx, viewer)
	if v.FilterAvailable == nil {
		return titles
	}
	filtered := v.FilterAvailable(core.WithCatalogViewer(ctx, viewer), titles)
	if len(filtered) == len(titles) || viewer == nil || v.Sessions == nil {
		return filtered
	}
	keep := map[core.RatingKey]bool{}
	for _, title := range filtered {
		keep[title.Key] = true
	}
	for _, session := range v.Sessions() {
		if session != nil && session.UserID == viewer.ID {
			keep[session.Key] = true
		}
	}
	out := make([]core.Title, 0, len(titles))
	for _, title := range titles {
		if keep[title.Key] {
			out = append(out, title)
		}
	}
	return out
}

// CollectionAllowed checks the library restriction for a collection.
func (v Visibility) CollectionAllowed(viewer *core.User, c *core.Collection) bool {
	if c.Type != core.TypeMovie && c.Type != core.TypeShow {
		return true
	}
	if v.SectionFor == nil {
		return true
	}
	id, ok := v.SectionFor(c.Type)
	return !ok || SectionAllowed(viewer, id)
}

// UpcomingItems retains the existing placeholder discovery rules for a page.
func (v Visibility) UpcomingItems(ctx context.Context, viewer *core.User, items []core.Title, now func() time.Time) []core.Title {
	ctx = core.WithCatalogViewer(ctx, viewer)
	nowVal := time.Now()
	if now != nil {
		nowVal = now()
	}
	allowed := make([]bool, len(items))
	future := make([]bool, len(items))
	var released []core.Title
	for i := range items {
		title := &items[i]
		if !v.Playable(ctx, viewer, title) || title.Released.IsZero() {
			continue
		}
		allowed[i] = true
		if title.IsFutureRelease(nowVal) {
			future[i] = true
		} else {
			released = append(released, *title)
		}
	}
	availSet := make(map[core.RatingKey]bool, len(released))
	if len(released) > 0 {
		for _, t := range v.Available(ctx, viewer, released) {
			availSet[t.Key] = true
		}
	}
	seen := make(map[core.RatingKey]bool, len(items))
	out := make([]core.Title, 0, len(items))
	for i, title := range items {
		if !allowed[i] || seen[title.Key] {
			continue
		}
		if !future[i] && !availSet[title.Key] {
			continue
		}
		seen[title.Key] = true
		out = append(out, title)
	}
	return out
}

// TitlesAllowed filters the title/section guard without discovery filtering.
func (v Visibility) TitlesAllowed(viewer *core.User, titles []core.Title) []core.Title {
	out := make([]core.Title, 0, len(titles))
	for i := range titles {
		if v.TitleAllowed(viewer, &titles[i]) {
			out = append(out, titles[i])
		}
	}
	return out
}

// MaySetViewerRating is the owner-console rule for changing a household rating cap.
// Delegated sessions and non-owners cannot write it.
func MaySetViewerRating(actor *core.Viewer, active *core.User) bool {
	return actor != nil && !actor.Delegated && active != nil && active.IsOwner
}

// ViewerRestricted chooses inherited visibility for limited or unknown policies.
func ViewerRestricted(policy AccessPolicy, u *core.User) bool {
	if u == nil {
		return true
	}
	if u.IsOwner {
		return false
	}
	if u.MaxContentRating != "" || len(u.AllowedSections) > 0 {
		return true
	}
	if policy == nil {
		return false
	}
	if p, ok := policy.(core.ArtworkAccessPolicy); ok {
		return p.Restricted(u)
	}
	return true
}

// HomeRevision stamps a viewer's cached Home. Viewer changes the Home must
// show on the very next request: the latest watch-state write (Continue
// Watching, watched badges, ratings), playlists, watchlist, favorites and
// "not interested" exclusions. Policy is the shared viewer-policy epoch:
// it also moves on catalogue and guide changes, so only restricted viewers
// treat it as a hard input.
type HomeRevision struct {
	Viewer string
	Policy int64
}

// ApplyMetadataDisplay applies a cached Plex metadata overlay's display
// fields to title.
func ApplyMetadataDisplay(title *core.Title, overlay MetadataOverlay) {
	plexmeta.ApplyDisplay(title, overlay)
}

// SaferMetadataRating is the stricter of a catalogue and overlay rating.
func SaferMetadataRating(catalogue, overlay string) string {
	return plexmeta.SaferRating(catalogue, overlay)
}

// MetadataOverlayRatingAllowed reports whether a cached metadata overlay's
// rating stays within viewer's content-rating cap when combined with the
// catalogue rating.
func MetadataOverlayRatingAllowed(viewer *core.User, catalogue, overlay string) bool {
	if viewer == nil || viewer.MaxContentRating == "" || overlay == "" {
		return true
	}
	return !contentrating.Exceeds(SaferMetadataRating(catalogue, overlay), viewer.MaxContentRating)
}

// DownloadsAllowed is shared capability admission; handlers still enforce it.
func DownloadsAllowed(viewer *core.User, ready, allowFriends bool, policy AccessPolicy) bool {
	if viewer == nil || !ready {
		return false
	}
	if viewer.IsOwner {
		return true
	}
	if p, ok := policy.(core.DownloadAccessPolicy); ok {
		return p.AllowDownloads(viewer)
	}
	return allowFriends
}

// PlexWireID is the positive int32-range id Plex clients already parse.
// Offer rows use it directly and are not stream bindings.
func PlexWireID(key core.RatingKey, slot string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strconv.FormatInt(int64(key), 10) + "/" + slot))
	return int64(h.Sum64()%2_000_000_000) + 1
}
