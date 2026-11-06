package onair

import (
	"context"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestLocalReelImportPreservesProvenance(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	opts := Options{DB: db, Dir: t.TempDir()}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	reel, err := s.SaveReel(ctx, Reel{Name: "Commercials", Order: "sequence"})
	if err != nil {
		t.Fatal(err)
	}
	metadata := ClipUploadMetadata{Source: "https://archive.org/details/example", Attribution: "Internet Archive; public domain"}
	clip, err := s.UploadClip(ctx, reel.ID, "Commercial", strings.NewReader("local media fixture"), metadata)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// A restart and a failed conversion must retain the local import's credit.
	if err := s.convertClip(ctx, reel.ID, clip); err == nil {
		t.Fatal("expected unavailable conversion")
	}
	reel, err = s.Reel(ctx, reel.ID)
	if err != nil || len(reel.Clips) != 1 {
		t.Fatalf("reel after restart: %+v, %v", reel, err)
	}
	got := reel.Clips[0]
	if got.Source != metadata.Source || got.Attribution != metadata.Attribution || got.Status != "error" {
		t.Fatalf("provenance lost during conversion lifecycle: %+v", got)
	}
}
