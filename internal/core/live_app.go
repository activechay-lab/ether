package core

import "context"

type liveAppKey struct{}

// WithLiveApp names the client app (frontend surface id such as "plex" or
// "jellyfin") a Live TV request is served to, so the owner's per-app channel
// visibility applies. Requests without an app (the owner console) see every
// channel; viewer access rules apply either way.
func WithLiveApp(ctx context.Context, app string) context.Context {
	return context.WithValue(ctx, liveAppKey{}, app)
}

// LiveApp returns the app WithLiveApp recorded, or "".
func LiveApp(ctx context.Context) string {
	app, _ := ctx.Value(liveAppKey{}).(string)
	return app
}
