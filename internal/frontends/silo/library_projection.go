package silo

import "github.com/braedonsaunders/ether/internal/core"

// NativeLibraryType returns this adapter's built-in library category.
func NativeLibraryType(mt core.MediaType) string {
	if mt == core.TypeShow {
		return "tvshows"
	}
	return "movies"
}

// ProjectLibrary interprets this adapter's plugin presentation declaration.
func ProjectLibrary(p core.Presentations, fallback core.MediaType) (core.MediaType, string, bool) {
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

}
