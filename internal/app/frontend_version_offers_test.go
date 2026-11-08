package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestFrontendVersionKeepsReleaseIdentityOffTheWire(t *testing.T) {
	published := time.Date(2024, 4, 5, 0, 0, 0, 0, time.UTC)
	title := &core.Title{UpdatedAt: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC), Released: time.Unix(30, 0).UTC()}
	variants := []core.ReleaseVariant{
		{ID: "raw-release-id", Label: "1080p", Info: core.MediaInfo{Estimated: true, Size: 40, Container: "mkv"}, Release: core.Release{ID: "raw-release-id", Name: "/private/folder/movie.mkv?token=secret", PubDate: published}},
		{ID: "display-only", Label: "Auto", Release: core.Release{Name: "secret-name"}},
	}
	for _, variant := range variants {
		got := frontendVersion(title, variant)
		if got.ID != variant.ID || got.Name != variant.Label || !reflect.DeepEqual(got.Media, variant.Info) || !got.AddedAt.Equal(title.DateAdded(time.Now())) {
			t.Fatalf("version = %+v", got)
		}
		if got.FileName != "" || got.PartCount != nil || got.ETag != "" || got.DateModified != nil {
			t.Fatalf("frontend id semantics changed: %+v", got)
		}
		if variant.Release.PubDate.IsZero() {
			if got.PublishedAt != nil {
				t.Fatalf("unpublished version gained a date: %+v", got)
			}
			continue
		}
		if got.PublishedAt == nil || !got.PublishedAt.Equal(published) {
			t.Fatalf("published = %v", got.PublishedAt)
		}
	}
	variant := variants[0]
	got := frontendVersion(title, variant)
	variant.Release.PubDate = published.Add(time.Hour)
	if got.PublishedAt == nil || !got.PublishedAt.Equal(published) {
		t.Fatal("published time aliased the release")
	}
}
