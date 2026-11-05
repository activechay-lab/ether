package core

import "testing"

func TestResolutionTierCroppedAndUnknown(t *testing.T) {
	for _, v := range []struct {
		w, h, tier int
		label      string
	}{{3836, 1600, 2160, "4K"}, {1920, 800, 1080, "1080p"}, {1280, 544, 720, "720p"}, {0, 0, 0, "SD"}, {720, 480, 480, "480p"}} {
		if got := ResolutionTier(v.w, v.h); got != v.tier {
			t.Errorf("%dx%d: %d", v.w, v.h, got)
		}
		if got := ResolutionLabel(v.w, v.h); got != v.label {
			t.Errorf("label %dx%d: %s", v.w, v.h, got)
		}
	}
}
