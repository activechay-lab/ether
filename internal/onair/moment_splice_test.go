package onair

import "testing"

// A ready master can replace the startup plate only at a segment boundary.
func TestMomentPlateSplicesAtSegmentBoundary(t *testing.T) {
	plate := &plateReader{pixels: []byte{30}, remaining: -1}
	master := &plateReader{pixels: []byte{200}, remaining: -1}
	ready := false
	m := &MomentStream{mode: "static-plate", fpsNum: 30, fpsDen: 1, baseReader: plate}
	m.promote = func() FrameReader {
		if ready {
			return master
		}
		return nil
	}
	defer m.Close()
	for tick := 0; tick < 240; tick++ {
		if tick == 45 {
			ready = true
		}
		video := make([]byte, 1)
		if _, err := m.ReadFrame(video); err != nil {
			t.Fatal(err)
		}
		want := byte(30)
		if tick >= 120 {
			want = 200
		}
		if video[0] != want {
			t.Fatalf("tick %d: picture %d, want %d", tick, video[0], want)
		}
	}
	if m.Mode() != "cached" {
		t.Fatalf("mode %q", m.Mode())
	}
}
