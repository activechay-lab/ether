package frontendadapter

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// KindMediaPath is the Ether-owned endpoint a plugin embeds in an adapter
// response (spec §6.5). The path segment after it is the opaque ticket.
const KindMediaPath = "/ether/v1/kind-media/"

// MediaDeps are the seams the kind-media endpoint needs. The app fills them
// from pluginitems + the kind resolver; the endpoint never touches tables.
type MediaDeps struct {
	// Tickets redeems the scoped, short-lived grant.
	Tickets *TicketIssuer
	// Viewer loads the current policy user by id, so the library and rating
	// checks use live restrictions rather than a stale ticket snapshot.
	Viewer func(ctx context.Context, id int64) (*core.Viewer, error)
	// Open opens one plugin item ref for the granted viewer, honouring the
	// request's accepted MIME types. It runs the same authorization path
	// kind playback uses.
	Open func(ctx context.Context, viewer *core.Viewer, itemRef string, accept []string) (*frontend.KindMedia, error)
}

// MediaHandler serves /ether/v1/kind-media/{ticket}: it redeems the ticket,
// loads the viewer, opens the item through KindStreams and streams the
// bytes with Range support. The ticket is the only credential the request
// needs.
type MediaHandler struct {
	deps MediaDeps
	now  func() time.Time
}

// NewMediaHandler builds the endpoint handler. It returns nil when the
// dependencies are incomplete, so app wiring can leave the route unmounted.
func NewMediaHandler(deps MediaDeps) *MediaHandler {
	if deps.Tickets == nil || deps.Viewer == nil || deps.Open == nil {
		return nil
	}
	return &MediaHandler{deps: deps, now: time.Now}
}

func (h *MediaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, KindMediaPath)
	if token == "" || strings.Contains(token, "/") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	grant, err := h.deps.Tickets.Redeem(token)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	viewer, err := h.deps.Viewer(r.Context(), grant.ViewerID)
	if err != nil || viewer == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	accept := parseAccept(r.Header.Get("Accept"))
	media, err := h.deps.Open(r.Context(), viewer, grant.ItemID, accept)
	if err != nil || media == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer media.Closer.Close()
	h.serve(w, r, media)
}

// serve streams the media with Range support. ServeContent needs an
// io.ReadSeeker, so the ReaderAt is wrapped in a SectionReader over the
// whole object.
func (h *MediaHandler) serve(w http.ResponseWriter, r *http.Request, media *frontend.KindMedia) {
	if media.Size <= 0 {
		// Unknown size: stream once, no Range.
		if media.MIME != "" {
			w.Header().Set("Content-Type", media.MIME)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, io.NewSectionReader(media.ReaderAt, 0, 1<<62))
		return
	}
	name := media.Name
	if name == "" {
		name = "media"
	}
	if media.MIME != "" {
		w.Header().Set("Content-Type", media.MIME)
	}
	http.ServeContent(w, r, name, time.Time{}, io.NewSectionReader(media.ReaderAt, 0, media.Size))
}

// parseAccept splits an Accept header into media types, lowercased and
// trimmed; a plugin's resolver uses it to pick a format.
func parseAccept(header string) []string {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	parts := strings.Split(header, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if mt := strings.TrimSpace(strings.SplitN(p, ";", 2)[0]); mt != "" {
			out = append(out, mt)
		}
	}
	return out
}
