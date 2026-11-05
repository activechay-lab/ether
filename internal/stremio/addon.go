// Package stremio is the Stremio add-on protocol, and the only model of it
// in Ether: manifests, routes and ids, and decoding every resource
// (streams into releases, catalogs, metas, subtitles, `tv` channels and
// their guide).
//
// It owns no network client. An Addon reads routes through a Fetcher; in
// the server that is the plugin manager's remote runtime, which guards,
// meters, redacts and health-checks every call to an installed add-on
// (internal/plugins). Ether ships no add-on URLs: the owner pastes the
// install link of an add-on they chose.
package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Response size limits per route.
const (
	MaxManifestBytes  = 1 << 20
	MaxStreamsBytes   = 8 << 20
	MaxCatalogBytes   = 8 << 20
	MaxMetaBytes      = 4 << 20
	MaxSubtitlesBytes = 2 << 20
	// MaxCatalogItems bounds one catalog read.
	MaxCatalogItems = 2000
)

// Fetcher reads one route of an add-on ("/stream/movie/tt1.json"). A
// non-2xx answer is an *HTTPError. limit bounds the body.
type Fetcher interface {
	Fetch(ctx context.Context, route string, limit int64) ([]byte, error)
}

// HTTPError is a non-2xx add-on answer. It names no URL: add-on links
// carry the owner's configuration, often a debrid key.
type HTTPError struct {
	Status     int
	RetryAfter string
}

func (e *HTTPError) Error() string {
	if e.Status == http.StatusForbidden || e.Status == http.StatusUnauthorized {
		return fmt.Sprintf("the add-on refused Ether (HTTP %d); some public add-on instances serve only the Stremio app, so use an instance that allows servers or host the add-on yourself", e.Status)
	}
	return fmt.Sprintf("the add-on answered HTTP %d", e.Status)
}

// RateLimited reports an answer that asks Ether to slow down.
func (e *HTTPError) RateLimited() bool {
	return e.Status == http.StatusTooManyRequests || e.Status == http.StatusServiceUnavailable && e.RetryAfter != ""
}

// Addon is one installed add-on: its manifest and how to reach it.
type Addon struct {
	Manifest Manifest
	Fetcher  Fetcher
}

func (a Addon) get(ctx context.Context, route string, limit int64, out any) error {
	if a.Fetcher == nil {
		return errors.New("stremio: the add-on is not reachable")
	}
	body, err := a.Fetcher.Fetch(ctx, route, limit)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("stremio: the add-on answered with invalid JSON")
	}
	return nil
}

// Route builds a resource route: /{resource}/{type}/{id}[/{extra}].json.
// Extras are sorted and query-escaped, as the Stremio SDK writes them.
func Route(resource, typ, id string, extra map[string]string) string {
	r := "/" + resource + "/" + url.PathEscape(typ) + "/" + escapeID(id)
	keys := make([]string, 0, len(extra))
	for k, v := range extra {
		if k != "" && v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(extra[k]))
	}
	if len(parts) > 0 {
		r += "/" + strings.Join(parts, "&")
	}
	return r + ".json"
}

func escapeID(id string) string { return url.PathEscape(id) }

// ValidateExtras checks catalog extras against the declaration: required
// extras present, options respected, nothing undeclared. skip is the
// paging extra the reader adds itself.
func (c Catalog) ValidateExtras(extra map[string]string) error {
	declared := map[string]Extra{}
	for _, e := range c.Extras() {
		declared[e.Name] = e
		if e.IsRequired && e.Name != "skip" && strings.TrimSpace(extra[e.Name]) == "" {
			return fmt.Errorf("%s is required by the add-on", e.Name)
		}
	}
	for k, v := range extra {
		if v == "" {
			continue
		}
		e, ok := declared[k]
		if !ok {
			return fmt.Errorf("%s is not declared by the add-on", k)
		}
		if len(e.Options) > 0 && !contains(e.Options, v) {
			return fmt.Errorf("%s must be one of the add-on's declared options", k)
		}
	}
	return nil
}

// Streams reads the streams for a lookup.
func (a Addon) Streams(ctx context.Context, look Lookup) ([]Stream, error) {
	return a.StreamsFor(ctx, look.Type, look.ID)
}

