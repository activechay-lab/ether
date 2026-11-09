package experience

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestVersionOfferPublicationIsNotModification(t *testing.T) {
	published := time.Date(2024, 4, 5, 6, 7, 8, 9, time.UTC)
	updated := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	released := time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC)
	title := &core.Title{UpdatedAt: updated, Released: released}
	variant := core.ReleaseVariant{ID: "candidate", Label: "1080p", Info: core.MediaInfo{Estimated: true, Size: 99}, Release: core.Release{ID: "raw-release", PubDate: published}}
	got := VersionOfferFrom(title, variant)
	if got.ID != "candidate" || got.Name != "1080p" || !got.Media.Estimated || got.Media.Size != 99 {
		t.Fatalf("candidate facts changed: %+v", got)
	}
	if got.Publication == nil || !got.Publication.Equal(published) || !got.AddedAt.Equal(released) {
		t.Fatalf("publication lost: %+v", got)
	}
	variant.Release.PubDate = published.Add(time.Hour)
	if !got.Publication.Equal(published) {
		t.Fatal("publication aliased the release")
	}
	variant.Release.PubDate = time.Time{}
	got = VersionOfferFrom(title, variant)
	if got.Publication != nil || !got.AddedAt.Equal(released) {
		t.Fatalf("catalogue date became publication: %+v", got)
	}
	title.UpdatedAt = time.Time{}
	got = VersionOfferFrom(title, variant)
	if got.Publication != nil || !got.AddedAt.Equal(released) {
		t.Fatalf("release date fallback: %+v", got)
	}
	variant.Release.PubDate = published
	aired := time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC)
	show := &core.Title{Type: core.TypeShow, Released: released, AddedAt: aired, UpdatedAt: updated}
	showOffer := VersionOfferFrom(show, variant)
	if showOffer.Publication == nil || !showOffer.Publication.Equal(published) || !showOffer.AddedAt.Equal(aired) {
		t.Fatalf("show added date: %+v", showOffer)
	}
	variant.Release.PubDate = time.Time{}
	title.Released = time.Time{}
	if !VersionOfferFrom(title, variant).AddedAt.IsZero() || !VersionOfferFrom(nil, variant).AddedAt.IsZero() {
		t.Fatal("invented date")
	}
	variant.Release.PubDate = published
	if !VersionOfferFrom(nil, variant).AddedAt.IsZero() {
		t.Fatal("nil title dropped publication")
	}
}

func TestVersionOfferKeepsRawReleaseIdentityInternal(t *testing.T) {
	raw := "indexer/guid?token=secret"
	variant := core.ReleaseVariant{ID: "picker-id", Label: "label", Release: core.Release{ID: raw, Name: "/private/folder/movie.mkv?token=secret"}}
	got := VersionOfferFrom(nil, variant)
	if got.ID != "picker-id" || got.ID == raw || got.Name == variant.Release.Name {
		t.Fatalf("identity crossed: %+v", got)
	}
}
