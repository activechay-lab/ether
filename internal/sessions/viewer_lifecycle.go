package sessions

import "github.com/braedonsaunders/ether/internal/store"

// Per-viewer lifecycle for this component's tables: how a viewer's rows
// are purged on removal and moved on merge. See store.RegisterViewerTable.
func init() {
	store.RegisterViewerTable("sessions", "plex_sessions", "user_id", store.ViewerMergeDrop)
	store.RegisterViewerTable("sessions", "plex_playqueues", "user_id", store.ViewerMergeDrop)
	store.RegisterViewerTable("sessions", "sessions_devices", "viewer_id", store.ViewerMergeMove, "protocol", "device_id")
	store.RegisterViewerTable("sessions", "sessions_delivery_authority", "viewer_id", store.ViewerMergeDrop)
	store.RegisterViewerTable("sessions", "sessions_detail_sources", "viewer_id", store.ViewerMergeDrop)
	store.RegisterViewerTable("sessions", "sessions_lazy_streams", "viewer_id", store.ViewerMergeDrop)
	store.RegisterViewerTable("sessions", "sessions_lazy_viewer_capacity", "viewer_id", store.ViewerMergeDrop)
}
