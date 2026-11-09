package admin

import (
	"net/http"

	"github.com/braedonsaunders/ether/internal/collections"
)

func (h *Handler) handleCollectionTemplateBundles(w http.ResponseWriter, r *http.Request) {
	bundles := collections.TemplateBundles()
	if bundles == nil {
		bundles = []collections.TemplateBundle{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bundles": bundles})
}

func (h *Handler) handlePreviewTemplateBundle(w http.ResponseWriter, r *http.Request) {
	if h.deps.Collections == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "collections studio is not configured")
		return
	}
	existing, err := h.deps.Collections.ListSpecs(r.Context())
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	templates := h.deps.Collections.Templates()
	if len(templates) == 0 {
		templates = collections.Templates()
	}
	preview, err := collections.PreviewTemplateBundle(templates, existing, r.PathValue("id"))
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *Handler) handleApplyTemplateBundle(w http.ResponseWriter, r *http.Request) {
	if h.deps.Collections == nil {
		writeError(w, http.StatusNotImplemented, "not_configured", "collections studio is not configured")
		return
	}
	res, err := collections.ApplyTemplateBundle(r.Context(), h.deps.Collections, r.PathValue("id"))
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	if res.CreatedCount > 0 {
		h.invalidateOwnerRows()
	}
	views := make([]collectionView, 0, len(res.Created))
	for _, s := range res.Created {
		views = append(views, viewOf(s))
	}
	items := res.Items
	if items == nil {
		items = []collections.TemplateBundlePreviewItem{}
	}
	skipped := res.Skipped
	if skipped == nil {
		skipped = []collections.TemplateBundlePreviewItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bundleId":     res.BundleID,
		"bundle":       res.Bundle,
		"items":        items,
		"created":      views,
		"skipped":      skipped,
		"createdCount": res.CreatedCount,
		"skippedCount": res.SkippedCount,
	})
}
