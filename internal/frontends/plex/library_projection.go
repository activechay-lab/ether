package plex

import "github.com/braedonsaunders/ether/internal/core"

// NativeLibraryType returns this adapter's built-in library category.
func NativeLibraryType(mt core.MediaType) string {
	if mt == core.TypeShow {
		return "show"
	}
	return "movie"
}

// ProjectLibrary interprets this adapter's plugin presentation declaration.
func ProjectLibrary(p core.Presentations, fallback core.MediaType) (core.MediaType, string, bool) {
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

}
