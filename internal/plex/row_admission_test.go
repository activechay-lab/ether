package plex

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Row cards, detail and artwork must agree on release admission, including a
// digital release preceding a future theatrical date. Counts follow selection.
func TestRowDetailAdmissionInvariant(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	var titles []core.Title
	for i, state := range []string{"released", "future", "digital", "adult"} {
		title := fightClub
		title.Key = core.MovieKey(int64(1662858 + i))
		title.Title = state
		title.Released = fixedNow.Add(24 * time.Hour)
		if state == "released" || state == "adult" {
			title.Released = fixedNow.Add(-24 * time.Hour)
		}
		if state == "digital" {
			title.HomeReleaseChecked = true
			title.HomeReleased = fixedNow.Add(-24 * time.Hour)
		}
		title.Adult = state == "adult"
		f.catalog.titles[title.Key] = title
		titles = append(titles, title)
	}
	pr := &Request{User: &core.User{ID: 1, IsOwner: true}}
	row := f.srv.renderHub(context.Background(), pr, hubSpec{Hub: core.Hub{ID: "collection.netflix", Items: titles}}, hubCtx{})
	if len(row.Metadata) != 2 {
		t.Fatalf("row has %d cards, want released and digital", len(row.Metadata))
	}
	for _, title := range titles {
		expected := http.StatusNotFound
		if title.Title == "released" || title.Title == "digital" {
			expected = http.StatusOK
		}
		for _, suffix := range []string{"", "/thumb"} {
			result := f.get(t, "/library/metadata/"+title.Key.String()+suffix)
			if result.Code != expected {
				t.Errorf("%s%s: %d want %d", title.Title, suffix, result.Code, expected)
			}
		}
	}
}
