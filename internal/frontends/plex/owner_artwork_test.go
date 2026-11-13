package plex

import (
	"github.com/braedonsaunders/ether/internal/frontend"
	"testing"
)

type versionedOwnerArtwork struct {
	frontend.Artwork
	version string
}

func (a *versionedOwnerArtwork) OwnerArtworkVersion(*frontend.Title, string) string { return a.version }

func TestOwnerArtworkVersionsMetadataURLs(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	artwork := &versionedOwnerArtwork{version: "first"}
	f.srv.mutateHost(func(h *frontend.Host) { h.Artwork = artwork })
	title := fightClub
	firstThumb := f.srv.thumbURL(&title)
	firstArt := f.srv.artURL(&title)
	artwork.version = "replacement"
	if firstThumb == f.srv.thumbURL(&title) || firstArt == f.srv.artURL(&title) {
		t.Fatal("replacement did not change image URLs")
	}
	title.PosterPath = ""
	title.StillPath = ""
	title.BackdropPath = ""
	if f.srv.thumbURL(&title) == "" || f.srv.artURL(&title) == "" {
		t.Fatal("owner artwork missing when upstream paths are empty")
	}
}
