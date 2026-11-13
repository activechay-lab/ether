package stremio

import "github.com/braedonsaunders/ether/internal/core"

// NativeLibraryType returns this adapter's built-in library category.
func NativeLibraryType(mt core.MediaType) string {
	if mt == core.TypeShow {
		return "series"
	}
	return "movie"
}

// ProjectLibrary interprets this adapter's plugin presentation declaration.
func ProjectLibrary(p core.Presentations, fallback core.MediaType) (core.MediaType, string, bool) {
	if p.Stremio == nil || p.Stremio.Type == "" {
		return "", "", false
	}
	mt := core.TypeMovie
	if p.Stremio.Type == "series" {
		mt = core.TypeShow
	}
	return mt, p.Stremio.Type, true

}
