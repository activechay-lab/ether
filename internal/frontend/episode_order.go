package frontend

import "context"

// EpisodeOrder reads the viewer's presented ordering. The host owns saved
// groups and applies them to stored catalogue pages before adapters render.
type EpisodeOrder interface {
	Customized(context.Context, *Viewer) (bool, error)
	Grouped(context.Context, *Viewer, []RatingKey) (map[RatingKey]bool, error)
	Get(context.Context, *Viewer, RatingKey) (*Title, error)
	Children(context.Context, *Viewer, RatingKey) ([]Title, error)
	Grandchildren(context.Context, *Viewer, RatingKey) ([]Title, error)
	OrderStoredGrandchildren(context.Context, *Viewer, RatingKey, []Title) ([]Title, error)
	Position(context.Context, *Viewer, RatingKey) (season, episode int, name string, ok bool)
}
