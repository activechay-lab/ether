package frontend

// WatchRefreshSubscriber subscribes a mounted frontend to committed changes
// that invalidate viewer metadata. Closing must fence in-flight callbacks.
// explicitOnly excludes playback progress/completion refreshes for fixtures.
type WatchRefreshSubscriber func(refresh func(viewerID int64, key RatingKey), explicitOnly bool) (close func())
