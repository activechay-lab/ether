// Package experience contains protocol-independent viewer experience logic.
package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// The viewer access-policy contract and its pure helpers live in
// internal/frontend (host_sessions_experience.go) so protocol adapters use
// them without importing this package. These aliases keep the experience
// service and host wiring on the same types.
type AccessPolicy = frontend.AccessPolicy
type Visibility = frontend.Visibility

var (
	TitleAllowed       = frontend.TitleAllowed
	SectionAllowed     = frontend.SectionAllowed
	ViewerRestricted   = frontend.ViewerRestricted
	MaySetViewerRating = frontend.MaySetViewerRating
)

// LibrarySection maps a title or episode to the configured library of that kind.
// Seasons and episodes belong to the show library. Unknown kinds use the movie library.
func LibrarySection(libraries []Library, kind core.MediaType) (int, bool) {
	want := core.TypeMovie
	switch kind {
	case core.TypeShow, core.TypeSeason, core.TypeEpisode:
		want = core.TypeShow
	}
	for _, library := range libraries {
		if library.Type == want {
			return library.ID, true
		}
	}
	return 0, false
}

// BindVisibility is the shared library, policy and availability port.
// Frontends supply sources; they do not decide which library a media type uses.
func BindVisibility(policy AccessPolicy, get func(context.Context, core.RatingKey) (*core.Title, error), available func(context.Context, []core.Title) []core.Title, sessions func() []*core.PlaySession, libraries []Library) Visibility {
	return Visibility{Policy: policy, GetTitle: get, FilterAvailable: available, Sessions: sessions,
		SectionFor: func(kind core.MediaType) (int, bool) { return LibrarySection(libraries, kind) }}
}
