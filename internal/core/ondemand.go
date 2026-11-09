package core

import (
	"context"
	"sync"
)

// On-demand stream links (Release.StreamOnDemand) make their source fetch
// the release when opened: a Stremio add-on that resolves a torrent on its
// debrid account, or queues and imports an NZB, on every request. Ether
// opens one such link at a time and only for work a viewer or the owner is
// waiting on, so a Play costs the source one release while it plays.

// OpensOnDemand reports whether reading r opens an on-demand stream link.
func (r Release) OpensOnDemand() bool {
	return r.StreamOnDemand && r.StreamURL != "" && r.Protocol == ProtocolHTTP
}

type noOnDemandKey struct{}

// WithoutOnDemandStreams marks ctx as work that must not open on-demand
// stream links although it is not speculative, such as a client's
// file-availability check on a detail page.
func WithoutOnDemandStreams(ctx context.Context) context.Context {
	return context.WithValue(ctx, noOnDemandKey{}, true)
}

// OnDemandStreamsAllowed reports whether ctx may open an on-demand stream
// link: Play, failover and the owner's own checks may. Speculative indexer
// work (warm-ups, prefetch, detail discovery), background checks and work
// marked WithoutOnDemandStreams may not; they answer ErrForegroundOnly.
func OnDemandStreamsAllowed(ctx context.Context) bool {
	if v, _ := ctx.Value(noOnDemandKey{}).(bool); v {
		return false
	}
	return !SpeculativeIndexerWork(ctx) && !IsBackgroundCheck(ctx)
}

// onDemandWarm is the one add-on stream a warm-up with strong play intent
// may open (WithOnDemandWarm). It is granted at most once per warm-up.
type onDemandWarm struct {
	mu      sync.Mutex
	admit   func(Release) bool
	used    bool
	granted string
}

type onDemandWarmKey struct{}

// WithOnDemandWarm marks speculative work that follows a strong play intent
// (an Up Next or next-episode warm-up, a detail page held by a device with
// known caps). Such work may open exactly one on-demand stream link: the
// top-ranked candidate, only when its add-on reports the release cached on
// a debrid service (no import or download is queued), and only when admit
// (the caller's per-viewer and per-add-on budgets; nil admits) agrees.
// Nothing else changes: a failed or refused add-on stream is never followed
// by a second one in the background.
func WithOnDemandWarm(ctx context.Context, admit func(Release) bool) context.Context {
	return context.WithValue(ctx, onDemandWarmKey{}, &onDemandWarm{admit: admit})
}

// OnDemandCached reports whether an add-on marks r cached on a debrid
// service and nothing about it is uncached: opening it queues no import or
// download. A Usenet-importing add-on never marks its streams that way, so
// such streams are never warmed or hedged.
func OnDemandCached(r Release) bool {
	return r.OpensOnDemand() && len(r.DebridCached) > 0 && len(r.DebridUncached) == 0
}

// OnDemandAllowedAt reports whether ctx may open r, an on-demand stream link
// ranked at position rank (0 is the top candidate) of a Resolve. Work
// OnDemandStreamsAllowed admits may; a WithOnDemandWarm warm-up may open the
// one top-ranked, cached candidate its budgets admit; nothing else may.
func OnDemandAllowedAt(ctx context.Context, r Release, rank int) bool {
	if OnDemandStreamsAllowed(ctx) {
		return true
	}
	if v, _ := ctx.Value(noOnDemandKey{}).(bool); v || IsBackgroundCheck(ctx) {
		return false
	}
	w, _ := ctx.Value(onDemandWarmKey{}).(*onDemandWarm)
	if w == nil || rank != 0 || r.ID == "" || !OnDemandCached(r) {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.used {
		return w.granted == r.ID
	}
	if w.admit != nil && !w.admit(r) {
		w.used = true // refused once: never a later or lower candidate
		return false
	}
	w.used, w.granted = true, r.ID
	return true
}

// OnDemandWarmGranted reports whether ctx's warm-up was granted r's link by
// OnDemandAllowedAt; the source opening the link checks it.
func OnDemandWarmGranted(ctx context.Context, r Release) bool {
	if v, _ := ctx.Value(noOnDemandKey{}).(bool); v || IsBackgroundCheck(ctx) {
		return false
	}
	w, _ := ctx.Value(onDemandWarmKey{}).(*onDemandWarm)
	if w == nil || r.ID == "" {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.used && w.granted == r.ID
}
