package iso_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/iso"
	"github.com/braedonsaunders/ether/internal/iso/isotest"
)

func TestAnalyzeBlurayMainFeature(t *testing.T) {
	c1, c2, extra := bytes.Repeat([]byte{1}, 5000), bytes.Repeat([]byte{2}, 7000), bytes.Repeat([]byte{3}, 900)
	img := isotest.Build("MY_DISC", []isotest.File{
		{Path: "BDMV/PLAYLIST/00000.mpls", Data: isotest.MPLS([]isotest.PlayItem{{Clip: "00001", In: 0, Out: 450000}, {Clip: "00002", In: 90000, Out: 270000}})},
		{Path: "BDMV/PLAYLIST/00001.mpls", Data: isotest.MPLS([]isotest.PlayItem{{Clip: "00003", In: 0, Out: 45000}})},
		{Path: "BDMV/STREAM/00001.m2ts", Data: c1},
		{Path: "BDMV/STREAM/00002.m2ts", Data: c2},
		{Path: "BDMV/STREAM/00003.m2ts", Data: extra},
	})
	a, err := iso.Analyze(context.Background(), bytes.NewReader(img), int64(len(img)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Label != "MY_DISC" {
		t.Fatalf("label %q", a.Label)
	}
	if len(a.MainFeature) != 2 || a.MainFeature[0].Base() != "00001.M2TS" && a.MainFeature[0].Base() != "00001.m2ts" {
		t.Fatalf("main feature %+v", a.MainFeature)
	}
	if a.MainFeature[1].InTicks != 90000 || a.MainFeature[1].DurationTicks != 180000 || a.DurationTicks != 630000 {
		t.Fatalf("timing %+v total %d", a.MainFeature[1], a.DurationTicks)
	}
	// Extents point at the clip bytes inside the image.
	for i, want := range [][]byte{c1, c2} {
		var got []byte
		for _, x := range a.MainFeature[i].Extents {
			got = append(got, img[x.Offset:x.Offset+x.Length]...)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("clip %d bytes differ", i)
		}
	}
}

func TestAnalyzePlainDisc(t *testing.T) {
	movie := bytes.Repeat([]byte{9}, 30000)
	img := isotest.Build("PLAIN", []isotest.File{{Path: "Movie.mkv", Data: movie}, {Path: "readme.txt", Data: []byte("hi")}})
	a, err := iso.Analyze(context.Background(), bytes.NewReader(img), int64(len(img)), nil)
	if err != nil || len(a.Files) != 2 || a.MainFeature != nil {
		t.Fatalf("analysis %+v err %v", a, err)
	}
	if _, err := iso.Analyze(context.Background(), bytes.NewReader(make([]byte, 100000)), 100000, nil); err == nil {
		t.Fatal("expected error for a non-ISO")
	}
}
