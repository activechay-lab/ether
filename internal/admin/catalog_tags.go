package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
)

type catalogTagger interface {
	TitleTags(context.Context, core.RatingKey) ([]string, error)
	SetTitleTags(context.Context, core.RatingKey, []string) error
}

func (h *Handler) handleCatalogTags(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.deps.CatalogHelper.(catalogTagger)
	if !ok {
		writeError(w, 503, "unavailable", "Tags are unavailable")
		return
	}
	key, err := core.ParseRatingKey(r.PathValue("key"))
	if err != nil {
		writeError(w, 404, "not_found", "no such title")
		return
	}
	if r.Method == http.MethodPut {
		var input struct {
			Tags []string `json:"tags"`
		}
		if err = json.NewDecoder(io.LimitReader(r.Body, 8193)).Decode(&input); err != nil || len(input.Tags) > 16 {
			writeError(w, 400, "invalid_tags", "Use at most 16 tags")
			return
		}
		for _, tag := range input.Tags {
			if strings.TrimSpace(tag) == "" || utf8.RuneCountInString(tag) > 64 {
				writeError(w, 400, "invalid_tags", "Each tag must contain between 1 and 64 characters")
				return
			}
		}
		if err = tagger.SetTitleTags(r.Context(), key, input.Tags); err != nil {
			h.writeServiceError(w, err)
			return
		}
	}
	tags, err := tagger.TitleTags(r.Context(), key)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"tags": tags})
}
