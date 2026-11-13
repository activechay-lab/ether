package watchlist

import "github.com/braedonsaunders/ether/internal/store"

// Per-viewer lifecycle for this component's tables: how a viewer's rows
// are purged on removal and moved on merge. See store.RegisterViewerTable.
func init() {
	store.RegisterViewerTable("watchlist", "watchlist_items", "user_id", store.ViewerMergeMove, "item_key", "source")
	store.RegisterViewerTable("watchlist", "watchlist_episodes", "user_id", store.ViewerMergeMove, "item_key", "episode_key")
	store.RegisterViewerTable("watchlist", "watchlist_community_jobs", "user_id", store.ViewerMergeDrop)
}
