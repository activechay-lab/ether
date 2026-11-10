package experience

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type plexExperience struct {
	stepAside *StepAside
}

// NewPlexExperience returns the experience-backed implementation of the
// Plex experience host contract.
func NewPlexExperience() frontend.PlexExperience { return plexExperience{} }

// NewPlexExperienceWithStepAside returns a PlexExperience that disables
// unrestricted fast-paths whenever stepAside holds matched titles, ensuring
// owner and unrestricted viewers still run step-aside filtering across recent,
// search, collection, discovery, and post-play paths.
func NewPlexExperienceWithStepAside(stepAside *StepAside) frontend.PlexExperience {
	return plexExperience{stepAside: stepAside}
}

func (p plexExperience) wrapUnrestricted(fn func(*core.User) bool) func(*core.User) bool {
	if p.stepAside == nil || fn == nil {
		return fn
	}
	return func(u *core.User) bool {
		if p.stepAside.Active() {
			return false
		}
		return fn(u)
	}
}

func (p plexExperience) NewRecent(o frontend.RecentOptions) frontend.Recent {
	return Recent{Visibility: o.Visibility, Unrestricted: p.wrapUnrestricted(o.Unrestricted)}
}
func (plexExperience) NewDetailRelated(o frontend.DetailRelatedOptions) frontend.DetailRelated {
	return DetailRelated{Source: o.Source, Visibility: o.Visibility, Get: o.Get}
}
func (plexExperience) NewPlexTimeline(o frontend.PlexTimelineOptions) frontend.PlexTimeline {
	return PlexTimeline{Writer: o.Writer, Now: o.Now, RestoreContinue: o.RestoreContinue}
}
func (plexExperience) NewEpisodeBatch(o frontend.EpisodeBatchOptions) frontend.EpisodeBatch {
	b := EpisodeBatch{Catalog: o.Catalog, State: o.State}
	if o.EpisodeOrder != nil {
		b.EpisodeOrder = o.EpisodeOrder
	}
	return b
}
func (plexExperience) NewContinueDeck(o frontend.ContinueDeckOptions) frontend.ContinueDeck {
	return &ContinueDeck{Load: o.Load, Order: o.Order, Hydrate: o.Hydrate, Unavailable: o.Unavailable, Fallback: o.Fallback}
}
func (p plexExperience) NewContinue(o frontend.ContinueOptions) frontend.Continue {
	c := Continue{
		ShowOnDeck: o.ShowOnDeck, PrepareShowOnDeck: o.PrepareShowOnDeck, PrefetchTitles: o.PrefetchTitles,
		Filter: o.Filter, State: o.State, Now: o.Now, Grandchildren: o.Grandchildren,
		NextEpisode: o.NextEpisode, NextEpisodes: o.NextEpisodes, Visible: o.Visible,
		SameDayUnavailable: o.SameDayUnavailable, GetTitles: o.GetTitles, SectionAllowed: o.SectionAllowed,
		OnError: o.OnError, FavoriteShows: o.FavoriteShows, Unrestricted: p.wrapUnrestricted(o.Unrestricted),
	}
	if o.EpisodeOrder != nil {
		c.EpisodeOrder = o.EpisodeOrder
	}
	return c
}
func (plexExperience) NewRowSelection(o frontend.RowSelectionOptions) frontend.RowSelection {
	return RowSelection{Visibility: o.Visibility, PrioritizeAvailable: o.PrioritizeAvailable, SameDayUnavailable: o.SameDayUnavailable}
}
func (p plexExperience) NewHomeSelection(o frontend.HomeSelectionOptions) frontend.HomeSelection {
	restricted := o.Restricted || (p.stepAside != nil && p.stepAside.Active())
	return HomeSelection{Source: o.Source, Visibility: o.Visibility, Restricted: restricted}
}
func (p plexExperience) NewScopedCollection(o frontend.ScopedCollectionOptions) frontend.ScopedCollection {
	return ScopedCollection{Collections: o.Collections, Visibility: o.Visibility, Now: o.Now, Unrestricted: p.wrapUnrestricted(o.Unrestricted), TitleAllowed: o.TitleAllowed, Openable: o.Openable}
}
func (plexExperience) NewPlaylistSelection(o frontend.PlaylistSelectionOptions) frontend.PlaylistSelection {
	return PlaylistSelection{Playlists: o.Playlists, Collections: o.Collections, Visibility: o.Visibility, Children: o.Children, Grandchildren: o.Grandchildren}
}
func (p plexExperience) NewPostPlaySelection(o frontend.PostPlaySelectionOptions) frontend.PostPlaySelection {
	unrestricted := o.Unrestricted
	if p.stepAside != nil && p.stepAside.Active() {
		unrestricted = false
	}
	return PostPlaySelection{Visibility: o.Visibility, Unrestricted: unrestricted, Picks: o.Picks, Related: o.Related}
}
func (p plexExperience) NewDiscoverySelection(o frontend.DiscoverySelectionOptions) frontend.DiscoverySelection {
	return DiscoverySelection{Visibility: o.Visibility, Unrestricted: p.wrapUnrestricted(o.Unrestricted)}
}
func (plexExperience) NewTrackPreferenceSelector(o frontend.TrackPreferenceSelectorOptions) frontend.TrackPreferenceSelector {
	return TrackPreferences{Source: o.Source, GetTitle: o.GetTitle}
}
func (p plexExperience) SearchFor(src frontend.SearchSources, vis frontend.Visibility, unrestricted func(*core.User) bool) frontend.Search {
	return SearchFor(src, vis, p.wrapUnrestricted(unrestricted))
}
func (plexExperience) StatisticsDefaults(dashboard frontend.Dashboard, resources frontend.ResourceStatistics, now func() time.Time) (frontend.Dashboard, frontend.ResourceStatistics) {
	return Statistics(dashboard, resources, now)
}
func (plexExperience) MergeShareFilter(filter, share core.Filter) (core.Filter, bool) {
	return MergeShareFilter(filter, share)
}
func (plexExperience) ArrangeHome(ctx context.Context, source core.Recommender, viewer *core.User, hc core.HubContext, section string, rows []core.Hub, candidates []core.NeutralHomeRow) ([]int, error) {
	return ArrangeHome(ctx, source, viewer, hc, section, rows, candidates)
}
func (plexExperience) DetailExtras(ctx context.Context, source frontend.ExtrasSource, parent *core.Title, language string, wanted map[int]bool) ([]frontend.ExtraClip, error) {
	return DetailExtras(ctx, source, parent, language, wanted)
}
func (plexExperience) DiscoverySearch(source *frontend.DiscoveryFeatures) func(context.Context, *core.User, string, int) ([]core.Title, bool) {
	return DiscoverySearch(source)
}
func (plexExperience) FamiliarFrom(st any) func(context.Context, *core.User, []core.RatingKey) map[core.RatingKey]int {
	return FamiliarFrom(st)
}
func (plexExperience) ContinueFilter(state core.State, personal func(context.Context, *core.User, []core.Title) []core.Title) func(context.Context, *core.User, []core.Title) []core.Title {
	return ContinueFilter(state, personal)
}
func (plexExperience) SameDayUnavailable(ctx context.Context, title *core.Title, now time.Time, filter func(context.Context, []core.Title) []core.Title, source core.AvailabilitySource) bool {
	return SameDayUnavailable(ctx, title, now, filter, source)
}
func (plexExperience) SeasonNextEpisode(eps []core.Title, st map[core.RatingKey]core.WatchStatus, now time.Time, unavailable func(*core.Title) bool) *core.Title {
	return SeasonNextEpisode(eps, st, now, unavailable)
}
func (plexExperience) ShowOnDeck(eps []core.Title, st map[core.RatingKey]core.WatchStatus, unavailable func(*core.Title) bool) *core.Title {
	return ShowOnDeck(eps, st, unavailable)
}
func (plexExperience) RequestDatabaseObservation(ctx context.Context) (int64, time.Duration) {
	return RequestDatabaseObservation(ctx)
}
func (plexExperience) PlaybackMarkerRanges(ctx context.Context, source frontend.MarkerRanges, key core.RatingKey, info core.MediaInfo) ([]frontend.MarkerRange, error) {
	return PlaybackMarkerRanges(ctx, source, key, info)
}
