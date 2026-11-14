package identity

import "github.com/braedonsaunders/ether/internal/store"

// Per-viewer lifecycle for this component's tables: how a viewer's rows
// are purged on removal and moved on merge. See store.RegisterViewerTable.
func init() {
	store.RegisterViewerTable("identity", "identity_web_codes", "viewer_id", store.ViewerMergeDrop)
	store.RegisterViewerTable("identity", "identity_grants", "viewer_id", store.ViewerMergeUnion)
	store.RegisterViewerTable("identity", "identity_refresh_families", "viewer_id", store.ViewerMergeUnion)
	store.RegisterViewerTable("identity", "identity_invites", "viewer_id", store.ViewerMergeUnion)
	store.RegisterViewerTable("identity", "identity_quickconnect", "viewer_id", store.ViewerMergeDrop)
	store.RegisterViewerTable("identity", "identity_grant_revocations", "viewer_id", store.ViewerMergeDrop)
	store.RegisterViewerTable("identity", "identity_viewer_times", "viewer_id", store.ViewerMergeMove)
	store.RegisterViewerTable("identity", "frontend_image_grants", "viewer_id", store.ViewerMergeDrop)
	store.RegisterViewerTable("identity", "frontend_preferences", "viewer_id", store.ViewerMergeMove, "surface", "client", "pref_key")
}
