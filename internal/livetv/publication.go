package livetv

import "context"

type plexLineupKey struct{}

// WithPlexLineup selects the owner's Plex publication subset. Viewer access
// restrictions remain independent and are always applied as well.
func WithPlexLineup(ctx context.Context) context.Context {
	return context.WithValue(ctx, plexLineupKey{}, true)
}

func publishedTo(ctx context.Context, c Channel) bool {
	plex, _ := ctx.Value(plexLineupKey{}).(bool)
	return !plex || !c.PlexHidden
}
