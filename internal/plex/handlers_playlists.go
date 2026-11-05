package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

// maxExpand caps how many items a URI may expand to (a whole section).
const maxExpand = experience.PlaylistExpandLimit

var (
	reMetadataPath   = regexp.MustCompile(`^/library/metadata/([\d,]+)(?:/(children|allLeaves|grandchildren))?/?$`)
	reCollectionPath = regexp.MustCompile(`^/library/collections/(\d+)(?:/(children|items))?/?$`)
	rePlaylistPath   = regexp.MustCompile(`^/playlists/(\d+)(?:/items)?/?$`)
	reSectionPath    = regexp.MustCompile(`^/library/sections/(\d+)/(all|unwatched|newest|recentlyAdded)$`)
)

// uriPath extracts the library path (and query) from a Plex content URI:
//
//	server://{machineId}/com.plexapp.plugins.library/library/metadata/1,2
//	library://{sectionUUID}/item/%2Flibrary%2Fmetadata%2F123
//	library://{sectionUUID}/directory/%2Flibrary%2Fsections%2F1%2Fall%3Ftype%3D1
//	/library/metadata/123
func uriPath(uri string) (string, error) {
	switch {
	case strings.HasPrefix(uri, "server://"):
		rest := strings.TrimPrefix(uri, "server://")
		_, rest, ok := strings.Cut(rest, "/") // machine id
		if !ok {
			return "", fmt.Errorf("bad uri %q", uri)
		}
		_, rest, ok = strings.Cut(rest, "/") // provider
		if !ok {
			return "", fmt.Errorf("bad uri %q", uri)
		}
		p := "/" + rest
		if dec, err := url.PathUnescape(p); err == nil && strings.Contains(p, "%2F") {
			p = dec
		}
		return p, nil
	case strings.HasPrefix(uri, "library://"):
		rest := strings.TrimPrefix(uri, "library://")
		_, rest, ok := strings.Cut(rest, "/")
		if !ok {
			return "", fmt.Errorf("bad uri %q", uri)
		}
		_, enc, ok := strings.Cut(rest, "/") // "item" or "directory"
		if !ok {
			return "", fmt.Errorf("bad uri %q", uri)
		}
		p, err := url.PathUnescape(enc)
		if err != nil {
			return "", err
		}
		return p, nil
	case strings.HasPrefix(uri, "/"):
		return uri, nil
	}
	return "", fmt.Errorf("unsupported uri %q", uri)
}

// expandKey expands containers (show, season, collection, playlist) into
// playable items.
func (s *Server) expandKey(ctx context.Context, pr *Request, k core.RatingKey) ([]core.RatingKey, error) {
	if k.Kind() == core.KindPlaylist {
		pl, err := s.playlist(ctx, pr.User, k)
		if err != nil {
			return nil, err
		}
		if pl.SmartURI != "" {
			copyRequest := *pr
			copyRequest.HasPage, copyRequest.Start, copyRequest.Size = true, 0, maxExpand
			titles, _, _, err := s.smartPlaylistBrowse(ctx, &copyRequest, pl.SmartURI)
			if err != nil {
				return nil, err
			}
			var out []core.RatingKey
			for _, title := range titles {
				keys, err := s.expandKey(ctx, pr, title.Key)
				if err == nil {
					out = append(out, keys...)
				}
				if len(out) >= maxExpand {
					break
				}
			}
			return capKeys(out), nil
		}
	}
	selection := experience.PlaylistSelection{Playlists: s.d.Playlists, Collections: s.d.Collections, Visibility: s.visibility(), Children: func(ctx context.Context, key core.RatingKey) ([]core.Title, error) {
		return s.metadataChildren(ctx, pr, key)
	}, Grandchildren: func(ctx context.Context, key core.RatingKey) ([]core.Title, error) {
		return s.metadataGrandchildren(ctx, pr, key)
	}}
	return selection.Expand(ctx, pr.User, k)
}

