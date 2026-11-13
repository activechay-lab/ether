package app

import "github.com/braedonsaunders/ether/internal/core"

func builtinNative(mediaType core.MediaType, protocol core.Protocol) string {
	switch protocol {
	case "plex":
		if mediaType == core.TypeShow {
			return "show"
		}
		return "movie"
	case "jellyfin", "emby", "silo":
		if mediaType == core.TypeShow {
			return "tvshows"
		}
		return "movies"
	case "stremio", "xtream":
		if mediaType == core.TypeShow {
			return "series"
		}
		return "movie"
	default:
		if mediaType == core.TypeShow {
			return "show"
		}
		return "movie"
	}
}

// presentLibraryForProtocol evaluates whether p publishes a library on protocol, and
// returns its projected root MediaType and protocol-native presentation string.
func presentLibraryForProtocol(p core.Presentations, protocol core.Protocol, fallback core.MediaType) (core.MediaType, string, bool) {
	if p.AdapterOnly {
		return "", "", false
	}
	if fallback == "" {
		fallback = core.TypeMovie
	}
	switch protocol {
	case "plex":
		if p.Plex == nil || p.Plex.Base == "" {
			return "", "", false
		}
		mt := fallback
		switch p.Plex.Base {
		case "movie":
			mt = core.TypeMovie
		case "show":
			mt = core.TypeShow
		}
		return mt, p.Plex.Base, true
	case "jellyfin", "emby":
		if p.MediaBrowser == nil || p.MediaBrowser.CollectionType == "" {
			return "", "", false
		}
		mt := fallback
		switch p.MediaBrowser.CollectionType {
		case "movies":
			mt = core.TypeMovie
		case "tvshows":
			mt = core.TypeShow
		}
		return mt, p.MediaBrowser.CollectionType, true
	case "silo":
		if p.Silo != nil && p.Silo.Type != "" {
			mt := fallback
			switch p.Silo.Type {
			case "movies", "movie":
				mt = core.TypeMovie
			case "tvshows", "series", "show":
				mt = core.TypeShow
			}
			return mt, p.Silo.Type, true
		}
		return "", "", false
	case "stremio":
		if p.Stremio == nil || p.Stremio.Type == "" {
			return "", "", false
		}
		mt := core.TypeMovie
		if p.Stremio.Type == "series" {
			mt = core.TypeShow
		}
		return mt, p.Stremio.Type, true
	case "xtream":
		if p.Xtream == nil || p.Xtream.Category == "" {
			return "", "", false
		}
		mt := core.TypeMovie
		if p.Xtream.Category == "series" {
			mt = core.TypeShow
		}
		return mt, p.Xtream.Category, true
	case "dlna", "webdav", "m3u":
		if p.MediaBrowser != nil && p.MediaBrowser.CollectionType != "" {
			switch p.MediaBrowser.CollectionType {
			case "movies":
				return core.TypeMovie, "movies", true
			case "tvshows":
				return core.TypeShow, "tvshows", true
			}
		}
		if p.Plex != nil && p.Plex.Base != "" {
			switch p.Plex.Base {
			case "movie":
				return core.TypeMovie, "movie", true
			case "show":
				return core.TypeShow, "show", true
			}
		}
		return "", "", false
	default:
		return "", "", false
	}
}
