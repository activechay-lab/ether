package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
)

// ViewerLifecycle is the people-lifecycle service behind pause, resume,
// sign-out, remove and merge (satisfied by *auth.Service).
type ViewerLifecycle interface {
	PauseViewer(ctx context.Context, viewerID int64) error
	ResumeViewer(ctx context.Context, viewerID int64) error
	RevokeViewerSessions(ctx context.Context, viewerID int64) (int, error)
	RemoveViewer(ctx context.Context, viewerID int64) error
	MergeViewers(ctx context.Context, fromID, intoID int64) error
}

// viewerLifecycle is the lifecycle object every user and viewer row
// carries. Plex viewers are managed on plex.tv: Ether never pauses,
// removes or merges them, which managedBy and the can* flags say plainly.
type viewerLifecycle struct {
	Source     string `json:"source"` // owner | local | invite | plex | plexHome | dlnaGuest
	Paused     bool   `json:"paused"`
	PlexShared bool   `json:"plexShared"`
	CanRemove  bool   `json:"canRemove"`
	CanMerge   bool   `json:"canMerge"`
	CanPause   bool   `json:"canPause"`
	ManagedBy  string `json:"managedBy"` // "plex" or ""
}

func lifecycleOf(u ViewerRecord) viewerLifecycle {
	hasPlex := u.HasPlex || u.PlexID != 0
	lc := viewerLifecycle{Paused: !u.IsOwner && !u.OwnerEnabled}
	switch {
	case u.IsOwner:
		lc.Source = "owner"
		lc.Paused = false
	case u.Origin == "dlna_guest":
		lc.Source = "dlnaGuest"
	case hasPlex && u.Home:
		lc.Source = "plexHome"
	case hasPlex:
		lc.Source = "plex"
	case u.Origin == "invite":
		lc.Source = "invite"
	default:
		lc.Source = "local"
	}
	if !u.IsOwner && u.Origin != "dlna_guest" && hasPlex {
		lc.PlexShared = u.PlexShared
		lc.ManagedBy = "plex"
		return lc
	}
	if lc.Source != "owner" {
		lc.CanRemove, lc.CanMerge, lc.CanPause = true, true, true
	}
	return lc
}

// userWithLifecycle is a core.User row plus its lifecycle object.
type userWithLifecycle struct {
	core.User
	Lifecycle viewerLifecycle `json:"lifecycle"`
}

func toUserWithLifecycle(u ViewerRecord) userWithLifecycle {
	return userWithLifecycle{User: u.User, Lifecycle: lifecycleOf(u)}
}

// recordByID finds one stored viewer, answering 400/404/502 itself.
func (h *Handler) recordByID(w http.ResponseWriter, r *http.Request, id int64) (*ViewerRecord, bool) {
	users, err := h.storedViewers(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "users_failed", err.Error())
		return nil, false
	}
	for i := range users {
		if users[i].ID == id {
			u := users[i]
			return &u, true
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "no such viewer")
	return nil, false
}

func lifecycleID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad_id", "viewer id must be a number")
		return 0, false
	}
	return id, true
}

// refuseLifecycle answers for viewers Ether must not pause, remove or
// merge: the owner (403) and Plex viewers managed on plex.tv (409).
func refuseLifecycle(w http.ResponseWriter, u *ViewerRecord) bool {
	if u.IsOwner {
		writeError(w, http.StatusForbidden, "owner", "You can't change your own account this way.")
		return true
	}
	if !lifecycleOf(*u).CanPause {
		writeError(w, http.StatusConflict, "plex_managed", "This viewer is managed on plex.tv. Change their access there.")
		return true
	}

	return false
}

func writeLifecycleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrViewerIsOwner):
		writeError(w, http.StatusForbidden, "owner", "You can't change your own account this way.")
	case errors.Is(err, auth.ErrViewerPlexManaged):
		writeError(w, http.StatusConflict, "plex_managed", "This viewer is managed on plex.tv. Change their access there.")
	case errors.Is(err, auth.ErrMergeSameViewer):
		writeError(w, http.StatusBadRequest, "invalid", "Choose a different viewer to merge into.")
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such viewer")
	default:
		writeError(w, http.StatusInternalServerError, "lifecycle_failed", err.Error())
	}
}

func (h *Handler) lifecycleReady(w http.ResponseWriter, r *http.Request) bool {
	access, _ := RequestAccess(r.Context())
	if !access.Owner {
		writeError(w, http.StatusForbidden, "forbidden", "owner required")
		return false
	}
	if h.deps.Lifecycle == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "viewer management unavailable")
		return false
	}
	return true
}

