package core

import (
	"context"
	"testing"
)

// A warm-up with play intent opens at most one add-on stream: the top-ranked
// one, only when cached, and never another after a refusal or a failure.
func TestOnDemandWarmOpensOnlyTheTopCachedStream(t *testing.T) {
	spec, _ := WithSpeculative(context.Background())
	cached := Release{ID: "a", Protocol: ProtocolHTTP, StreamURL: "https://addon/a", StreamOnDemand: true, DebridCached: []string{"torbox"}}
	second := cached
	second.ID = "b"
	uncached := cached
	uncached.DebridCached, uncached.DebridUncached = nil, []string{"torbox"}
	if OnDemandAllowedAt(spec, cached, 0) {
		t.Fatal("speculative work without play intent opened an add-on stream")
	}
	ctx := WithOnDemandWarm(spec, nil)
	if OnDemandAllowedAt(ctx, uncached, 0) || OnDemandAllowedAt(ctx, cached, 1) {
		t.Fatal("warmed an uncached or lower-ranked stream")
	}
	if !OnDemandAllowedAt(ctx, cached, 0) || !OnDemandWarmGranted(ctx, cached) {
		t.Fatal("top cached stream not granted")
	}
	if OnDemandAllowedAt(ctx, second, 0) || OnDemandWarmGranted(ctx, second) {
		t.Fatal("a second add-on stream was granted")
	}
	refused := WithOnDemandWarm(spec, func(Release) bool { return false })
	if OnDemandAllowedAt(refused, cached, 0) || OnDemandAllowedAt(WithOnDemandWarm(spec, nil), uncached, 0) {
		t.Fatal("budget refusal ignored")
	}
}
