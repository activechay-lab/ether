package mdblist

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// HistoryPayload uses only identities represented unambiguously by Ether's
// native movie and episode keys. Show and season keys are skipped. Episodes
// are sent in TMDb numbering (nested, by season/episode number), which is what
// MDBList's sync documents as the canonical form.
// https://api.mdblist.com/docs/ (POST /sync/watched)
func HistoryPayload(key core.RatingKey, at time.Time) (map[string]any, error) {
	if !key.Valid() || key.TMDB() <= 0 || at.IsZero() {
		return nil, errors.New("mdblist: unsupported history identity or timestamp")
	}
	ids := map[string]any{"tmdb": key.TMDB()}
	watched := at.UTC().Format(time.RFC3339)
	switch key.Kind() {
	case core.KindMovie:
		return map[string]any{"movies": []any{map[string]any{"ids": ids, "watched_at": watched}}}, nil
	case core.KindEpisode:
		if key.Episode() <= 0 {
			return nil, errors.New("mdblist: unsupported episode number")
		}
		episode := map[string]any{"number": key.Episode(), "watched_at": watched}
		season := map[string]any{"number": key.Season(), "episodes": []any{episode}}
		return map[string]any{"shows": []any{map[string]any{"ids": ids, "seasons": []any{season}}}}, nil
	default:
		return nil, errors.New("mdblist: unsupported history identity")
	}
}

// PushHistory sends one completion. MDBList records a play per request and
// treats a re-sent already-watched item without a date as a no-op, so a retry
// after an uncertain response is idempotent. A 4xx that means the item cannot
// be written is reported as ErrUnsupported so the caller can drop it.
func (c Client) PushHistory(ctx context.Context, apiKey string, key core.RatingKey, at time.Time) error {
	payload, err := HistoryPayload(key, at)
	if err != nil {
		return err
	}
	var result struct {
		NotFound json.RawMessage `json:"not_found"`
	}
	if err = c.request(ctx, http.MethodPost, "/sync/watched", apiKey, nil, payload, &result); err != nil {
		return err
	}
	if len(result.NotFound) > 0 && string(result.NotFound) != "null" {
		return ErrUnsupported
	}
	return nil
}

// ErrUnsupported marks a history identity MDBList cannot resolve or store.
var ErrUnsupported = errors.New("mdblist: history identity unsupported")

// parseIDs accepts MDBList's numeric ids, which are sometimes serialised as
// strings. Absent or non-numeric ids yield 0.
func parseTMDB(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return 0
	}
	n, _ = strconv.ParseInt(s, 10, 64)
	return n
}
