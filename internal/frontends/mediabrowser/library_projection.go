package mediabrowser

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

}
