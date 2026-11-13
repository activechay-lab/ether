package mediabrowser

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
	if p.MediaBrowser == nil || p.MediaBrowser.CollectionType == "" {
		return "", "", false
	}
	mt := fallback
	switch p.MediaBrowser.CollectionType {
	case "movies":
		mt = frontend.TypeMovie
	case "tvshows":
		mt = frontend.TypeShow
	}
	return mt, p.MediaBrowser.CollectionType, true

}
