package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Endpoints the catalogue sync uses to fill and refresh its mirror. They
// bypass the response cache: a sync reads each answer once, and caching
// a million of them would only evict what interactive reads need.

// MovieSummary is /movie/{id} without append_to_response: the fields a
// /discover result has, plus runtime, status and the IMDb id, in about
// 1.5 KB instead of the 130 KB a full detail fetch can be.
func (c *Client) MovieSummary(ctx context.Context, id int64) (*MovieDetails, error) {
	var out MovieDetails
	err := c.getUncachedJSON(ctx, fmt.Sprintf("/movie/%d", id), url.Values{}, &out)
	return &out, err
}

// TVSummary is /tv/{id} without append_to_response.
func (c *Client) TVSummary(ctx context.Context, id int64) (*TVDetails, error) {
	var out TVDetails
	err := c.getUncachedJSON(ctx, fmt.Sprintf("/tv/%d", id), url.Values{}, &out)
	return &out, err
}

// ChangedID is one entry of a /changes page.
type ChangedID struct {
	ID    int64 `json:"id"`
	Adult bool  `json:"adult"`
}

// ChangesMaxWindow is the longest start_date..end_date range /changes
// accepts ("Invalid date range: Should be a range no longer than 14 days",
// https://developer.themoviedb.org/reference/changes-movie-list).
const ChangesMaxWindow = 14 * 24 * time.Hour

// Changes pages /movie/changes or /tv/changes (tv true): the ids TMDB
// edited between start and end (UTC days, inclusive), 100 a page.
func (c *Client) Changes(ctx context.Context, tv bool, start, end time.Time, page int) (*Paged[ChangedID], error) {
	path := "/movie/changes"
	if tv {
		path = "/tv/changes"
	}
	q := url.Values{"start_date": {start.UTC().Format("2006-01-02")}, "end_date": {end.UTC().Format("2006-01-02")}}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	var out Paged[ChangedID]
	err := c.getUncachedJSON(ctx, path, q, &out)
	return &out, err
}

func (c *Client) getUncachedJSON(ctx context.Context, path string, q url.Values, out any) error {
	cfg := c.settings()
	if cfg.key == "" {
		return ErrNoKey
	}
	body, _, _, err := c.doWithRetry(ctx, c.buildURL(path, q, cfg), "", cfg)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("tmdb: decode %s: %w", path, err)
	}
	return nil
}
