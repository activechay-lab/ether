package frontend

import "github.com/braedonsaunders/ether/internal/core"

// Protocol adapters use these aliases to exchange host values without importing
// domain packages directly. Their implementations remain owned by core.
type Viewer = core.Viewer
type Device = core.Device
type Title = core.Title
type Collection = core.Collection
type Person = core.Person
type RatingKey = core.RatingKey
type Playlist = core.Playlist
type MediaType = core.MediaType
type Page = core.Page
type WatchStatus = core.WatchStatus
type StreamInfo = core.StreamInfo
type MediaInfo = core.MediaInfo
type DecideOptions = core.DecideOptions
type ClientCaps = core.ClientCaps
type PlaybackSourceHistory = core.PlaybackSourceHistory
type Decision = core.Decision
type FacetValue = core.FacetValue
type User = core.User

// Library browse and history values stay owned by core. Adapters build
// queries and read results through these aliases.
type BrowseQuery = core.BrowseQuery
type CreditFilter = core.CreditFilter
type Filter = core.Filter
type FilterGroup = core.FilterGroup
type StringFilter = core.StringFilter
type SortOrder = core.SortOrder
type SortSpec = core.SortSpec
type HistoryEntry = core.HistoryEntry
type WatchPage = core.WatchPage
type TitleFacts = core.TitleFacts

const (
	SortAdded      = core.SortAdded
	SortDuration   = core.SortDuration
	SortPopularity = core.SortPopularity
	SortRating     = core.SortRating
	SortReleased   = core.SortReleased
	SortTitle      = core.SortTitle
	SortUpdated    = core.SortUpdated
	SortYear       = core.SortYear
)

// Catalogue key kinds and constructors stay owned by core. Adapters compare
// and rebuild parent keys through these aliases.
const (
	KindShow    = core.KindShow
	KindSeason  = core.KindSeason
	KindEpisode = core.KindEpisode
	KindExtra   = core.KindExtra
)

func ShowKey(tmdb int64) RatingKey { return core.ShowKey(tmdb) }

func SeasonKey(show int64, season int) RatingKey {
	return core.SeasonKey(show, season)
}
