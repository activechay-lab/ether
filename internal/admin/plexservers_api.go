package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plexwatch"
)

// PlexServers is server-to-server watch sync with Plex Media Servers the
// owner runs (*plexwatch.Servers). Tokens go in and never come back out.
type PlexServers interface {
	List(ctx context.Context) ([]plexwatch.PMSServer, error)
	ViewerServers(ctx context.Context, userID int64) ([]plexwatch.PMSViewerMapping, error)
	Add(ctx context.Context, name, baseURL, token string) (plexwatch.PMSServer, error)
	Update(ctx context.Context, id int64, name, baseURL, token string) (plexwatch.PMSServer, error)
	Remove(ctx context.Context, id int64) error
	Accounts(ctx context.Context, id int64) ([]plexwatch.PMSAccount, error)
	MapAccount(ctx context.Context, id, accountID int64, in plexwatch.PMSMappingInput) (plexwatch.PMSServer, error)
	SyncNow(ctx context.Context, id int64) (plexwatch.PMSServer, error)
	BackfillPreview(ctx context.Context, id, accountID int64) (plexwatch.PMSBackfillPreview, error)
	BackfillIn(ctx context.Context, id, accountID int64) (plexwatch.PMSServer, error)
	BackfillOut(ctx context.Context, id, accountID int64) (plexwatch.PMSServer, error)
	Unmatched(ctx context.Context, id, userID int64, after string) ([]plexwatch.PMSUnmatched, error)
}

var _ PlexServers = (*plexwatch.Servers)(nil)

func (h *Handler) plexServerRoutes(api func(string, http.HandlerFunc)) {
	api("GET /api/viewers/{id}/plex-servers", h.handleViewerPlexServers)
	if h.deps.PlexServers == nil {
		return
	}
	api("GET /api/plex-servers", h.handlePlexServers)
	api("POST /api/plex-servers", h.handlePlexServerAdd)
	api("PUT /api/plex-servers/{id}", h.handlePlexServerUpdate)
	api("DELETE /api/plex-servers/{id}", h.handlePlexServerRemove)
	api("GET /api/plex-servers/{id}/accounts", h.handlePlexServerAccounts)
	api("PUT /api/plex-servers/{id}/accounts/{account}", h.handlePlexServerMap)
	api("GET /api/plex-servers/{id}/accounts/{account}/backfill", h.handlePlexServerBackfillPreview)
	api("POST /api/plex-servers/{id}/accounts/{account}/backfill-in", h.handlePlexServerBackfillIn)
	api("POST /api/plex-servers/{id}/accounts/{account}/backfill-out", h.handlePlexServerBackfillOut)
	api("POST /api/plex-servers/{id}/sync", h.handlePlexServerSync)
	api("GET /api/plex-servers/{id}/unmatched", h.handlePlexServerUnmatched)
}

func (h *Handler) handleViewerPlexServers(w http.ResponseWriter, r *http.Request) {
	u, ok := h.viewerByID(w, r)
	if !ok {
		return
	}
	if h.deps.PlexServers == nil {
		writeJSON(w, http.StatusOK, []plexwatch.PMSViewerMapping{})
		return
	}
	list, err := h.deps.PlexServers.ViewerServers(r.Context(), u.ID)
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Handler) plexServerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plexwatch.ErrPMSToken):
		writeError(w, http.StatusBadRequest, "token", "the Plex Media Server refused this token")
	case errors.Is(err, plexwatch.ErrPMSNotOwner):
		writeError(w, http.StatusBadRequest, "not_owner", "this token is not the server owner's")
	case errors.Is(err, plexwatch.ErrPMSUnreachable):
		writeError(w, http.StatusBadGateway, "unreachable", "the Plex Media Server could not be reached")
	case errors.Is(err, plexwatch.ErrPMSDuplicate):
		writeError(w, http.StatusConflict, "duplicate", "already added")
	case errors.Is(err, plexwatch.ErrPMSDifferentServer):
		writeError(w, http.StatusConflict, "different_server", "that address is a different Plex Media Server")
	case errors.Is(err, plexwatch.ErrPMSLimit):
		writeError(w, http.StatusConflict, "limit", "limit reached")
	case errors.Is(err, plexwatch.ErrPMSNeedsToken):
		writeError(w, http.StatusBadRequest, "needs_token", "sending to Plex for this person needs their own token for this server")
	case errors.Is(err, plexwatch.ErrReceivingOff):
		writeError(w, http.StatusBadRequest, "receiving_off", "reading from Plex is off for this person")
	case errors.Is(err, plexwatch.ErrSendingOff):
		writeError(w, http.StatusBadRequest, "sending_off", "sending to Plex is off for this person")
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, plexwatch.ErrPMSInvalid), errors.Is(err, errBadJSON):
		writeError(w, http.StatusBadRequest, "invalid", "invalid server settings")
	case errors.Is(err, plexwatch.ErrPMSAddress):
		writeError(w, http.StatusBadRequest, "address", "enter the server address, like http://192.168.1.10:32400")
	default:
		h.log.Warn("plex media server request", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "the request failed")
	}
}