// StreamsFor reads stream/{type}/{id}.json.
func (a Addon) StreamsFor(ctx context.Context, typ, id string) ([]Stream, error) {
	var ans struct {
		Streams []Stream `json:"streams"`
	}
	if err := a.get(ctx, Route(ResourceStream, typ, id, nil), MaxStreamsBytes, &ans); err != nil {
		return nil, err
	}
	return ans.Streams, nil
}

// MetaPreview is one catalog entry.
type MetaPreview struct {
	ID            string   `json:"id"`
	Type          string   `json:"type"`
	Name          string   `json:"name"`
	Poster        string   `json:"poster,omitempty"`
	Logo          string   `json:"logo,omitempty"`
	Description   string   `json:"description,omitempty"`
	Genres        []string `json:"genres,omitempty"`
	ReleaseInfo   string   `json:"releaseInfo,omitempty"`
	IMDBRating    string   `json:"imdbRating,omitempty"`
	IMDbID        string   `json:"imdb_id,omitempty"`
	TMDBID        any      `json:"tmdb_id,omitempty"`
	BehaviorHints struct {
		DefaultVideoID     string `json:"defaultVideoId,omitempty"`
		HasScheduledVideos bool   `json:"hasScheduledVideos,omitempty"`
	} `json:"behaviorHints,omitzero"`
}

// CatalogPage reads one catalog page. hasMore is the add-on's own hint
// (nil when absent).
func (a Addon) CatalogPage(ctx context.Context, typ, id string, extra map[string]string) (metas []MetaPreview, hasMore *bool, err error) {
	var ans struct {
		Metas   []MetaPreview `json:"metas"`
		HasMore *bool         `json:"hasMore"`
	}
	if err := a.get(ctx, Route(ResourceCatalog, typ, id, extra), MaxCatalogBytes, &ans); err != nil {
		return nil, nil, err
	}
	return ans.Metas, ans.HasMore, nil
}

// Catalog reads a whole catalog, paging with skip when the catalog
// declares it, until an empty page, a page of only duplicates (an add-on
// ignoring skip), hasMore false, a 404 after the first page, or maxItems
// (clamped to MaxCatalogItems). Duplicate ids keep their first place.
func (a Addon) Catalog(ctx context.Context, c Catalog, extra map[string]string, maxItems int) ([]MetaPreview, error) {
	if maxItems <= 0 || maxItems > MaxCatalogItems {
		maxItems = MaxCatalogItems
	}
	var out []MetaPreview
	seen := map[string]bool{}
	skip := 0
	for len(out) < maxItems {
		ex := make(map[string]string, len(extra)+1)
		for k, v := range extra {
			ex[k] = v
		}
		if skip > 0 {
			ex["skip"] = strconv.Itoa(skip)
		}
		page, more, err := a.CatalogPage(ctx, c.Type, c.ID, ex)
		if err != nil {
			var he *HTTPError
			if skip > 0 && errors.As(err, &he) && he.Status == http.StatusNotFound {
				break
			}
			return out, err
		}
		added := 0
		for _, m := range page {
			if m.ID == "" || seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			out = append(out, m)
			added++
			if len(out) >= maxItems {
				break
			}
		}
		skip += len(page)
		if added == 0 || !c.SupportsSkip() || (more != nil && !*more) {
			break
		}
	}
	return out, nil
}

// Video is one entry of a meta's videos list. `tv` add-ons publish their
// schedule as videos with timestamps.
type Video struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Name      string    `json:"name,omitempty"`
	Overview  string    `json:"overview,omitempty"`
	Season    int       `json:"season,omitempty"`
	Episode   int       `json:"episode,omitempty"`
	Released  time.Time `json:"released,omitzero"`
	StartTime time.Time `json:"startTime,omitzero"`
	EndTime   time.Time `json:"endTime,omitzero"`
	Thumbnail string    `json:"thumbnail,omitempty"`
}

// Meta is a meta document.
type Meta struct {
	MetaPreview
	Background string   `json:"background,omitempty"`
	Runtime    string   `json:"runtime,omitempty"`
	Released   string   `json:"released,omitempty"`
	Year       any      `json:"year,omitempty"`
	Cast       []string `json:"cast,omitempty"`
	Director   []string `json:"director,omitempty"`
	Writer     []string `json:"writer,omitempty"`
	Country    string   `json:"country,omitempty"`
	Language   string   `json:"language,omitempty"`
	Website    string   `json:"website,omitempty"`
	Videos     []Video  `json:"videos,omitempty"`
}

