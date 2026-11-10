package frontend

// WatchRefreshSubscriber subscribes a mounted frontend to committed changes
// that invalidate viewer metadata. Closing must fence in-flight callbacks.
// explicitOnly excludes playback progress/completion refreshes for fixtures.
type WatchRefreshSubscriber func(refresh func(viewerID int64, key RatingKey), explicitOnly bool) (close func())

// HomeInvalidationSubscriber subscribes a mounted frontend to committed state
// changes that invalidate a viewer's home screen. Closing fences in-flight callbacks.
type HomeInvalidationSubscriber func(invalidate func(viewerID int64)) (close func())
