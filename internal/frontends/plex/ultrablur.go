package plex

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

const (
	ultraBlurWorkers  = 2
	ultraBlurQueueLen = 512
)

func wirePalette(p frontend.ArtworkPalette) model.UltraBlurColors {
	return model.UltraBlurColors{TopLeft: p.TopLeft, TopRight: p.TopRight, BottomLeft: p.BottomLeft, BottomRight: p.BottomRight}
}

func (s *Server) ultraBlurFor(key frontend.RatingKey, backdrop string) *model.UltraBlurColors {
	if s.svc().ultra == nil || backdrop == "" {
		return nil
	}
	p := s.svc().ultra.Lookup(key, backdrop)
	if p == nil {
		return nil
	}
	result := wirePalette(*p)
	return &result
}
func (s *Server) preloadUltraBlur(ctx context.Context, titles []frontend.Title) {
	if s.svc().ultra == nil {
		return
	}
	wants := make([]frontend.ArtworkPaletteWant, 0, len(titles))
	for _, t := range titles {
		if ultraBlurType(t.Type) {
			wants = append(wants, frontend.ArtworkPaletteWant{Key: t.Key, Path: t.BackdropPath})
		}
	}
	s.svc().ultra.Preload(ctx, wants)
}
func ultraBlurType(t frontend.MediaType) bool {
	return t == frontend.TypeMovie || t == frontend.TypeShow
}

// handleUltraBlurColors answers GET /service(s)/ultrablur/colors?url=...
// (Plex for Apple TV asks it for episode stills, which never get inline
// colours). The url names a same-server image; the palette cache answers
// cached colours immediately and schedules the compute otherwise, so the
// request never waits on an image download.
func (s *Server) handleUltraBlurColors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pr := FromContext(ctx)
	key, element, ok := ultraBlurImageTarget(r.URL.Query().Get("url"))
	if !ok {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	t, err := s.metadataTitle(ctx, pr, key)
	if err == nil && !key.IsPluginKey() && !s.visibleTitle(ctx, pr.User, t) {
		err = frontend.ErrNotFound
	}
	if err != nil {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	mc := &model.MediaContainer{Size: 1}
	if path := ultraBlurImagePath(t, element); path != "" {
		mc.UltraBlurColors = s.ultraBlurFor(key, path)
	}
	s.write(w, r, mc)
}

// ultraBlurImageTarget parses a same-server image url
// (/library/metadata/<key>/<element>[/<ts>]) into its title key and element.
// Anything else — another server, another route, an unknown element — is
// rejected so the endpoint only ever colours Ether's own artwork.
func ultraBlurImageTarget(raw string) (frontend.RatingKey, string, bool) {
	raw = strings.TrimSpace(raw)
	if u, err := url.Parse(raw); err == nil && u.IsAbs() {
		return 0, "", false
	}
	rest, ok := strings.CutPrefix(raw, "/library/metadata/")
	if !ok {
		return 0, "", false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 {
		return 0, "", false
	}
	switch parts[1] {
	case "thumb", "poster", "composite", "squareArt", "art", "background":
	default:
		return 0, "", false
	}
	key, err := frontend.ParseRatingKey(parts[0])
	if err != nil {
		return 0, "", false
	}
	return key, parts[1], true
}

// ultraBlurImagePath is the artwork path the url's element names: an episode
// still, a poster, or a backdrop.
func ultraBlurImagePath(t *frontend.Title, element string) string {
	if t == nil {
		return ""
	}
	switch element {
	case "art", "background":
		return t.BackdropPath
	}
	if t.Type == frontend.TypeEpisode {
		return t.StillPath
	}
	return t.PosterPath
}
