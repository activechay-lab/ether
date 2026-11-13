package xtream

import "github.com/braedonsaunders/ether/internal/frontend"

// NativeLibraryType returns this adapter's built-in library category.
func NativeLibraryType(mt frontend.MediaType) string {
	if mt == frontend.TypeShow {
		return "series"
	}
	return "movie"
}

// ProjectLibrary interprets this adapter's plugin presentation declaration.
func ProjectLibrary(p frontend.Presentations, fallback frontend.MediaType) (frontend.MediaType, string, bool) {
	if p.Xtream == nil || p.Xtream.Category == "" {
		return "", "", false
	}
	mt := frontend.TypeMovie
	if p.Xtream.Category == "series" {
		mt = frontend.TypeShow
	}
	return mt, p.Xtream.Category, true

}
