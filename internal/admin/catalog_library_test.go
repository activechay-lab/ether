package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type libraryHelper struct {
	fakeHelper
	included  map[core.RatingKey]bool
	gated     map[core.RatingKey]bool
	searchAll int
}

func (l *libraryHelper) TitleLibraryStatus(_ context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error) {
	if l.gated[key] {
		return catalog.BrowsabilityStatus{
			Browsable:    false,
			CanInclude:   false,
			ReleaseGated: true,
			Reasons:      []string{"Waiting for a playable release (new movie)"},
			Items:        []catalog.BrowsabilityReason{{Code: "release_gate_movie", Message: "Waiting for a playable release (new movie)"}},
		}, nil
	}
	if l.included[key] {
		return catalog.BrowsabilityStatus{
			Browsable:     true,
			Included:      true,
			InLibraryTier: true,
		}, nil
	}
	return catalog.BrowsabilityStatus{
		Browsable:  false,
		CanInclude: true,
		Reasons:    []string{"Below your popularity floor (12 votes; floor 50)"},
		Items: []catalog.BrowsabilityReason{{
			Code:     "popularity_floor",
			Message:  "Below your popularity floor (12 votes; floor 50)",
			Votes:    12,
			MinVotes: 50,
		}},
	}, nil
}

func (l *libraryHelper) IncludeTitle(ctx context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error) {
	if l.gated[key] {
		return catalog.BrowsabilityStatus{}, &catalog.IncludeRejectedError{
			Message: "This title is waiting for a playable release and cannot be added early",
		}
	}
	l.included[key] = true
	return l.TitleLibraryStatus(ctx, key)
}

func (l *libraryHelper) RemoveInclude(ctx context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error) {
	delete(l.included, key)
	return l.TitleLibraryStatus(ctx, key)
}

func (l *libraryHelper) SearchAllTitles(_ context.Context, _ string, _ core.MediaType, _ int) ([]core.Title, error) {
	l.searchAll++
	return []core.Title{{Key: core.MovieKey(77), Type: core.TypeMovie, Title: "Hidden French Film"}}, nil
}

func TestCatalogTitleLibraryAndUnfilteredSearch(t *testing.T) {
	store := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = true })
	key := core.MovieKey(77)
	gatedKey := core.MovieKey(88)
	helper := &libraryHelper{
		included: map[core.RatingKey]bool{},
		gated:    map[core.RatingKey]bool{gatedKey: true},
	}
	h := admin.NewHandler(admin.Deps{
		Config:        store,
		IsOwnerToken:  storedOwner(store),
		CatalogHelper: helper,
	})

	path := "/ether/api/catalog/title/" + key.String() + "/library"
	if rec := serve(h, req(http.MethodGet, path, "")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", rec.Code)
	}

	// GET explains why the title is not browsable.
	rec := serve(h, authed(req(http.MethodGet, path, "")))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", rec.Code, rec.Body)
	}
	var status catalog.BrowsabilityStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Browsable || !status.CanInclude || len(status.Reasons) != 1 || status.Reasons[0] != "Below your popularity floor (12 votes; floor 50)" {
		t.Fatalf("unexpected GET status: %+v", status)
	}

	// PUT promotes into the library.
	rec = serve(h, authed(req(http.MethodPut, path, "{}")))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d: %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Browsable || !status.Included || status.CanInclude {
		t.Fatalf("unexpected PUT status: %+v", status)
	}

	// DELETE removes the include.
	rec = serve(h, authed(req(http.MethodDelete, path, "")))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d: %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Browsable || status.Included || !status.CanInclude {
		t.Fatalf("unexpected DELETE status: %+v", status)
	}

	// Release-gated movie returns 409 Conflict on PUT.
	gatedPath := "/ether/api/catalog/title/" + gatedKey.String() + "/library"
	rec = serve(h, authed(req(http.MethodPut, gatedPath, "{}")))
	if rec.Code != http.StatusConflict {
		t.Fatalf("gated PUT status = %d, want 409: %s", rec.Code, rec.Body)
	}

	// Episode keys are rejected with 404.
	epPath := "/ether/api/catalog/title/" + core.EpisodeKey(1, 1, 1).String() + "/library"
	if rec := serve(h, authed(req(http.MethodGet, epPath, ""))); rec.Code != http.StatusNotFound {
		t.Fatalf("episode GET status = %d, want 404", rec.Code)
	}

	// Unfiltered TMDB lookup (?all=1) reaches SearchAllTitles.
	rec = serve(h, authed(req(http.MethodGet, "/ether/api/catalog/search?q=French&all=1", "")))
	if rec.Code != http.StatusOK || helper.searchAll != 1 {
		t.Fatalf("search?all=1 code=%d calls=%d: %s", rec.Code, helper.searchAll, rec.Body)
	}
}