// Meta reads meta/{type}/{id}.json.
func (a Addon) Meta(ctx context.Context, typ, id string) (Meta, error) {
	var ans struct {
		Meta Meta `json:"meta"`
	}
	if err := a.get(ctx, Route(ResourceMeta, typ, id, nil), MaxMetaBytes, &ans); err != nil {
		return Meta{}, err
	}
	return ans.Meta, nil
}

// GuideCatalog reads a `tv` catalog's dated guide pages (the epgProvider
// extension), which answer detailed metas instead of previews.
func (a Addon) GuideCatalog(ctx context.Context, id, date string) ([]Meta, error) {
	var out []Meta
	seen := map[string]bool{}
	for skip := 0; len(out) < MaxCatalogItems; {
		extra := map[string]string{"date": date}
		if skip > 0 {
			extra["skip"] = strconv.Itoa(skip)
		}
		var page struct {
			MetasDetailed []Meta `json:"metasDetailed"`
		}
		if err := a.get(ctx, Route(ResourceCatalog, TypeTV, id, extra), MaxCatalogBytes, &page); err != nil {
			var he *HTTPError
			if skip > 0 && errors.As(err, &he) && he.Status == http.StatusNotFound {
				break
			}
			return nil, err
		}
		added := 0
		for _, m := range page.MetasDetailed {
			if m.ID == "" || seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			out = append(out, m)
			added++
			if len(out) == MaxCatalogItems {
				break
			}
		}
		if added == 0 {
			break
		}
		skip += len(page.MetasDetailed)
	}
	return out, nil
}

// Subtitle is one subtitle an add-on offers.
type Subtitle struct {
	ID          string `json:"id,omitempty"`
	URL         string `json:"url"`
	Lang        string `json:"lang"`
	Filename    string `json:"filename,omitempty"`
	SubFileName string `json:"SubFileName,omitempty"`
	HashMatch   bool   `json:"hashMatch,omitempty"`
	Format      string `json:"format,omitempty"`
	Name        string `json:"name,omitempty"`
}

// Subtitles lists subtitles/{type}/{id}[/extra].json. Listing only:
// nothing is downloaded here.
func (a Addon) Subtitles(ctx context.Context, typ, id string, extra map[string]string) ([]Subtitle, error) {
	var ans struct {
		Subtitles []Subtitle `json:"subtitles"`
	}
	if err := a.get(ctx, Route(ResourceSubtitles, typ, id, extra), MaxSubtitlesBytes, &ans); err != nil {
		return nil, err
	}
	return ans.Subtitles, nil
}

// ParseCatalogRef reads an external identity from a catalog id: "tt…" or
// "imdb:tt…" → IMDb; "tmdb:550", "tmdb:movie:550" → TMDB; "tvdb:N" → TVDB.
// Series ids may carry ":season:episode"; the show id leads.
func ParseCatalogRef(id string) (imdb string, tmdb, tvdb int64) {
	id = strings.TrimSpace(id)
	if v := IMDbID(strings.SplitN(id, ":", 2)[0]); v != "" {
		return v, 0, 0
	}
	lower := strings.ToLower(id)
	if rest, ok := strings.CutPrefix(lower, "imdb:"); ok {
		return IMDbID(strings.SplitN(rest, ":", 2)[0]), 0, 0
	}
	num := func(rest string) int64 {
		for _, p := range strings.Split(rest, ":") {
			if n, err := strconv.ParseInt(p, 10, 64); err == nil && n > 0 {
				return n
			}
			if p != "movie" && p != "series" && p != "tv" && p != "show" {
				return 0
			}
		}
		return 0
	}
	if rest, ok := strings.CutPrefix(lower, "tmdb:"); ok {
		return "", num(rest), 0
	}
	if rest, ok := strings.CutPrefix(lower, "tvdb:"); ok {
		return "", 0, num(rest)
	}
	return "", 0, 0
}

// Ref reads a catalog entry's identity: its id, else explicit imdb_id or
// tmdb_id fields some add-ons add.
func (m MetaPreview) Ref() (imdb string, tmdb, tvdb int64) {
	imdb, tmdb, tvdb = ParseCatalogRef(m.ID)
	if imdb == "" && tmdb == 0 && tvdb == 0 {
		imdb = IMDbID(m.IMDbID)
		switch v := m.TMDBID.(type) {
		case float64:
			tmdb = int64(v)
		case string:
			tmdb, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	return imdb, tmdb, tvdb
}
