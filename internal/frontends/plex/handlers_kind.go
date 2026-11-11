package plex

import (
	"context"
	"net/http"
	"strconv"

	"github.com/braedonsaunders/ether/internal/frontend"
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
		mc.Metadata[i].Type = kindItemType(sec, mc.Metadata[i].Type)
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

// kindItemType maps a projected item's Plex type to the native type the
// section's base implies. The projector renders music and photo items in the
// video hierarchy (show/season/episode); the section base recovers the
// protocol-native type Plex music/photo clients expect.
func kindItemType(sec Section, projected string) string {
	switch string(sec.Type) {
	case plexBaseArtist:
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
