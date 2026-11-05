package plex

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestSectionShelfCutRetainsFilteredMixedOrder(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	pr := &Request{User: &ownerUser, ClientID: "paging"}
	mixed := core.Hub{ID: "mixed", Items: []core.Title{fightClub, breakingBad, alien}}
	section, ok := core.SectionView(mixed, core.TypeMovie)
	if !ok {
		t.Fatal("missing movie view")
	}
	builds := 0
	build := func() []core.Title { builds++; return section.Items }
	hc := hubCtx{section: 1}
	first := f.srv.shelfItems(context.Background(), pr, section, hc, build)
	// Simulate new digital evidence changing exclusions and row assignment.
	section.Items = []core.Title{alien}
	second := f.srv.shelfItems(context.Background(), pr, section, hc, build)
	if builds != 1 || len(first) != 2 || len(second) != 2 || first[0].Key != second[0].Key || first[1].Key != second[1].Key {
		t.Fatalf("shelf shifted: builds=%d first=%v second=%v", builds, first, second)
	}
	hc.section = 2
	other := f.srv.shelfItems(context.Background(), pr, section, hc, build)
	if len(other) != 1 || builds != 2 {
		t.Fatal("section cuts shared a list")
	}
}

func TestBrowseScopeOmitsPagingAndCredentials(t *testing.T) {
	pr := &Request{ClientID: "client-one"}
	first := httptest.NewRequest("GET", "/library/sections/1/all?sort=titleSort&X-Plex-Container-Start=0&X-Plex-Container-Size=50&X-Plex-Token=secret", nil)
	next := httptest.NewRequest("GET", "/library/sections/1/all?sort=titleSort&X-Plex-Container-Start=50&X-Plex-Container-Size=100", nil)
	a, b := browseScope(first, pr), browseScope(next, pr)
	if a != b || strings.Contains(a, "secret") {
		t.Fatalf("unstable/credential-bearing scope: %q %q", a, b)
	}
	next.URL.RawQuery = "sort=addedAt"
	if a == browseScope(next, pr) {
		t.Fatal("sorts shared a sequence")
	}
}