// smartSectionURI keeps only a local section query, without client credentials.
func (s *Server) smartSectionURI(u *core.User, uri string) (string, error) {
	p, err := uriPath(uri)
	if err != nil {
		return "", err
	}
	path, raw, _ := strings.Cut(p, "?")
	m := reSectionPath.FindStringSubmatch(path)
	if len(m) == 0 || m[2] != "all" {
		return "", core.ErrNotFound
	}
	id, _ := strconv.Atoi(m[1])
	sec, ok := s.sectionByID(id)
	if !ok || !s.canSeeSection(u, id) {
		return "", core.ErrNotFound
	}
	q, err := url.ParseQuery(raw)
	if err != nil {
		return "", err
	}
	t := typeFromParam(q.Get("type"))
	if t != "" && t != sec.Type && !(sec.Type == core.TypeShow && (t == core.TypeSeason || t == core.TypeEpisode)) {
		return "", core.ErrNotFound
	}
	var safe []string
	for _, pair := range strings.Split(raw, "&") {
		key, _, _ := strings.Cut(pair, "=")
		key, err = url.QueryUnescape(key)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(strings.ToLower(key), "x-plex-") || strings.HasPrefix(strings.ToLower(key), "x-ether-") {
			continue
		}
		if pair != "" {
			safe = append(safe, pair)
		}
	}
	return path + "?" + strings.Join(safe, "&"), nil
}

func (s *Server) smartPlaylistBrowse(ctx context.Context, pr *Request, uri string) ([]core.Title, int, int, error) {
	p, err := s.smartSectionURI(pr.User, uri)
	if err != nil {
		return nil, 0, 0, err
	}
	path, raw, _ := strings.Cut(p, "?")
	m := reSectionPath.FindStringSubmatch(path)
	id, _ := strconv.Atoi(m[1])
	sec, _ := s.sectionByID(id)
	q, _ := url.ParseQuery(raw)
	t := typeFromParam(q.Get("type"))
	if t == "" {
		t = sec.Type
	}
	br := s.parseBrowse(ctx, sec, t, pr.User, q, raw)
	if t == core.TypeShow {
		// Select episodes by the complete show predicate in SQL. Do not
		// reinterpret show title/year/watch rules as episode rules or expand
		// all matching shows in memory before paging.
		show := br.q.Filter
		if br.unwatched != nil || br.inProgress != nil || br.lastViewedAfter > 0 || br.lastViewedBefore > 0 {
			var uid int64
			if pr.User != nil && s.d.State != nil {
				uid = pr.User.ID
			}
			state := core.FilterGroup{UserID: uid, Unwatched: br.unwatched, InProgress: br.inProgress, LastViewedAfter: br.lastViewedAfter, LastViewedBefore: br.lastViewedBefore}
			if show.Group == nil {
				show.Group = &state
			} else {
				show.Group = &core.FilterGroup{Operator: "and", Children: []core.FilterGroup{*show.Group, state}}
			}
			br.unwatched, br.inProgress = nil, nil
			br.lastViewedAfter, br.lastViewedBefore = 0, 0
		}
		br.q.Filter = core.Filter{Type: core.TypeEpisode, Group: &core.FilterGroup{ParentOnly: true, Filter: &show}}
	}
	return s.browse(ctx, pr, br)
}

func titleKeys(ts []core.Title) []core.RatingKey {
	out := make([]core.RatingKey, len(ts))
	for i := range ts {
		out[i] = ts[i].Key
	}
	return out
}