// signOutEverywhere revokes the viewer's device grants and app
// passwords (identity) plus console sessions and delegations (auth).
func (h *Handler) signOutEverywhere(ctx context.Context, id int64) (int, error) {
	total := 0
	if h.deps.IdentityGrants != nil {
		n, err := h.deps.IdentityGrants.SignOutViewer(ctx, id)
		if err != nil {
			return total, err
		}
		total += n
	}
	if h.deps.Lifecycle != nil {
		n, err := h.deps.Lifecycle.RevokeViewerSessions(ctx, id)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func (h *Handler) handlePauseViewer(w http.ResponseWriter, r *http.Request) {
	if !h.lifecycleReady(w, r) {
		return
	}
	id, ok := lifecycleID(w, r)
	if !ok {
		return
	}
	u, ok := h.recordByID(w, r, id)
	if !ok || refuseLifecycle(w, u) {
		return
	}
	if err := h.deps.Lifecycle.PauseViewer(r.Context(), id); err != nil {
		writeLifecycleError(w, err)
		return
	}
	if _, err := h.signOutEverywhere(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "signout_failed", "paused, but signing them out everywhere failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"paused": true})
}

func (h *Handler) handleResumeViewer(w http.ResponseWriter, r *http.Request) {
	if !h.lifecycleReady(w, r) {
		return
	}
	id, ok := lifecycleID(w, r)
	if !ok {
		return
	}
	u, ok := h.recordByID(w, r, id)
	if !ok || refuseLifecycle(w, u) {
		return
	}
	if err := h.deps.Lifecycle.ResumeViewer(r.Context(), id); err != nil {
		writeLifecycleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"paused": false})
}

// handleSignOutViewer signs a viewer out everywhere. Unlike pause,
// remove and merge it is allowed for Plex viewers too: it only revokes
// Ether-side credentials, never the plex.tv account or share.
func (h *Handler) handleSignOutViewer(w http.ResponseWriter, r *http.Request) {
	access, _ := RequestAccess(r.Context())
	if !access.Owner {
		writeError(w, http.StatusForbidden, "forbidden", "owner required")
		return
	}
	if h.deps.IdentityGrants == nil && h.deps.Lifecycle == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "sign out unavailable")
		return
	}
	id, ok := lifecycleID(w, r)
	if !ok {
		return
	}
	if _, ok := h.recordByID(w, r, id); !ok {
		return
	}
	n, err := h.signOutEverywhere(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "signout_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"revoked": n})
}

func (h *Handler) handleRemoveViewer(w http.ResponseWriter, r *http.Request) {
	if !h.lifecycleReady(w, r) {
		return
	}
	id, ok := lifecycleID(w, r)
	if !ok {
		return
	}
	u, ok := h.recordByID(w, r, id)
	if !ok || refuseLifecycle(w, u) {
		return
	}
	if err := h.deps.Lifecycle.RemoveViewer(r.Context(), id); err != nil {
		writeLifecycleError(w, err)
		return
	}
	if h.deps.ViewerCatalogRules != nil {
		if err := h.deps.ViewerCatalogRules.RefreshViewerRules(r.Context()); err != nil {
			writeError(w, 500, "rules_reload", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

func (h *Handler) handleMergeViewer(w http.ResponseWriter, r *http.Request) {
	if !h.lifecycleReady(w, r) {
		return
	}
	id, ok := lifecycleID(w, r)
	if !ok {
		return
	}
	var body struct {
		Into int64 `json:"into"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Into <= 0 {
		writeError(w, http.StatusBadRequest, "invalid", "choose a viewer to merge into")
		return
	}
	if body.Into == id {
		writeError(w, http.StatusBadRequest, "invalid", "Choose a different viewer to merge into.")
		return
	}
	u, ok := h.recordByID(w, r, id)
	if !ok || refuseLifecycle(w, u) {
		return
	}
	target, ok := h.recordByID(w, r, body.Into)
	if !ok || refuseLifecycle(w, target) {
		return
	}
	if err := h.deps.Lifecycle.MergeViewers(r.Context(), id, body.Into); err != nil {
		writeLifecycleError(w, err)
		return
	}
	if h.deps.ViewerCatalogRules != nil {
		if err := h.deps.ViewerCatalogRules.RefreshViewerRules(r.Context()); err != nil {
			writeError(w, 500, "rules_reload", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"merged": true, "into": body.Into})
}

func (h *Handler) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id, ok := lifecycleID(w, r)
	if !ok {
		return
	}
	u, ok := h.recordByID(w, r, id)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserWithLifecycle(*u)})
}
