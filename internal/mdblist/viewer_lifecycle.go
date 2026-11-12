package mdblist

import "github.com/braedonsaunders/ether/internal/store"

// Per-viewer lifecycle for this component's tables: how a viewer's rows
// are purged on removal and moved on merge. See store.RegisterViewerTable.
func init() {
	store.RegisterViewerTable("mdblist", "mdblist_accounts", "user_id", store.ViewerMergeMove)
	store.RegisterViewerTable("mdblist", "mdblist_outbox", "user_id", store.ViewerMergeDrop)
}
