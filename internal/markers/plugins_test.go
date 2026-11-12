package markers

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPluginMarkersStoredAndOrderedBySource(t *testing.T) {
	ctx := context.Background()
	order := []string{"plugin:test/markers", "chapter", "theintrodb"}
	svc := New(Options{DB: store.OpenTest(t), Config: func() Lookup { return Lookup{SourceOrder: order} }})
	key := core.MovieKey(5)
	d := 100 * time.Second
	if err := svc.PutPlugin(ctx, key, d, "plugin:test/markers", []Marker{{Type: "intro", StartMS: 2000, EndMS: 8000}, {Type: "preview", StartMS: 90000, EndMS: 95000}}); err != nil {
		t.Fatal(err)
	}
	chapters := []Marker{{Type: "intro", StartMS: 1000, EndMS: 5000, Source: "chapter"}}
	got := svc.mergeSources(ctx, key, d, chapters, nil)
	if len(got) != 2 || got[0].Source != "plugin:test/markers" || got[1].Type != "preview" {
		t.Fatalf("unexpected markers: %+v", got)
	}
	order = []string{"chapter", "plugin:test/markers"}
	got = svc.mergeSources(ctx, key, d, chapters, nil)
	if len(got) != 2 || got[0].Source != "chapter" {
		t.Fatalf("order not respected: %+v", got)
	}
	if err := svc.DeletePlugin(ctx, "plugin:test/markers"); err != nil {
		t.Fatal(err)
	}
	if got = svc.mergeSources(ctx, key, d, chapters, nil); len(got) != 1 {
		t.Fatalf("disabled source still present: %+v", got)
	}
}

func TestSkipDBMergedOnlyWhenEnabled(t *testing.T) {
	ctx := context.Background()
	enabled := true
	svc := New(Options{DB: store.OpenTest(t), Config: func() Lookup { return Lookup{SkipDB: enabled} }})
	key := core.MovieKey(7)
	d := 100 * time.Second
	if err := svc.PutPlugin(ctx, key, d, SkipDBSource, []Marker{{Type: "intro", StartMS: 2000, EndMS: 8000}}); err != nil {
		t.Fatal(err)
	}
	chapters := []Marker{{Type: "credits", StartMS: 90000, EndMS: 98000, Source: "chapter"}}
	got := svc.mergeSources(ctx, key, d, chapters, nil)
	if len(got) != 2 {
		t.Fatalf("skipdb not merged with empty source order: %+v", got)
	}
	types := map[string]bool{}
	for _, m := range got {
		types[m.Type] = true
	}
	if !types["intro"] || !types["credits"] {
		t.Fatalf("both sources should be present: %+v", got)
	}
	enabled = false
	if got = svc.mergeSources(ctx, key, d, chapters, nil); len(got) != 1 || got[0].Type != "credits" {
		t.Fatalf("disabled skipdb still merged: %+v", got)
	}
}
