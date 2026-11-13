package segcache

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestLocalProvenanceMergeAndRestart(t *testing.T) {
	dir := t.TempDir()
	c := openT(t, dir, 1024)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	mustPut(t, c, "r", 0, []byte("x"))
	c.RecordProvenance(core.CacheProvenance{ReleaseID: "r", File: "Film.mkv", Size: 100, Source: "Release.Name", Title: "Film (2024)", Cause: core.CacheCausePlaying, Play: true})
	now = now.Add(time.Hour)
	c.RecordProvenance(core.CacheProvenance{ReleaseID: "r", Cause: core.CacheCausePaused})
	now = now.Add(time.Hour)
	c.RecordProvenance(core.CacheProvenance{ReleaseID: "r", Cause: core.CacheCausePlaying, Play: true})

	r := c.Releases()[0]
	if r.file != "Film.mkv" || r.source != "Release.Name" || r.Size != 100 || r.Plays != 2 {
		t.Fatalf("metadata = %+v", r)
	}
	if len(r.causes) != 2 || r.causes[0].Code != core.CacheCausePlaying || !r.causes[0].FirstSeen.Equal(time.Unix(1000, 0)) || r.causes[1].Code != core.CacheCausePaused {
		t.Fatalf("causes = %+v", r.causes)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	again := openT(t, dir, 1024)
	again.indexDisk(context.Background())
	got := again.Releases()[0]
	if got.file != "Film.mkv" || got.source != "Release.Name" || got.Size != 100 || got.Plays != 2 || len(got.causes) != 2 || !got.causes[0].FirstSeen.Equal(time.Unix(1000, 0)) {
		t.Fatalf("restart lost provenance: %+v", got)
	}
}

func TestSharedProvenanceMergesAcrossNodes(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := t.TempDir()
	a, err := OpenShared(dir, 1024, db, "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenShared(dir, 1024, db, "b", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = a.Put("film", 0, []byte("bytes")); err != nil {
		t.Fatal(err)
	}
	if got := b.Releases()[0]; len(got.causes) != 0 || got.file != "" {
		t.Fatalf("legacy row should be unrecorded: %+v", got)
	}
	a.RecordProvenance(core.CacheProvenance{ReleaseID: "film", File: "Film.mkv", Size: 100, Source: "Release.Name", Cause: core.CacheCausePlaying, Play: true})
	b.RecordProvenance(core.CacheProvenance{ReleaseID: "film", Cause: core.CacheCausePaused})
	got := b.Releases()[0]
	if got.file != "Film.mkv" || got.source != "Release.Name" || got.Plays != 1 || len(got.causes) != 2 {
		t.Fatalf("shared provenance = %+v", got)
	}
	if got.causes[0].Code != core.CacheCausePlaying || got.causes[1].Code != core.CacheCausePaused {
		t.Fatalf("shared causes = %+v", got.causes)
	}
}