// resolveURI turns a content URI into playable rating keys.
func (s *Server) resolveURI(ctx context.Context, pr *Request, uri string) ([]core.RatingKey, error) {
	p, err := uriPath(uri)
	if err != nil {
		return nil, err
	}
	path, rawQuery, _ := strings.Cut(p, "?")
	switch {
	case path == "/hubs/items" || strings.HasPrefix(path, "/hubs/items/"):
		q, err := url.ParseQuery(rawQuery)
		if err != nil {
			return nil, err
		}
		id := q.Get("identifier")
		if path != "/hubs/items" {
			id, err = url.PathUnescape(strings.TrimPrefix(path, "/hubs/items/"))
			if err != nil {
				return nil, err
			}
		}
		h, ok := s.findHub(ctx, pr, id, parseSectionList(q.Get("contentDirectoryID")), q.Get("metadataID"), maxExpand)
		if !ok {
			return nil, core.ErrNotFound
		}
		// Use the rendered row so availability and viewer restrictions match
		// the items the client was offered, including episode parent policy.
		var out []core.RatingKey
		for _, md := range s.renderHub(ctx, pr, h, hubCtx{}).Metadata {
			k, err := core.ParseRatingKey(md.RatingKey)
			if err != nil {
				continue
			}
			sub, err := s.expandKey(ctx, pr, k)
			if err == nil {
				out = append(out, sub...)
			}
			if len(out) >= maxExpand {
				break
			}
		}
		return capKeys(out), nil
	case reMetadataPath.MatchString(path):
		m := reMetadataPath.FindStringSubmatch(path)
		keys, err := parseKeys(m[1])
		if err != nil {
			return nil, err
		}
		var out []core.RatingKey
		for _, k := range keys {
			// An addressed title (or the show above it) the viewer may
			// not see never reaches a playlist or play queue. Policy only:
			// discovery evidence hides a recent title from lists but must
			// not stop a viewer queueing it to play.
			switch k.Kind() {
			case core.KindShow, core.KindSeason, core.KindMovie, core.KindEpisode:
				if t, err := s.metadataTitle(ctx, pr, k); err == nil && !s.playableTitle(ctx, pr.User, t) {
					return nil, core.ErrNotFound
				}
			}
			sub, err := s.expandKey(ctx, pr, k)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			if len(out) >= maxExpand {
				break
			}
		}
		return capKeys(out), nil
	case reCollectionPath.MatchString(path):
		m := reCollectionPath.FindStringSubmatch(path)
		k, err := core.ParseRatingKey(m[1])
		if err != nil {
			return nil, err
		}
		keys, err := s.expandKey(ctx, pr, k)
		return capKeys(keys), err
	case rePlaylistPath.MatchString(path):
		m := rePlaylistPath.FindStringSubmatch(path)
		k, err := core.ParseRatingKey(m[1])
		if err != nil {
			return nil, err
		}
		return s.expandKey(ctx, pr, k)
	case reSectionPath.MatchString(path):
		m := reSectionPath.FindStringSubmatch(path)
		id, _ := strconv.Atoi(m[1])
		sec, ok := s.sectionByID(id)
		if !ok || !s.canSeeSection(pr.User, id) {
			return nil, core.ErrNotFound
		}
		q, _ := url.ParseQuery(rawQuery)
		br := s.parseBrowse(ctx, sec, sec.Type, pr.User, q, rawQuery)
		br.q.Offset, br.q.Limit = 0, maxExpand
		page, err := s.d.Catalog.Browse(ctx, br.q)
		if err != nil {
			return nil, err
		}
		var out []core.RatingKey
		for _, t := range page.Items {
			sub, err := s.expandKey(ctx, pr, t.Key)
			if err == nil {
				out = append(out, sub...)
			}
			if len(out) >= maxExpand {
				break
			}
		}
		return capKeys(out), nil
	}
	return nil, fmt.Errorf("unsupported uri path %q", path)
}

func capKeys(ks []core.RatingKey) []core.RatingKey {
	if len(ks) > maxExpand {
		return ks[:maxExpand]
	}
	return ks
}

// ---------------------------------------------------------------------------
// Playlists

func (s *Server) playlist(ctx context.Context, u *core.User, k core.RatingKey) (*core.Playlist, error) {
	return (experience.PlaylistSelection{Playlists: s.d.Playlists}).Get(ctx, u, k)
}

