package core

import "regexp"

var artworkPath = regexp.MustCompile(`^/[A-Za-z0-9_\-]{1,128}\.(jpg|jpeg|png|webp|svg)$`)

// ValidArtworkPath accepts one TMDB image filename, never a host or traversal.
func ValidArtworkPath(path string) bool { return artworkPath.MatchString(path) }

// NeedsGeneratedPoster reports whether a title has its own fallback poster.
// Seasons and episodes inherit their show's poster instead.
func NeedsGeneratedPoster(t *Title) bool {
	return t != nil && t.PosterPath == "" && (t.Type == TypeMovie || t.Type == TypeShow)
}
