package frontend

import "github.com/braedonsaunders/ether/internal/core"

// Catalogue identities are stable across frontends. Their encoding stays in
// core; adapters parse and render the same identifiers through this boundary.
type KeyKind = core.KeyKind

const (
	KindMovie      = core.KindMovie
	KindCollection = core.KindCollection
	KindSmart      = core.KindSmart
	KindPlaylist   = core.KindPlaylist
	KindLocal      = core.KindLocal
	TypeMovie      = core.TypeMovie
	TypeShow       = core.TypeShow
	TypeSeason     = core.TypeSeason
	TypeEpisode    = core.TypeEpisode
)

func MovieKey(id int64) RatingKey { return core.MovieKey(id) }
func EpisodeKey(show int64, season, episode int) RatingKey {
	return core.EpisodeKey(show, season, episode)
}
func ParseRatingKey(value string) (RatingKey, error) { return core.ParseRatingKey(value) }