func (s *Server) playlistMetadata(ctx context.Context, pl *core.Playlist, omitState ...bool) model.Metadata {
	rk := pl.Key.String()
	count := len(pl.Items)
	if pl.SmartURI != "" {
		pr := *FromContext(ctx)
		pr.HasPage, pr.Start, pr.Size = true, 0, 0
		if _, total, _, err := s.smartPlaylistBrowse(ctx, &pr, pl.SmartURI); err == nil {
			count = total
		}
	}
	stamp := unixOrZero(pl.UpdatedAt)
	if stamp == 0 {
		stamp = 1
	}
	m := model.Metadata{
		RatingKey:    rk,
		Key:          "/playlists/" + rk + "/items",
		GUID:         "com.plexapp.agents.none://playlist-" + rk,
		Type:         "playlist",
		Title:        pl.Title,
		Summary:      "",
		Smart:        model.Ptr(pl.Smart),
		PlaylistType: "video",
		Composite:    "/playlists/" + rk + "/composite/" + strconv.FormatInt(stamp, 10),
		LeafCount:    model.Ptr(count),
		AddedAt:      unixOrZero(pl.UpdatedAt),
		UpdatedAt:    unixOrZero(pl.UpdatedAt),
	}
	if pl.Smart {
		m.Icon = "playlist://image.smart"
	}
	m.Duration = s.playlistDuration(pl.Items)
	if len(omitState) == 0 || !omitState[0] {
		s.applyListWatch(ctx, &m, pl.Key)
	}
	return m
}

// playlistDuration is the total runtime PMS reports on a playlist, from
// the title-detail cache without blocking: it is omitted (0) until every
// item's runtime is known, and unknown items are fetched in the background.
func (s *Server) playlistDuration(items []core.RatingKey) int64 {
	if s.titles == nil || len(items) > 200 {
		return 0
	}
	var seconds int64
	known := true
	for _, key := range items {
		title := s.titles.get(key)
		if title == nil || title.Runtime <= 0 {
			known = false
			continue
		}
		// PMS truncates each member before summing; truncating the total
		// instead incorrectly carries fractional seconds between members.
		seconds += title.Runtime.Milliseconds() / 1000
	}
	if !known {
		return 0
	}
	return seconds * 1000
}

func (s *Server) handlePlaylists(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pr := FromContext(ctx)
	q := r.URL.Query()
	mc := &model.MediaContainer{}
	if pt := q.Get("playlistType"); pt != "" && !strings.Contains(pt, "video") {
		s.write(w, r, mc)
		return
	}
	if s.d.Playlists != nil && pr.User != nil {
		pls, err := s.d.Playlists.List(ctx, pr.User.ID)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		smart := q.Get("smart")
		lo, hi := pr.window(len(pls))
		for i := lo; i < hi; i++ {
			if smart == "1" && !pls[i].Smart || smart == "0" && pls[i].Smart {
				continue
			}
			mc.Metadata = append(mc.Metadata, s.playlistMetadata(ctx, &pls[i]))
		}
		if pr.HasPage {
			mc.SetPage(lo, len(pls))
		}
	}
	mc.Size = len(mc.Metadata)
	s.write(w, r, mc)
}

func (s *Server) requirePlaylists(w http.ResponseWriter, r *http.Request) bool {
	if s.d.Playlists == nil || FromContext(r.Context()).User == nil {
		writeHTTPError(w, http.StatusNotFound)
		return false
	}
	return true
}

func (s *Server) handlePlaylistCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requirePlaylists(w, r) {
		return
	}
	ctx := r.Context()
	pr := FromContext(ctx)
	q := r.URL.Query()
	if t := q.Get("type"); t != "" && t != "video" {
		writeHTTPError(w, http.StatusBadRequest)
		return
	}
	title := q.Get("title")
	if title == "" {
		title = "Playlist"
	}
	if q.Get("smart") == "1" {
		uri, err := s.smartSectionURI(pr.User, q.Get("uri"))
		store, ok := s.d.Playlists.(core.SmartPlaylists)
		if err != nil || !ok {
			writeHTTPError(w, http.StatusBadRequest)
			return
		}
		pl, err := store.CreateSmartPlaylist(ctx, pr.User.ID, title, uri)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		s.notifyPlaylistLifecycle(pl, true)
		s.write(w, r, &model.MediaContainer{Size: 1, Metadata: []model.Metadata{s.playlistMetadata(ctx, pl, true)}})
		return
	}
	var keys []core.RatingKey
	if uri := q.Get("uri"); uri != "" {
		ks, err := s.resolveURI(ctx, pr, uri)
		if err != nil {
			s.log.Debug("playlist uri", "uri", uri, "err", err)
			writeHTTPError(w, http.StatusBadRequest)
			return
		}
		keys = ks
	} else if pqID, err := strconv.ParseInt(q.Get("playQueueID"), 10, 64); err == nil {
		if pq, ok := s.queues.get(pqID, pr.User.ID); ok {
			for _, it := range pq.items {
				keys = append(keys, it.key)
			}
		}
	}
	pl, err := s.d.Playlists.Create(ctx, pr.User.ID, title, keys)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.notifyPlaylistLifecycle(pl, true)
	mc := &model.MediaContainer{Size: 1, Metadata: []model.Metadata{s.playlistMetadata(ctx, pl, true)}}
	s.write(w, r, mc)
}

