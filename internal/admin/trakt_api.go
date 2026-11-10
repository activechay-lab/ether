package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/identity"
)

// TraktDevice is the browser-safe portion of an active Trakt device-code flow.
// It never includes the secret device_code.
type TraktDevice struct {
	UserCode        string    `json:"userCode"`
	VerificationURL string    `json:"verificationUrl"`
	ExpiresAt       time.Time `json:"expiresAt"`
	Interval        int       `json:"interval"`
}

// TraktStatus is a viewer's Trakt connection state. It never includes OAuth
// access or refresh tokens.
type TraktStatus struct {
	Configured bool         `json:"configured"`
	Connected  bool         `json:"connected"`
	Username   string       `json:"username,omitempty"`
	Status     string       `json:"status,omitempty"`
	Device     *TraktDevice `json:"device,omitempty"`
	PortalURL  string       `json:"portalUrl,omitempty"`
}

// Sentinels returned by the app adapter so this package does not import
// internal/trakt directly.
var (
	ErrTraktNotConfigured = errors.New("trakt is not configured")
	ErrTraktNoDeviceCode  = errors.New("no pending trakt device code")
)

// Trakt is the per-viewer Trakt OAuth device-code integration.
type Trakt interface {
	Status(ctx context.Context, userID int64) (TraktStatus, error)
	StartDevice(ctx context.Context, userID int64) (TraktStatus, error)
	PollDevice(ctx context.Context, userID int64) (TraktStatus, error)
	Disconnect(ctx context.Context, userID int64) (TraktStatus, error)
}

func (h *Handler) traktViewer(w http.ResponseWriter, r *http.Request) (*ViewerRecord, bool) {
	access, _ := RequestAccess(r.Context())
	if !access.Owner {
		writeError(w, http.StatusForbidden, "forbidden", "owner required")
		return nil, false
	}
	if h.deps.Trakt == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "trakt is not available")
		return nil, false
	}
	return h.viewerByID(w, r)
}

func (h *Handler) writeTrakt(w http.ResponseWriter, st TraktStatus, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, st)
		return
	}
	switch {
	case errors.Is(err, ErrTraktNotConfigured):
		writeError(w, http.StatusConflict, "not_configured", "Trakt is not configured")
	case errors.Is(err, ErrTraktNoDeviceCode):
		writeError(w, http.StatusGone, "no_device_code", "no pending Trakt device code")
	default:
		writeError(w, http.StatusBadGateway, "trakt", "Trakt could not be reached")
	}
}

func (h *Handler) ownerTraktCall(w http.ResponseWriter, r *http.Request, call func(Trakt, context.Context, int64) (TraktStatus, error)) {
	v, ok := h.traktViewer(w, r)
	if !ok {
		return
	}
	st, err := call(h.deps.Trakt, r.Context(), v.ID)
	if err == nil && h.deps.ViewerPortalURL != nil {
		if u, uerr := h.deps.ViewerPortalURL(r.Context(), r); uerr == nil && u != "" {
			st.PortalURL = u
		}
	}
	h.writeTrakt(w, st, err)
}

func (h *Handler) handleTraktStatus(w http.ResponseWriter, r *http.Request) {
	h.ownerTraktCall(w, r, Trakt.Status)
}

func (h *Handler) handleTraktStart(w http.ResponseWriter, r *http.Request) {
	h.ownerTraktCall(w, r, Trakt.StartDevice)
}

func (h *Handler) handleTraktPoll(w http.ResponseWriter, r *http.Request) {
	h.ownerTraktCall(w, r, Trakt.PollDevice)
}

func (h *Handler) handleTraktDisconnect(w http.ResponseWriter, r *http.Request) {
	h.ownerTraktCall(w, r, Trakt.Disconnect)
}

func (h *Handler) portalTraktCall(w http.ResponseWriter, r *http.Request, rateLimited bool, call func(Trakt, context.Context, int64) (TraktStatus, error)) {
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, "csrf", "request rejected")
		return
	}
	viewer, err := h.portalViewer(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "sign in required")
		return
	}
	if rateLimited && h.deps.PollLimiter != nil {
		ip, _ := identity.ClientNet(r, h.proxyPrefixes()...)
		ok, lerr := h.deps.PollLimiter.Allow(r.Context(), "trakt:"+strconv.FormatInt(viewer.ID, 10), ip)
		if lerr != nil || !ok {
			writeError(w, http.StatusTooManyRequests, "rate_limit", "try again later")
			return
		}
	}
	if h.deps.Trakt == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "trakt is not available")
		return
	}
	st, err := call(h.deps.Trakt, r.Context(), viewer.ID)
	h.writeTrakt(w, st, err)
}

func (h *Handler) portalTraktStatus(w http.ResponseWriter, r *http.Request) {
	h.portalTraktCall(w, r, false, Trakt.Status)
}

func (h *Handler) portalTraktStart(w http.ResponseWriter, r *http.Request) {
	h.portalTraktCall(w, r, true, Trakt.StartDevice)
}

func (h *Handler) portalTraktPoll(w http.ResponseWriter, r *http.Request) {
	h.portalTraktCall(w, r, true, Trakt.PollDevice)
}

func (h *Handler) portalTraktDisconnect(w http.ResponseWriter, r *http.Request) {
	h.portalTraktCall(w, r, false, Trakt.Disconnect)
}
