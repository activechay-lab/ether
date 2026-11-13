package plex

import "github.com/braedonsaunders/ether/internal/frontend"

// NativeLibraryType returns this adapter's built-in library category.
func NativeLibraryType(mt frontend.MediaType) string {
	if mt == frontend.TypeShow {
		return "show"
	}
	return "movie"
}

// ProjectLibrary interprets this adapter's plugin presentation declaration.
func ProjectLibrary(p frontend.Presentations, fallback frontend.MediaType) (frontend.MediaType, string, bool) {
	if p.Plex == nil || p.Plex.Base == "" {
		return "", "", false
	}
	mt := fallback
	switch p.Plex.Base {
	case "movie":
		mt = frontend.TypeMovie
	case "show":
		mt = frontend.TypeShow
	}
	return mt, p.Plex.Base, true

}
