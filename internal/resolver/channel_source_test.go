package resolver

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Broadcast selection must never promote a verified REMUX above a source
// that can actually feed the clock; measured delivery can admit it later.
func TestChannelSourceAdmission(t *testing.T) {
	svc := New(&fakeIndexer{}, fakeHealth{}, nil, testCfg(), Options{})
	title := matrixTitle()
	caps := webCaps()
	caps.BroadcastBitrateKbps = 6000
	caps.MaxBitrateKbps = 6000
	small := rel("small", "The.Signal.1999.1080p.WEB-DL.H.264-GRP", 3000)
	heavy := rel("remux", "The.Signal.1999.1080p.BluRay.REMUX.H.264-GRP", 39000)
	if !svc.ChannelSourceAllowed(small, caps, title) || svc.ChannelSourceAllowed(heavy, caps, title) {
		t.Fatal("unmeasured sources did not respect rendition budget")
	}
	sample := func(r core.Release, rate float64) {
		svc.delivery.samples = map[string]deliverySample{deliveryKey(r): {rate: rate, at: svc.now(), count: 3}}
	}
	sample(heavy, 2_000_000)
	if svc.ChannelSourceAllowed(heavy, caps, title) {
		t.Fatal("REMUX slower than real time admitted")
	}
	sample(heavy, 10_000_000)
	if !svc.ChannelSourceAllowed(heavy, caps, title) {
		t.Fatal("measured headroom did not admit fallback")
	}
	sample(small, 100_000)
	if svc.ChannelSourceAllowed(small, caps, title) {
		t.Fatal("known slow small source admitted")
	}
	svc.delivery.samples[deliveryKey(small)] = deliverySample{rate: 100_000, at: svc.now().Add(-3 * time.Minute), count: 3}
	if !svc.ChannelSourceAllowed(small, caps, title) {
		t.Fatal("stale delivery poisoned selection")
	}
}
