package admin

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
)

type catalogCursor struct {
	Key   core.RatingKey `json:"key"`
	Query string         `json:"query"`
}

func catalogCursorSignature(q core.BrowseQuery) string {
	raw, _ := json.Marshal(struct {
		Filter     core.Filter
		Sort       core.SortOrder
		Desc       bool
		Secondary  []core.SortSpec
		Visibility string
		Rules      any
		Fallback   string
	}{q.Filter, q.Sort, q.Desc, q.SecondarySorts, q.Filter.RuleVisibility, q.Filter.PreviewRules, q.Filter.PreviewFallback})
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum[:16])
}
func encodeCatalogCursor(q core.BrowseQuery, key core.RatingKey) string {
	raw, _ := json.Marshal(catalogCursor{key, catalogCursorSignature(q)})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeCatalogCursor(raw string, q core.BrowseQuery) (core.RatingKey, error) {
	if len(raw) > 512 {
		return 0, errors.New("Invalid cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, errors.New("Invalid cursor")
	}
	var cursor catalogCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.Key <= 0 || cursor.Query != catalogCursorSignature(q) {
		return 0, errors.New("The filters changed. Restart browsing")
	}
	return cursor.Key, nil
}
