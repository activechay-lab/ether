package core

import "testing"

func TestLogicalPreviewIdentityStillIncludesItem(t *testing.T) {
	a := &PlaySession{Key: 1, Resolution: &Resolution{Release: Release{ID: "shared-pack"}}}
	b := &PlaySession{Key: 2, Resolution: a.Resolution}
	if TrickplayCacheKey(a) == TrickplayCacheKey(b) {
		t.Fatal("logical descriptors for different items share previews")
	}
	same := &PlaySession{Key: 1, Resolution: &Resolution{Release: Release{ID: "shared-pack"}}}
	if TrickplayCacheKey(a) != TrickplayCacheKey(same) {
		t.Fatal("identity is unstable")
	}
}
