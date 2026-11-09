package xtream

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

const pageSize = 250

func jsonEncoder(w http.ResponseWriter, v any) error { return json.NewEncoder(w).Encode(v) }

// walk keeps one policy-filtered page in memory and never fetches item details
// per listing row. Category selection is pushed down to the library query.
func (i *instance) walk(r *http.Request, l login, kind string, emit func(frontend.Title, string) error) error {
	views, err := i.host.Experience.Views(r.Context(), l.viewer)
	if err != nil {
		return err
	}
	cat := r.URL.Query().Get("category_id")
	for _, v := range views {
		if string(v.Type) != kind || cat != "" && cat != "0" && cat != v.ID {
			continue
		}
		if exporter, ok := i.host.Experience.(frontend.LibraryExporter); ok {
			err := exportPages(r.Context(), exporter, l, v.ID, func(page frontend.LibraryExportPage) error {
				if err := i.pageImages(r, l, page.Items); err != nil {
					return err
				}
				for _, t := range page.Items {
					if string(t.Type) == kind {
						if err := emit(t, v.ID); err != nil {
							return err
						}
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
			continue
		}
		for offset := 0; ; {
			if err := r.Context().Err(); err != nil {
				return err
			}
			page, err := i.host.Experience.LibraryItems(r.Context(), l.viewer, l.device, v.ID, frontend.ItemQuery{Offset: offset, Limit: pageSize, Sort: "title"})
			if err != nil {
				return err
			}
			if len(page.Items) > pageSize {
				return errors.New("unbounded catalogue response")
			}
			if err := i.pageImages(r, l, page.Items); err != nil {
				return err
			}
			for _, t := range page.Items {
				if string(t.Type) == kind {
					if err := emit(t, v.ID); err != nil {
						return err
					}
				}
			}
			offset += len(page.Items)
			if offset >= page.Total {
				break
			}
			if len(page.Items) == 0 {
				return errors.New("incomplete catalogue response")
			}
		}
	}
	return nil
}

// exportPages hands view's export pages to each in order, reading the next
// page while each handles this one: the database read of a page overlaps
// the signing and encoding of the one before. At most two pages are held.
// It returns nil only after the last page; the reader stops with each.
func exportPages(ctx context.Context, exporter frontend.LibraryExporter, l login, view string, each func(frontend.LibraryExportPage) error) error {
	ctx, cancel := context.WithCancel(ctx)
	type result struct {
		page frontend.LibraryExportPage
		err  error
	}
	next := make(chan result, 1)
	go func() {
		defer close(next)
		after := frontend.RatingKey(0)
		for {
			page, err := exporter.ExportLibraryPage(ctx, l.viewer, l.device, view, after, pageSize)
			switch {
			case err != nil:
			case len(page.Items) > pageSize:
				err = errors.New("unbounded export")
			case !page.Done && page.Next <= after:
				err = errors.New("invalid export cursor")
			}
			select {
			case next <- result{page, err}:
			case <-ctx.Done():
				return
			}
			if err != nil || page.Done {
				return
			}
			after = page.Next
		}
	}()
	defer func() {
		// The reader never outlives the request handler.
		cancel()
		for range next {
		}
	}()
	for res := range next {
		if res.err != nil {
			return res.err
		}
		if err := each(res.page); err != nil {
			return err
		}
		if res.page.Done {
			return nil
		}
	}
	// The reader stopped before the last page: the request was cancelled.
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("incomplete export")
}

func (i *instance) titleImage(r *http.Request, l login, t frontend.Title) string {
	if image, ok := l.artwork[t.Key]; ok {
		return image
	}
	if t.PosterPath == "" {
		return ""
	}
	return i.image(r, l, t.Key, "", []frontend.Image{{Kind: "poster", Path: t.PosterPath}})
}
// movieRow and seriesRow are a listing row. Their fields are in byte order
// of their JSON names, the order apps have always received them in.
type movieRow struct {
	Added              string  `json:"added"`
	Cast               string  `json:"cast"`
	CategoryID         string  `json:"category_id"`
	ContainerExtension string  `json:"container_extension"`
	DirectSource       string  `json:"direct_source"`
	Director           string  `json:"director"`
	DurationSecs       int64   `json:"duration_secs"`
	Genre              string  `json:"genre"`
	Name               string  `json:"name"`
	Num                int     `json:"num"`
	Plot               string  `json:"plot"`
	Rating             string  `json:"rating"`
	Rating5Based       float64 `json:"rating_5based"`
	ReleaseDate        string  `json:"release_date"`
	StreamIcon         string  `json:"stream_icon"`
	StreamID           int32   `json:"stream_id"`
	StreamType         string  `json:"stream_type"`
	Title              string  `json:"title"`
	Year               int     `json:"year"`
}

type seriesRow struct {
	Added              string   `json:"added"`
	BackdropPath       []string `json:"backdrop_path"`
	Cast               string   `json:"cast"`
	CategoryID         string   `json:"category_id"`
	ContainerExtension string   `json:"container_extension"`
	Cover              string   `json:"cover"`
	DirectSource       string   `json:"direct_source"`
	Director           string   `json:"director"`
	DurationSecs       int64    `json:"duration_secs"`
	Genre              string   `json:"genre"`
	LastModified       string   `json:"last_modified"`
	Name               string   `json:"name"`
	Num                int      `json:"num"`
	Plot               string   `json:"plot"`
	Rating             string   `json:"rating"`
	Rating5Based       float64  `json:"rating_5based"`
	ReleaseDate        string   `json:"release_date"`
	SeriesID           int32    `json:"series_id"`
	StreamType         string   `json:"stream_type"`
	Title              string   `json:"title"`
	Year               int      `json:"year"`
}

func (i *instance) row(r *http.Request, l login, t frontend.Title, cat string, num int) any {
	image := i.titleImage(r, l, t)
	rating, added, genre := strconv.FormatFloat(t.Rating, 'f', 1, 64), unixString(t.AddedAt), strings.Join(t.Genres, ", ")
	cast, director, released, duration := personNames(t.Cast), personNames(t.Directors), dateString(t.Released), int64(t.Runtime/time.Second)
	if t.Type == "show" {
		return seriesRow{Added: added, BackdropPath: []string{}, Cast: cast, CategoryID: cat, ContainerExtension: "mkv", Cover: image, Director: director, DurationSecs: duration, Genre: genre,
			LastModified: unixString(t.UpdatedAt), Name: t.Title, Num: num, Plot: t.Summary, Rating: rating, Rating5Based: t.Rating / 2, ReleaseDate: released, SeriesID: l.ids[t.Key], StreamType: "series", Title: t.Title, Year: t.Year}
	}
	return movieRow{Added: added, Cast: cast, CategoryID: cat, ContainerExtension: "mkv", Director: director, DurationSecs: duration, Genre: genre, Name: t.Title, Num: num, Plot: t.Summary,
		Rating: rating, Rating5Based: t.Rating / 2, ReleaseDate: released, StreamIcon: image, StreamID: l.ids[t.Key], StreamType: "movie", Title: t.Title, Year: t.Year}
}
func (i *instance) catalog(w http.ResponseWriter, r *http.Request, l login, action string) {
	kind := "movie"
	if strings.Contains(action, "series") {
		kind = "show"
	}
	if strings.HasSuffix(action, "categories") {
		vs, err := i.host.Experience.Views(r.Context(), l.viewer)
		if err != nil {
			http.Error(w, "Unavailable", 503)
			return
		}
		out := []map[string]any{}
		for _, v := range vs {
			if string(v.Type) == kind {
				out = append(out, map[string]any{"category_id": v.ID, "category_name": v.Name, "parent_id": 0})
			}
		}
		jsonResponse(w, out)
		return
	}
	if strings.HasSuffix(action, "info") {
		i.info(w, r, l, kind)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// A whole library is one response: write it in large chunks, not two
	// writes per row through the response writer's wrappers.
	out := bufio.NewWriterSize(w, 64<<10)
	n := 0
	err := i.walk(r, l, kind, func(t frontend.Title, cat string) error {
		data, err := json.Marshal(i.row(r, l, t, cat, n+1))
		if err != nil {
			return err
		}
		prefix := "["
		if n > 0 {
			prefix = ","
		}
		if _, err = out.WriteString(prefix); err != nil {
			return err
		}
		n++
		_, err = out.Write(data)
		return err
	})
	if err != nil {
		if n == 0 {
			http.Error(w, "Unavailable", http.StatusServiceUnavailable)
			return
		}
		panic(http.ErrAbortHandler)
	}
	if n == 0 {
		_, _ = out.WriteString("[")
	}
	_, _ = out.WriteString("]")
	_ = out.Flush()
}
func (i *instance) info(w http.ResponseWriter, r *http.Request, l login, kind string) {
	param := "vod_id"
	if kind == "show" {
		param = "series_id"
	}
	id, err := i.resolveItem(r, r.URL.Query().Get(param))
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	detail, err := i.host.Experience.Item(r.Context(), frontend.ItemRequest{Viewer: l.viewer, Device: l.device, Key: frontend.RatingKey(id)})
	if err != nil || string(detail.Title.Type) != kind {
		http.NotFound(w, r)
		return
	}
	t := detail.Title
	if err := i.pageImages(r, l, []frontend.Title{t}); err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	row := i.row(r, l, t, "", 1)
	info := map[string]any{"name": t.Title, "plot": t.Summary, "description": t.Summary, "movie_image": i.titleImage(r, l, t), "cover": i.titleImage(r, l, t), "rating": t.Rating, "year": t.Year, "duration_secs": int64(t.Runtime / time.Second), "duration": fmt.Sprintf("%02d:%02d:%02d", int(t.Runtime.Hours()), int(t.Runtime.Minutes())%60, int(t.Runtime.Seconds())%60)}
	info["tmdb_id"], info["cover_big"], info["genre"] = t.IDs.TMDB, i.titleImage(r, l, t), strings.Join(t.Genres, ", ")
	info["release_date"], info["releaseDate"] = dateString(t.Released), dateString(t.Released)
	info["cast"], info["director"], info["backdrop_path"] = personNames(t.Cast), personNames(t.Directors), []string{}
	info["last_modified"], info["youtube_trailer"], info["episode_run_time"] = unixString(t.UpdatedAt), "", int(t.Runtime/time.Minute)
	if kind == "movie" {
		jsonResponse(w, map[string]any{"info": info, "movie_data": row})
		return
	}
	// A single series is bounded; large responses are refused before allocation grows.
	seasons := []map[string]any{}
	episodes := map[string][]map[string]any{}
	count := 0
	err = i.children(r, l, t.Key, func(season frontend.Title) error {
		if season.Type != "season" {
			return nil
		}
		seasons = append(seasons, map[string]any{"id": l.ids[season.Key], "name": season.Title, "season_number": season.Season, "episode_count": season.ChildCount})
		return i.children(r, l, season.Key, func(ep frontend.Title) error {
			if ep.Type != "episode" {
				return nil
			}
			count++
			if count > 20000 {
				return errors.New("series capacity exceeded")
			}
			s := strconv.Itoa(ep.Season)
			episodes[s] = append(episodes[s], map[string]any{"id": strconv.FormatInt(int64(l.ids[ep.Key]), 10), "episode_num": ep.Episode, "title": ep.Title, "container_extension": "mkv", "season": ep.Season, "direct_source": "", "info": map[string]any{"plot": ep.Summary, "duration_secs": int64(ep.Runtime / time.Second), "movie_image": i.titleImage(r, l, ep)}})
			return nil
		})
	})
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	jsonResponse(w, map[string]any{"info": info, "seasons": seasons, "episodes": episodes})
}
func (i *instance) children(r *http.Request, l login, key frontend.RatingKey, emit func(frontend.Title) error) error {
	for offset := 0; offset < 20000; {
		p, err := i.host.Experience.Children(r.Context(), l.viewer, l.device, key, frontend.ItemQuery{Offset: offset, Limit: pageSize})
		if err != nil {
			return err
		}
		if len(p.Items) > pageSize {
			return errors.New("unbounded child response")
		}
		if err := i.pageImages(r, l, p.Items); err != nil {
			return err
		}
		pageIDs, pageArtwork := maps.Clone(l.ids), maps.Clone(l.artwork)
		for _, t := range p.Items {
			l.ids[t.Key], l.artwork[t.Key] = pageIDs[t.Key], pageArtwork[t.Key]
			if err := emit(t); err != nil {
				return err
			}
		}
		offset += len(p.Items)
		if offset >= p.Total {
			return nil
		}
		if len(p.Items) == 0 {
			return errors.New("incomplete children")
		}
	}
	return errors.New("series capacity exceeded")
}

// Stateless page artwork preserves the neutral catalogue's selected identity
// without one metadata lookup or database grant write per title.
func (i *instance) pageImages(r *http.Request, l login, titles []frontend.Title) error {
	clear(l.artwork)
	clear(l.ids)
	refs := make([]string, len(titles))
	for j, t := range titles {
		refs[j] = strconv.FormatInt(int64(t.Key), 10)
	}
	allocated, err := i.host.IDs.CompactBatch(r.Context(), "xtream", "item", refs)
	if err != nil {
		return err
	}
	for j, t := range titles {
		l.ids[t.Key] = allocated[refs[j]]
	}
	signer, ok := i.host.ImageGrants.(frontend.ImagePageSigner)
	if !ok {
		for _, t := range titles {
			l.artwork[t.Key] = ""
		}
		return nil
	}
	base := i.base(r)
	// One signing per export page (pageSize titles).
	for start := 0; start < len(titles); start += pageSize {
		page := titles[start:min(start+pageSize, len(titles))]
		// Xtream rows carry a poster URL only, never its size or hashes.
		grants, err := signer.SignPage(frontend.WithoutArtworkMetadata(r.Context()), frontend.ImagePageRequest{Credential: l.password, Device: l.device, Titles: page, Kinds: []string{frontend.ImagePoster}})
		if err != nil {
			return err
		}
		for _, t := range page {
			l.artwork[t.Key] = ""
		}
		for _, g := range grants {
			if len(g.Images) > 0 && base != "" {
				l.artwork[g.Item] = base + "/image/" + g.Images[0].Secret
			}
		}
	}
	return nil
}

func (i *instance) resolveItem(r *http.Request, id string) (frontend.RatingKey, error) {
	n, err := strconv.ParseInt(id, 10, 32)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
		return 0, errors.New("invalid identifier")
	}
	ref, err := i.host.IDs.Resolve(r.Context(), "xtream", "item", int32(n))
	if err != nil {
		return 0, err
	}
	key, err := strconv.ParseInt(ref, 10, 64)
	return frontend.RatingKey(key), err
}

func unixString(t time.Time) string {
	if t.IsZero() {
		return "0"
	}
	return strconv.FormatInt(t.Unix(), 10)
}
func dateString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}
func personNames(people []frontend.Person) string {
	names := make([]string, 0, len(people))
	for _, p := range people {
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}