func pmsPathID(r *http.Request, name string) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n, err == nil && n > 0
}

func (h *Handler) handlePlexServers(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.PlexServers.List(r.Context())
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type plexServerBody struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

func (h *Handler) handlePlexServerAdd(w http.ResponseWriter, r *http.Request) {
	var body plexServerBody
	if err := decodeJSON(r, &body); err != nil {
		h.plexServerError(w, err)
		return
	}
	srv, err := h.deps.PlexServers.Add(r.Context(), body.Name, body.URL, body.Token)
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (h *Handler) handlePlexServerUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pmsPathID(r, "id")
	if !ok {
		h.plexServerError(w, core.ErrNotFound)
		return
	}
	var body plexServerBody
	if err := decodeJSON(r, &body); err != nil {
		h.plexServerError(w, err)
		return
	}
	srv, err := h.deps.PlexServers.Update(r.Context(), id, body.Name, body.URL, body.Token)
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (h *Handler) handlePlexServerRemove(w http.ResponseWriter, r *http.Request) {
	id, ok := pmsPathID(r, "id")
	if !ok {
		h.plexServerError(w, core.ErrNotFound)
		return
	}
	if err := h.deps.PlexServers.Remove(r.Context(), id); err != nil {
		h.plexServerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handlePlexServerAccounts(w http.ResponseWriter, r *http.Request) {
	id, ok := pmsPathID(r, "id")
	if !ok {
		h.plexServerError(w, core.ErrNotFound)
		return
	}
	list, err := h.deps.PlexServers.Accounts(r.Context(), id)
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Handler) handlePlexServerMap(w http.ResponseWriter, r *http.Request) {
	id, ok := pmsPathID(r, "id")
	account, ok2 := pmsPathID(r, "account")
	if !ok || !ok2 {
		h.plexServerError(w, core.ErrNotFound)
		return
	}
	var body struct {
		UserID    int64   `json:"userId"`
		Direction int     `json:"direction"`
		Interval  int     `json:"interval"`
		Token     *string `json:"token,omitempty"`
	}
	if err := decodeJSON(r, &body); err != nil {
		h.plexServerError(w, err)
		return
	}
	srv, err := h.deps.PlexServers.MapAccount(r.Context(), id, account, plexwatch.PMSMappingInput{
		UserID: body.UserID, Direction: body.Direction, Interval: body.Interval, Token: body.Token,
	})
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (h *Handler) handlePlexServerBackfillPreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pmsPathID(r, "id")
	account, ok2 := pmsPathID(r, "account")
	if !ok || !ok2 {
		h.plexServerError(w, core.ErrNotFound)
		return
	}
	preview, err := h.deps.PlexServers.BackfillPreview(r.Context(), id, account)
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (h *Handler) handlePlexServerBackfillIn(w http.ResponseWriter, r *http.Request) {
	id, ok := pmsPathID(r, "id")
	account, ok2 := pmsPathID(r, "account")
	if !ok || !ok2 {
		h.plexServerError(w, core.ErrNotFound)
		return
	}
	srv, err := h.deps.PlexServers.BackfillIn(r.Context(), id, account)
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (h *Handler) handlePlexServerBackfillOut(w http.ResponseWriter, r *http.Request) {
	id, ok := pmsPathID(r, "id")
	account, ok2 := pmsPathID(r, "account")
	if !ok || !ok2 {
		h.plexServerError(w, core.ErrNotFound)
		return
	}
	srv, err := h.deps.PlexServers.BackfillOut(r.Context(), id, account)
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (h *Handler) handlePlexServerSync(w http.ResponseWriter, r *http.Request) {
	id, ok := pmsPathID(r, "id")
	if !ok {
		h.plexServerError(w, core.ErrNotFound)
		return
	}
	srv, err := h.deps.PlexServers.SyncNow(r.Context(), id)
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (h *Handler) handlePlexServerUnmatched(w http.ResponseWriter, r *http.Request) {
	id, ok := pmsPathID(r, "id")
	if !ok {
		h.plexServerError(w, core.ErrNotFound)
		return
	}
	viewer, _ := strconv.ParseInt(r.URL.Query().Get("viewer"), 10, 64)
	list, err := h.deps.PlexServers.Unmatched(r.Context(), id, viewer, r.URL.Query().Get("after"))
	if err != nil {
		h.plexServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
