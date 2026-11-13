package onair

import (
	"bytes"
	"github.com/braedonsaunders/ether/internal/onair/families"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"testing"
)

// A ready master can replace the startup plate only at a segment boundary.
func TestMomentPlateSplicesAtSegmentBoundary(t *testing.T) {
	t.Run("warm grunge title visible from frame zero", func(t *testing.T) {
		doc, err := families.Get("grunge", "tonight")
		if err != nil {
			t.Fatal(err)
		}
		_, overlay, ok := scene.SplitSlotLayers(doc)
		if !ok {
			t.Fatal("title must split")
		}
		base := bytes.Repeat([]byte{32}, 320*180*3/2)
		makeStream := func() *MomentStream {
			return &MomentStream{mode: "split-cached", width: 320, height: 180, fpsNum: 30, fpsDen: 1, loop: true, baseReader: &plateReader{pixels: base, remaining: -1}, overlayDoc: &overlay, overlayBnd: raster.Bindings{Bindings: families.Defaults("grunge")}}
		}
		empty := makeStream()
		defer empty.Close()
		before := make([]byte, len(base))
		if _, err = empty.ReadFrame(before); err != nil {
			t.Fatal(err)
		}
		warm := makeStream()
		defer warm.Close()
		warm.StartCompleteTitle()
		first := make([]byte, len(base))
		if _, err = warm.ReadFrame(first); err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(first, base) || bytes.Equal(first, before) {
			t.Fatal("warm first frame has no complete title foreground")
		}
		settled := makeStream()
		defer settled.Close()
		settled.tick = int64(doc.Poster * 30)
		expected := make([]byte, len(base))
		if _, err = settled.ReadFrame(expected); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, expected) {
			t.Fatal("first frame differs from authored complete title")
		}
	})

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