func (s *Server) playlistFromParam(w http.ResponseWriter, r *http.Request) (*core.Playlist, bool) {
	if !s.requirePlaylists(w, r) {
		return nil, false
	}
	k, err := keyParam(r)
	if err != nil || k.Kind() != core.KindPlaylist {
		writeHTTPError(w, http.StatusNotFound)
		return nil, false
	}
	pl, err := s.playlist(r.Context(), FromContext(r.Context()).User, k)
	if err != nil {
		s.writeErr(w, r, err)
		return nil, false
	}
	return pl, true
}

func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.playlistFromParam(w, r)
	if !ok {
		return
	}
	s.write(w, r, &model.MediaContainer{Size: 1, Metadata: []model.Metadata{s.playlistMetadata(r.Context(), pl, true)}})
}

// handlePlaylistEdit accepts PUT /playlists/{key}; renames are not part of
// core.Playlists, so only the current state is returned.
func (s *Server) handlePlaylistEdit(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.playlistFromParam(w, r)
	if !ok {
		return
	}
	if t := r.URL.Query().Get("title"); t != "" && t != pl.Title {
		s.log.Info("playlist rename not supported", "playlist", pl.Key)
	}
	s.write(w, r, &model.MediaContainer{Size: 1, Metadata: []model.Metadata{s.playlistMetadata(r.Context(), pl)}})
}

func (s *Server) handlePlaylistDelete(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.playlistFromParam(w, r)
	if !ok {
		return
	}
	if err := s.d.Playlists.Delete(r.Context(), FromContext(r.Context()).User.ID, pl.Key); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.notifyPlaylistLifecycle(pl, false)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePlaylistItems(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.playlistFromParam(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	pr := FromContext(ctx)
	if pl.SmartURI != "" {
		titles, total, offset, err := s.smartPlaylistBrowse(ctx, pr, pl.SmartURI)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		mc := &model.MediaContainer{RatingKey: pl.Key.String(), Key: "/playlists/" + pl.Key.String() + "/items", Title1: pl.Title, PlaylistType: "video", Smart: model.Ptr(true), LeafCount: model.Ptr(total), Composite: "/playlists/" + pl.Key.String() + "/composite/1"}
		storedOnly := len(titles) > 0 && titles[0].Type == core.TypeEpisode
		sqlAvailability, _ := s.d.Catalog.(interface{ BrowseAvailabilityInSQL() bool })
		mc.Metadata = s.renderTitles(ctx, pr, titles, renderOpts{media: true, storedOnly: storedOnly, availabilityInSQL: sqlAvailability != nil && sqlAvailability.BrowseAvailabilityInSQL()})
		for i := range mc.Metadata {
			mc.Metadata[i].PlaylistItemID = int64(offset + i + 1)
		}
		mc.Size = len(mc.Metadata)
		if pr.HasPage {
			mc.SetPage(offset, total)
		}
		s.write(w, r, mc)
		return
	}
	lo, hi := pr.window(len(pl.Items))
	window := pl.Items[lo:hi]
	titles := s.visibleTitles(pr.User, s.getTitles(ctx, window))
	byKey := make(map[core.RatingKey]*core.Title, len(titles))
	for i := range titles {
		byKey[titles[i].Key] = &titles[i]
	}
	state := s.stateFor(ctx, pr, titles)
	mc := &model.MediaContainer{
		RatingKey:    pl.Key.String(),
		Key:          "/playlists/" + pl.Key.String() + "/items",
		Title1:       pl.Title,
		PlaylistType: "video",
		Smart:        model.Ptr(pl.Smart),
		LeafCount:    model.Ptr(len(pl.Items)),
		Composite:    "/playlists/" + pl.Key.String() + "/composite/1",
	}
	var dur int64
	for i, k := range window {
		t, ok := byKey[k]
		if !ok {
			continue
		}
		md := s.metadata(ctx, pr, t, renderOpts{media: true, state: state})
		md.PlaylistItemID = int64(lo + i + 1)
		dur += md.Duration
		mc.Metadata = append(mc.Metadata, md)
	}
	mc.Duration = dur
	mc.Size = len(mc.Metadata)
	if pr.HasPage {
		mc.SetPage(lo, len(pl.Items))
	}
	s.write(w, r, mc)
}

func (s *Server) handlePlaylistAdd(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.playlistFromParam(w, r)
	if !ok {
		return
	}
	if pl.SmartURI != "" {
		writeHTTPError(w, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	pr := FromContext(ctx)
	uri := r.URL.Query().Get("uri")
	keys, err := s.resolveURI(ctx, pr, uri)
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest)
		return
	}
	if err := s.d.Playlists.Add(ctx, pr.User.ID, pl.Key, keys); err != nil {
		s.writeErr(w, r, err)
		return
	}
	if updated, err := s.playlist(ctx, pr.User, pl.Key); err == nil {
		pl = updated
	}
	md := s.playlistMetadata(ctx, pl)
	s.write(w, r, &model.MediaContainer{Size: 1, Extra: []model.Attr{model.A("leafCountAdded", len(keys)), model.A("leafCountRequested", len(keys))}, Metadata: []model.Metadata{md}})
}

func itemIndexParam(r *http.Request, n int) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "item"))
	if err != nil || id < 1 || id > n {
		return 0, false
	}
	return id - 1, true
}

