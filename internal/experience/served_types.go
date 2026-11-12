package experience

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/reqmemo"
)

// servedItemKinds names every Jellyfin/Emby item kind the viewer's accessible,
// published libraries can present, lowercased (movie, series, season, episode,
// musicalbum, audio, ...). A request whose IncludeItemTypes names only kinds
// outside this set can never match a title, so it answers empty instead of
// dropping the type filter and listing every library's items (#405: Moonfin's
// Favorites screen asks Person, MusicVideo, MusicArtist, MusicAlbum and Audio
// on a movies+shows server and the catalogue answered by scanning the TV
// library). Built-in libraries serve the four catalogue kinds; plugin
// libraries serve the kinds their MediaBrowser presentation declares.
func (s *Service) servedItemKinds(ctx context.Context, v *core.User) map[string]bool {
	out := map[string]bool{}
	builtin := func(mt core.MediaType) {
		switch mt {
		case core.TypeMovie:
			out["movie"] = true
		case core.TypeShow:
			// q.Types names episodes/shows by core.MediaType ("show"), while a
			// client's IncludeItemTypes names a series ("series").
			out["show"], out["series"], out["season"], out["episode"] = true, true, true, true
		}
	}
	if s.d.PluginLibraries == nil {
		for _, lib := range s.libraries() {
			if v.AllowsLibrary(core.LibraryID(lib.ID)) {
				builtin(lib.Type)
			}
		}
		return out
	}
	libs, err := reqmemo.Do(ctx, "experience:pluginlibs", func() ([]core.Library, error) {
		return s.d.PluginLibraries.Libraries(ctx)
	})
	if err != nil {
		for _, lib := range s.libraries() {
			if v.AllowsLibrary(core.LibraryID(lib.ID)) {
				builtin(lib.Type)
			}
		}
		return out
	}
	for _, lib := range libs {
		if !v.AllowsLibrary(lib.ID) {
			continue
		}
		mt, _, ok := s.publishedLibrary(ctx, lib)
		if !ok {
			continue
		}
		builtin(mt)
		if lib.Present.MediaBrowser != nil {
			for _, kind := range lib.Present.MediaBrowser.ItemTypes {
				name := strings.ToLower(kind)
				out[name] = true
				// Aliases so a request typed by core.MediaType matches too.
				switch name {
				case "movie":
					out["movie"] = true
				case "series":
					out["show"], out["series"] = true, true
				}
			}
		}
	}
	return out
}

// requestedTypesServable reports whether a request that named item kinds can be
// served at all: at least one requested kind (mapped to a core type or not)
// appears in the viewer's served set. It is false only when the request named
// kinds and none can match, which must answer empty. A request that named no
// kinds (both lists empty) is unrestricted and stays servable.
func (s *Service) requestedTypesServable(ctx context.Context, v *core.User, q frontend.ItemQuery) bool {
	if len(q.Types) == 0 && len(q.UnservedTypes) == 0 {
		return true
	}
	served := s.servedItemKinds(ctx, v)
	for _, mt := range q.Types {
		if served[string(mt)] {
			return true
		}
	}
	for _, kind := range q.UnservedTypes {
		if served[kind] {
			return true
		}
	}
	return false
}
