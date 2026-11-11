package plex

import (
	"context"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Plex section bases beyond the built-in movie/show (spec §6.4). The value
// is the Plex section `type` string a client reads from /library/sections.
const (
	plexBaseMovie  = "movie"
	plexBaseShow   = "show"
	plexBaseArtist = "artist"
	plexBaseAlbum  = "album"
	plexBaseTrack  = "track"
	plexBasePhoto  = "photo"
)

// Plex metadata type numbers for the non-video bases (spec §6.4): the music
// hierarchy is artist -> album -> track, photo is a flat photo album.
const (
	typeNumArtist = 8
	typeNumAlbum  = 9
	typeNumTrack  = 10
	typeNumPhoto  = 13
)

// ownerViewer is the synthetic viewer used to enumerate every published
// library, independent of any client. An owner sees all libraries.
func ownerViewer() *core.Viewer { return &core.User{IsOwner: true} }

// publishedKindSections returns one Section per Plex-published plugin or
// filter library, from the Experience's view list. It is the only place a
// plugin library becomes a Plex section (spec §6.4). Built-in libraries are
// not returned here; buildSections keeps their existing identities.
func (s *Server) publishedKindSections(ctx context.Context) []Section {
	exp := s.exp()
	if exp == nil {
		return nil
	}
	views, err := exp.Views(ctx, ownerViewer())
	if err != nil {
		return nil
	}
	out := make([]Section, 0, len(views))
	for _, v := range views {
		base, ok := plexBaseForView(v)
		if !ok {
			continue
		}
		id, err := strconv.Atoi(v.ID)
		if err != nil || id <= 0 {
			continue
		}
		out = append(out, Section{
			ID:        id,
			Type:      frontend.MediaType(base),
			Title:     v.Name,
			LibraryID: core.LibraryID(id),
			PluginID:  v.PluginID,
			KindKey:   v.KindKey,
		})
	}
	return out
}

// plexBaseForView maps a published view to its Plex section base, or reports
// false when the library does not publish to Plex. The base is taken from the
// protocol-native presentation the Experience already resolved (View.Native),
// never guessed from the name.
func plexBaseForView(v frontend.View) (string, bool) {
	switch strings.TrimSpace(v.Native) {
	case plexBaseMovie:
		return plexBaseMovie, true
	case plexBaseShow:
		return plexBaseShow, true
	case plexBaseArtist:
		return plexBaseArtist, true
	case plexBasePhoto:
		return plexBasePhoto, true
	}
	return "", false
}

// mergeKindSections appends the published plugin/filter sections to base,
// skipping any id already present so a built-in identity is never replaced.
func mergeKindSections(base []Section, extra []Section) []Section {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[int]bool, len(base))
	for _, sec := range base {
		seen[sec.ID] = true
	}
	out := base
	for _, sec := range extra {
		if seen[sec.ID] {
			continue
		}
		seen[sec.ID] = true
		out = append(out, sec)
	}
	return out
}

// sectionForLibrary returns the section for a library id (spec §6.4).
func (s *Server) sectionForLibrary(id core.LibraryID) (Section, bool) {
	for _, sec := range s.withLibraryTitles(s.getSections()) {
		if sec.LibraryID == id || core.LibraryID(sec.ID) == id {
			return sec, true
		}
	}
	return Section{}, false
}

// sectionForItem returns the section an item lives in (spec §6.4). inSection
// is the section the current listing is scoped to; 0 means "no section
// context", where the item's canonical library is used.
func (s *Server) sectionForItem(t *frontend.Title, inSection int) (Section, bool) {
	if inSection != 0 {
		if sec, ok := s.sectionByID(inSection); ok {
			return sec, true
		}
	}
	if t == nil {
		return Section{}, false
	}
	if t.Key.IsPluginKey() {
		// A plugin item has no catalogue section; without listing context its
		// canonical section is its own plugin library, which the caller must
		// supply via inSection. Callers that cannot answer return none rather
		// than guess a built-in section from the projected type.
		return Section{}, false
	}
	return s.sectionFor(t.Type)
}

// isKindSection reports whether sec is a plugin or filter library section,
// i.e. one whose items come from the Experience's plugin store rather than
// the TMDB catalogue.
func isKindSection(sec Section) bool {
	return sec.LibraryID != core.LibraryMovies && sec.LibraryID != core.LibraryShows && sec.LibraryID != 0
}

// kindBaseForSection is the Plex base of a section's library (movie, show,
// artist, photo).
func kindBaseForSection(sec Section) string {
	return string(sec.Type)
}
