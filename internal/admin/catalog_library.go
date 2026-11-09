package admin

import (
	"context"
	"net/http"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
)

// CatalogLibraryControls exposes per-title browsability explanations and
// owner library overrides to the console inspector.
type CatalogLibraryControls interface {
	TitleLibraryStatus(ctx context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error)
	IncludeTitle(ctx context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error)
	RemoveInclude(ctx context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error)
}

// CatalogUnfilteredSearch searches non-adult TMDB and local titles without
// hiding items excluded by catalogue rules or release gates.
type CatalogUnfilteredSearch interface {
	SearchAllTitles(ctx context.Context, q string, t core.MediaType, limit int) ([]core.Title, error)
}

func (h *Handler) handleCatalogTitleLibrary(w http.ResponseWriter, r *http.Request) {
	ctrl, ok := h.deps.CatalogHelper.(CatalogLibraryControls)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Catalog library controls are unavailable")
		return
	}
	key, err := core.ParseRatingKey(r.PathValue("key"))
	if err != nil || (key.Kind() != core.KindMovie && key.Kind() != core.KindShow) {
		writeError(w, http.StatusNotFound, "not_found", "no such title")
		return
	}
	var status catalog.BrowsabilityStatus
	switch r.Method {
	case http.MethodGet:
		status, err = ctrl.TitleLibraryStatus(r.Context(), key)
	case http.MethodPut:
		status, err = ctrl.IncludeTitle(r.Context(), key)
	case http.MethodDelete:
		status, err = ctrl.RemoveInclude(r.Context(), key)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	if status.Reasons == nil {
		status.Reasons = []string{}
	}
	if status.Items == nil {
		status.Items = []catalog.BrowsabilityReason{}
	}
	writeJSON(w, http.StatusOK, status)
}
