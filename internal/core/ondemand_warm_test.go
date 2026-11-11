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

// A client's file-availability check may open an on-demand add-on stream only
// when the owner enabled it (WithOnDemandStreamsForAvailability). The mark
// admits that one check and nothing else: speculative work, background checks
// and work marked WithoutOnDemandStreams keep refusing, and a denied check
// stays denied even when both marks are set.
func TestAvailabilityCheckOpensOnDemandStreamsOnly(t *testing.T) {
	spec, _ := WithSpeculative(context.Background())
	warm := WithIndexerPurpose(spec, IndexerPurposeWarmup)
	rel := Release{ID: "a", Protocol: ProtocolHTTP, StreamURL: "https://addon/a", StreamOnDemand: true}
	if OnDemandStreamsAllowed(warm) {
		t.Fatal("a warm-up opened an on-demand stream without permission")
	}
	allowed := WithOnDemandStreamsForAvailability(warm)
	if !OnDemandStreamsAllowed(allowed) {
		t.Fatal("the permitted availability check could not open an on-demand stream")
	}
	if !OnDemandAllowedAt(allowed, rel, 0) {
		t.Fatal("the permitted availability check was refused the top on-demand stream")
	}
	if !OnDemandAllowedAt(allowed, rel, 3) {
		t.Fatal("the permitted availability check should admit any rank")
	}
	// Nothing else gets the mark: plain warm-ups, discovery and idle work.
	if OnDemandStreamsAllowed(warm) || OnDemandStreamsAllowed(spec) {
		t.Fatal("the mark leaked to other speculative work")
	}
	// An explicit deny still wins when both marks are present.
	if OnDemandStreamsAllowed(WithoutOnDemandStreams(WithOnDemandStreamsForAvailability(warm))) {
		t.Fatal("WithoutOnDemandStreams did not override the availability mark")
	}
}
