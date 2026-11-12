package raster

import (
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"testing"
)

func TestSequenceStaticPlateReuse(t *testing.T) {
	d := scene.Document{V: 1, Moment: "standby", Duration: 16, Layers: []scene.Layer{{ID: "plate", Type: "rect", W: 1920, H: 1080, Fill: "#123456"}}}
	s := NewSequence(d, Bindings{}, 128, 72)
	first := s.Frame(0)
	for i := 1; i < 480; i++ {
		if s.Frame(float64(i)/30) != first {
			t.Fatal("unchanged frame allocated")
		}
	}
	if s.RasterCalls != 1 {
		t.Fatalf("raster calls=%d, want 1", s.RasterCalls)
	}
}
