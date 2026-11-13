package app

import "github.com/braedonsaunders/ether/internal/store"

// Per-viewer lifecycle for this component's tables: how a viewer's rows
// are purged on removal and moved on merge. See store.RegisterViewerTable.
func init() {
	store.RegisterViewerTable("mediabrowser_personal_home", "mediabrowser_personal_home", "viewer_id", store.ViewerMergeDrop)
	store.RegisterViewerTable("watchlist_community_jobs", "watchlist_community_jobs", "user_id", store.ViewerMergeDrop)
	store.RegisterViewerTable("watchlist_community_locks", "watchlist_community_locks", "user_id", store.ViewerMergeDrop)
}