func (s *Server) handlePlaylistRemove(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.playlistFromParam(w, r)
	if !ok {
		return
	}
	if pl.SmartURI != "" {
		writeHTTPError(w, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	uid := FromContext(ctx).User.ID
	idx, ok := itemIndexParam(r, len(pl.Items))
	if !ok {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	if err := s.d.Playlists.Remove(ctx, uid, pl.Key, idx); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.handlePlaylistItemsAfterChange(w, r, pl.Key)
}

func (s *Server) handlePlaylistClear(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.playlistFromParam(w, r)
	if !ok {
		return
	}
	if pl.SmartURI != "" {
		writeHTTPError(w, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	uid := FromContext(ctx).User.ID
	for i := len(pl.Items) - 1; i >= 0; i-- {
		if err := s.d.Playlists.Remove(ctx, uid, pl.Key, i); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	s.handlePlaylistItemsAfterChange(w, r, pl.Key)
}

func (s *Server) handlePlaylistMove(w http.ResponseWriter, r *http.Request) {
	pl, ok := s.playlistFromParam(w, r)
	if !ok {
		return
	}
	if pl.SmartURI != "" {
		writeHTTPError(w, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	uid := FromContext(ctx).User.ID
	from, ok := itemIndexParam(r, len(pl.Items))
	if !ok {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	to := 0
	if after, err := strconv.Atoi(r.URL.Query().Get("after")); err == nil && after >= 1 && after <= len(pl.Items) {
		a := after - 1
		// Moving an item after itself leaves it where it is.
		if from <= a {
			to = a
		} else {
			to = a + 1
		}
	}
	if from != to {
		if err := s.d.Playlists.Move(ctx, uid, pl.Key, from, to); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	s.handlePlaylistItemsAfterChange(w, r, pl.Key)
}

// handlePlaylistItemsAfterChange answers a mutation with the updated items,
// as PMS does.
func (s *Server) handlePlaylistItemsAfterChange(w http.ResponseWriter, r *http.Request, k core.RatingKey) {
	_ = k // the route's {key} is the same playlist
	s.handlePlaylistItems(w, r)
}
