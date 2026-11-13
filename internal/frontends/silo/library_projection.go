package silo

import "github.com/braedonsaunders/ether/internal/frontend"

// NativeLibraryType returns this adapter's built-in library category.
func NativeLibraryType(mt frontend.MediaType) string {
	if mt == frontend.TypeShow {
		return "tvshows"
	}
	return "movies"
}

// ProjectLibrary interprets this adapter's plugin presentation declaration.
func ProjectLibrary(p frontend.Presentations, fallback frontend.MediaType) (frontend.MediaType, string, bool) {
	if p.Silo != nil && p.Silo.Type != "" {
		mt := fallback
		switch p.Silo.Type {
		case "movies", "movie":
			mt = frontend.TypeMovie
		case "tvshows", "series", "show":
			mt = frontend.TypeShow
		}
		return mt, p.Silo.Type, true
	}
	return "", "", false

}
