package mdblist

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
)

// ParseHistory accepts only actual timestamps and TMDB identities. Movies are
// read from `movies[]`; watched episodes from the flat `episodes[]`, which
// carry the parent show's TMDb id and TMDb season/episode numbers, exactly the
// grain Ether stores. Shows and seasons are skipped: their watched state is
// already implied by their episodes.
// https://api.mdblist.com/docs/ (GET /sync/watched)
func ParseHistory(user int64, data []byte) ([]state.ImportRecord, int, error) {
	if user <= 0 {
		return nil, 0, errors.New("mdblist: invalid viewer")
	}
	type ids struct {
		TMDB json.RawMessage `json:"tmdb"`
	}
	var response struct {
		Movies []struct {
			WatchedAt     string `json:"watched_at"`
			LastWatchedAt string `json:"last_watched_at"`
			Movie         struct {
				IDs ids `json:"ids"`
			} `json:"movie"`
		} `json:"movies"`
		Episodes []struct {
			WatchedAt     string `json:"watched_at"`
			LastWatchedAt string `json:"last_watched_at"`
			Episode       struct {
				Season int `json:"season"`
				Number int `json:"number"`
				Show   struct {
					IDs ids `json:"ids"`
				} `json:"show"`
			} `json:"episode"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, 0, err
	}
	var out []state.ImportRecord
	skipped := 0
	// The full GET returns `watched_at` for movies and `last_watched_at` for
	// episodes; `plays=all` swaps them. Accept either, preferring the first
	// non-empty.
	stamp := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	add := func(key core.RatingKey, ts string) {
		at, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || at.IsZero() || !key.Valid() {
			skipped++
			return
		}
		out = append(out, state.ImportRecord{UserID: user, Key: key, Views: 1, ViewedAt: at.UTC()})
	}
	for _, m := range response.Movies {
		n := parseTMDB(m.Movie.IDs.TMDB)
		if n <= 0 || n >= 1_000_000_000_000_000 {
			skipped++
			continue
		}
		add(core.MovieKey(n), stamp(m.WatchedAt, m.LastWatchedAt))
	}
	for _, e := range response.Episodes {
		n := parseTMDB(e.Episode.Show.IDs.TMDB)
		if n <= 0 || n >= 100_000_000 || e.Episode.Season < 0 || e.Episode.Season >= 1000 || e.Episode.Number <= 0 || e.Episode.Number >= 10000 {
			skipped++
			continue
		}
		add(core.EpisodeKey(n, e.Episode.Season, e.Episode.Number), stamp(e.LastWatchedAt, e.WatchedAt))
	}
	return out, skipped, nil
}

// PullHistory reads the watched snapshot with cursor pagination. Movies and
// episodes are returned by default; the returned cursor must only be saved
// after all imported records commit. A first pull (empty cursor) reads from
// the beginning; later pulls pass the cursor to continue.
// https://api.mdblist.com/docs/ (GET /sync/watched)
func (c Client) PullHistory(ctx context.Context, user int64, apiKey, cursor string) ([]state.ImportRecord, string, int, error) {
	var records []state.ImportRecord
	skipped := 0
	for page := 0; page < 1000; page++ {
		query := url.Values{"limit": {"1000"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var data json.RawMessage
		if err := c.request(ctx, http.MethodGet, "/sync/watched", apiKey, query, nil, &data); err != nil {
			return nil, "", 0, err
		}
		r, n, err := ParseHistory(user, data)
		if err != nil {
			return nil, "", 0, err
		}
		records = append(records, r...)
		skipped += n
		var envelope struct {
			Pagination struct {
				NextCursor string `json:"next_cursor"`
			} `json:"pagination"`
		}
		_ = json.Unmarshal(data, &envelope)
		if envelope.Pagination.NextCursor == "" {
			return records, cursor, skipped, nil
		}
		cursor = envelope.Pagination.NextCursor
	}
	return nil, "", 0, errors.New("mdblist: history pagination did not terminate")
}
