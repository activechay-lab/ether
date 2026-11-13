package plex

import (
	"context"
	"net/http"
	"strconv"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// sectionKindList serves /library/sections/{id}/{sub} for a plugin or filter
// library from the Experience's plugin store (spec §6.4). Only the item
// listings Plex music/photo clients use are answered; the TMDB-shaped facet
// and collection subtrees are not meaningful for a plugin library and return
// an empty page rather than a catalogue query.
func (s *Server) sectionKindList(w http.ResponseWriter, r *http.Request, sec Section, sub string) {
	ctx := r.Context()
	pr := FromContext(ctx)
	q := r.URL.Query()
	_, allTitle := sectionTypeTitle(sec)
	mc := s.sectionContainer(sec)
	mc.Title2 = allTitle
	mc.ViewGroup = kindViewGroup(sec)
	mc.ViewMode = viewModeList

	switch sub {
	case "all", "allLeaves", "unwatched", "newest", "recentlyAdded", "recentlyViewedShows":
	default:
		// Facets, collections, prefs and search stay with their existing
		// handlers; only the browse listings dispatch here.
		mc.SetPage(0, 0)
		s.write(w, r, mc)
		return
	}

	offset, size := pr.pageOf(0)
	query := frontend.ItemQuery{Offset: offset, Limit: size}
	page, err := s.exp().LibraryItems(ctx, pr.User, plexDevice(pr), sec.IDString(), query)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	items := page.Items
	mc.Metadata = s.renderTitles(ctx, pr, items, renderOpts{media: true, inSection: true, storedOnly: true})
	for i := range mc.Metadata {
		var title *frontend.Title
		if i < len(items) {
			title = &items[i]
		}
		mc.Metadata[i].Type = kindTypeForBase(string(sec.Type), mc.Metadata[i].Type, title)
	}
	mc.Size = len(mc.Metadata)
	mc.SetPage(offset, page.Total)
	if q.Get("includeMeta") == "1" {
		mc.Meta = s.meta(sec, sec.Type, q.Get("sort"))
	}
	s.write(w, r, mc)
}

// IDString is the library id as the Experience's View.ID string.
func (sec Section) IDString() string { return strconv.Itoa(sec.ID) }

// kindViewGroup is the Plex viewGroup for a kind section's root listing.
func kindViewGroup(sec Section) string {
	switch string(sec.Type) {
	case plexBaseArtist:
		return "artist"
	case plexBasePhoto:
		return "photo"
	default:
		return string(sec.Type)
	}
}

// plexDevice builds the neutral device identity for an Experience call.
func plexDevice(pr *Request) frontend.Device {
	if pr == nil {
		return frontend.Device{}
	}
	return frontend.Device{ID: pr.ClientID, Client: pr.Product, Product: pr.Product, Version: pr.Version, Platform: pr.Platform, DeviceName: pr.Device, Language: pr.Language}
}

// kindItem resolves one plugin item through the Experience, which authorizes
// the viewer against the item's owning library (spec §6.4).
func (s *Server) kindItem(ctx context.Context, pr *Request, k frontend.RatingKey) (*frontend.Title, error) {
	detail, err := s.exp().Item(ctx, frontend.ItemRequest{Viewer: pr.User, Device: plexDevice(pr), Key: k})
	if err != nil {
		return nil, err
	}
	t := detail.Title
	return &t, nil
}

// kindChildren lists one plugin item's children (artist -> album -> track,
// album -> photos) through the Experience.
func (s *Server) kindChildren(ctx context.Context, pr *Request, k frontend.RatingKey) ([]frontend.Title, error) {
	page, err := s.exp().Children(ctx, pr.User, plexDevice(pr), k, frontend.ItemQuery{})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// plexTypeFor is the Plex wire type of a title: the protocol-native base for a
// plugin item in a music/photo section, the projected type otherwise. It is
// the single mapping used by detail, children and listings.
func (s *Server) plexTypeFor(t *frontend.Title) string {
	if t == nil {
		return ""
	}
	if !t.Key.IsPluginKey() {
		return string(t.Type)
	}
	if base, ok := s.kindSectionBaseForTitle(t); ok {
		return kindTypeForBase(base, string(t.Type), t)
	}
	return string(t.Type)
}

// kindSectionBaseForTitle is the Plex base (movie/show/artist/photo) of the
// library a projected plugin title belongs to, from its LibraryID. The second
// value is false when the title is not a plugin item or its library is not
// Plex-published.
func (s *Server) kindSectionBaseForTitle(t *frontend.Title) (string, bool) {
	if t == nil || !t.Key.IsPluginKey() {
		return "", false
	}
	if t.LibraryID != 0 {
		if sec, ok := s.sectionForLibrary(t.LibraryID); ok {
			if _, isKind := plexBaseForView(frontend.View{Native: string(sec.Type)}); isKind {
				return string(sec.Type), true
			}
		}
	}
	return "", false
}

// kindTypeForBase maps a projected plugin item's Plex type to the native type
// its section base implies (spec §6.4). The projector renders plugin items in
// the video hierarchy; the base recovers the protocol-native music/photo type.
// A KindLevel of artist/album/track wins when present, so a music library whose
// levels are named exactly that is typed from the declaration. The section
// listing, child and detail renderers share this one mapping.
func kindTypeForBase(base, projected string, t *frontend.Title) string {
	switch base {
	case plexBaseArtist:
		if t != nil && t.KindLevel != "" {
			switch t.KindLevel {
			case "artist", "album", "track":
				return t.KindLevel
			}
		}
		switch projected {
		case string(frontend.TypeShow):
			return plexBaseArtist
		case string(frontend.TypeSeason):
			return plexBaseAlbum
		case string(frontend.TypeEpisode):
			return plexBaseTrack
		}
		return plexBaseArtist
	case plexBasePhoto:
		return plexBasePhoto
	default:
		return projected
	}
}

// writeKindChildren renders one plugin item's children (artist -> album ->
// track, album -> photos) with the protocol-native Plex types its section base
// implies. It bypasses the catalogue/episode machinery handleChildren runs for
// TMDB titles, which does not know plugin keys.
func (s *Server) writeKindChildren(w http.ResponseWriter, r *http.Request, pr *Request, parent *frontend.Title, kids []frontend.Title) {
	ctx := r.Context()
	mc := s.itemContainer(parent.Type)
	mc.Key = parent.Key.String()
	mc.Nocache = model.Ptr(true)
	mc.Thumb = s.thumbURL(parent)
	mc.Art = s.artURL(parent)
	base, _ := s.kindSectionBaseForTitle(parent)
	mc.ViewGroup = kindViewGroupForBase(base, parent.Type)
	mc.ViewMode = viewModeList
	lo, hi := pr.window(len(kids))
	page := kids[lo:hi]
	rendered := s.renderTitles(ctx, pr, page, renderOpts{media: true, inSection: true, storedOnly: true})
	for i := range rendered {
		rendered[i].Type = kindTypeForBase(base, rendered[i].Type, &page[i])
	}
	mc.Metadata = rendered
	mc.Size = len(mc.Metadata)
	if pr.HasPage {
		mc.SetPage(lo, len(kids))
	}
	s.write(w, r, mc)
}

// kindViewGroupForBase is the Plex viewGroup for a plugin section's child
// listing, keyed by the section base.
func kindViewGroupForBase(base string, parentType frontend.MediaType) string {
	switch base {
	case plexBaseArtist:
		if parentType == frontend.TypeShow {
			return "album"
		}
		return "track"
	case plexBasePhoto:
		return "photo"
	}
	switch parentType {
	case frontend.TypeShow:
		return "season"
	case frontend.TypeSeason:
		return "episode"
	}
	return string(parentType)
}
