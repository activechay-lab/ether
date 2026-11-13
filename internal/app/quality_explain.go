package app

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
	"github.com/braedonsaunders/ether/internal/frontends/plex"
	"github.com/braedonsaunders/ether/internal/resolver"
)

// qualityClient is a known app the owner can ask "who gets what" about.
// The identities are what each official app sends, so the capability
// registry and quality rules match them exactly as they match playback.
type qualityClient struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Product  string        `json:"product"`
	Platform string        `json:"platform"`
	Device   string        `json:"device,omitempty"`
	Protocol core.Protocol `json:"protocol,omitempty"`
}

func qualityClients() []qualityClient {
	return []qualityClient{
		{ID: "plex-apple-tv", Name: "Plex for Apple TV", Product: "Plex for Apple TV", Platform: "tvOS", Device: "Apple TV"},
		{ID: "plex-ios", Name: "Plex for iPhone and iPad", Product: "Plex for iOS", Platform: "iOS", Device: "iPhone"},
		{ID: "plex-android-tv", Name: "Plex for Android TV", Product: "Plex for Android (TV)", Platform: "Android", Device: "Android TV"},
		{ID: "plex-shield", Name: "Plex on NVIDIA Shield", Product: "Plex for Android (TV)", Platform: "Android", Device: "SHIELD Android TV"},
		{ID: "plex-fire-tv", Name: "Plex for Fire TV", Product: "Plex for Android (TV)", Platform: "Android", Device: "AFTKRT"},
		{ID: "plex-android", Name: "Plex for Android phones", Product: "Plex for Android (Mobile)", Platform: "Android", Device: "Pixel 8"},
		{ID: "plex-roku", Name: "Plex for Roku", Product: "Plex for Roku", Platform: "Roku"},
		{ID: "plex-lg", Name: "Plex for LG", Product: "Plex for LG", Platform: "webOS"},
		{ID: "plex-samsung", Name: "Plex for Samsung", Product: "Plex for Samsung", Platform: "Tizen"},
		{ID: "plex-xbox", Name: "Plex for Xbox", Product: "Plex for Xbox", Platform: "Xbox"},
		{ID: "plex-playstation", Name: "Plex for PlayStation", Product: "Plex for PlayStation", Platform: "PlayStation"},
		{ID: "plex-web-chrome", Name: "Plex Web in Chrome", Product: "Plex Web", Platform: "Chrome"},
		{ID: "plex-web-safari", Name: "Plex Web in Safari", Product: "Plex Web", Platform: "Safari"},
		{ID: "plex-htpc", Name: "Plex HTPC", Product: "Plex HTPC", Platform: "Windows"},
		{ID: "plex-kodi", Name: "Plex for Kodi", Product: "PM4K", Device: "Kodi"},
		{ID: "plex-desktop", Name: "Plex for Windows and Mac", Product: "Plex for Windows", Platform: "Windows"},
		{ID: "jellyfin", Name: "Jellyfin", Product: "Jellyfin", Protocol: "jellyfin"},
		{ID: "emby", Name: "Emby", Product: "Emby", Protocol: "emby"},
	}
}

type qualityExplainIn struct {
	UserID    int64          `json:"userId"`
	ClientID  string         `json:"clientId,omitempty"`
	Client    *qualityClient `json:"client,omitempty"`
	Remote    bool           `json:"remote"`
	TitleKey  string         `json:"titleKey,omitempty"`
	MediaType string         `json:"mediaType,omitempty"`
}

// qualityHandler serves the Quality page's "Who gets what" checker.
func (a *App) qualityHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /clients", func(w http.ResponseWriter, r *http.Request) {
		writeQualityJSON(w, http.StatusOK, map[string]any{"clients": qualityClients()})
	})
	m.HandleFunc("POST /explain", a.handleQualityExplain)
	return m
}

func (a *App) handleQualityExplain(w http.ResponseWriter, r *http.Request) {
	var in qualityExplainIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		writeQualityError(w, http.StatusBadRequest, "bad_json", "the request body must be a JSON object")
		return
	}
	if in.MediaType != core.MediaAny && in.MediaType != core.MediaMovie && in.MediaType != core.MediaTV {
		writeQualityError(w, http.StatusBadRequest, "bad_media", "media type must be movie or tv")
		return
	}
	var client qualityClient
	switch {
	case in.ClientID == "any":
		// Explicitly ask for a generic client with no assumed app.
	case in.ClientID != "":
		found := false
		for _, c := range qualityClients() {
			if c.ID == in.ClientID {
				client, found = c, true
				break
			}
		}
		if !found {
			writeQualityError(w, http.StatusBadRequest, "unknown_client", "no such app")
			return
		}
	case in.Client != nil && strings.TrimSpace(in.Client.Product) != "":
		client = qualityClient{Product: strings.TrimSpace(in.Client.Product), Platform: strings.TrimSpace(in.Client.Platform), Device: strings.TrimSpace(in.Client.Device)}
	default:
		writeQualityError(w, http.StatusBadRequest, "missing_client", "choose an app")
		return
	}
	if len(client.Product) > 128 || len(client.Platform) > 128 || len(client.Device) > 128 {
		writeQualityError(w, http.StatusBadRequest, "bad_client", "app fields are too long")
		return
	}
	u, err := a.users.ActiveUser(r.Context(), in.UserID)
	if err != nil || u == nil {
		writeQualityError(w, http.StatusNotFound, "unknown_viewer", "no such viewer")
		return
	}
	var title *core.Title
	if in.TitleKey != "" {
		key, err := core.ParseRatingKey(in.TitleKey)
		if err != nil {
			writeQualityError(w, http.StatusBadRequest, "bad_title", "invalid title")
			return
		}
		loaded, err := a.catalog.CachedGet(r.Context(), key)
		if err != nil || loaded == nil || (loaded.Type != core.TypeMovie && loaded.Type != core.TypeEpisode) {
			writeQualityError(w, http.StatusNotFound, "unknown_title", "no such movie or episode")
			return
		}
		title = loaded
	}
	cfg := a.cfg.Read()
	// The Plex request path: registry, owner App compatibility edits, the
	// request's headers and the remote bitrate cap (plex.Request.ClientCapsWith).
	pr := &plex.Request{Product: client.Product, Platform: client.Platform, Device: client.Device, DeviceName: client.Device, Remote: in.Remote, User: u}
	declared := pr.ClientCapsWith(cfg.Transcode.RemoteMaxBitrateKbps, cfg.Clients.Overrides)
	builtin := pr.ClientCapsWith(cfg.Transcode.RemoteMaxBitrateKbps, nil)
	if frontendHostPolicy(client.Protocol).ProfileQualityEvaluation {
		info := caps.DeviceInfo{Protocol: client.Protocol, Product: client.Product, Platform: client.Platform, Device: client.Device, Remote: in.Remote}
		neutral, err := (caps.Builder{}).Build(r.Context(), info, caps.DeviceProfile{}, nil, "")
		if err != nil {
			writeQualityError(w, http.StatusBadRequest, "bad_client", "cannot evaluate app")
			return
		}
		builtin = playbackFloor(neutral, nil)
		declared = playbackFloor(neutral, cfg.Clients.Overrides)
	} else if client.Product == "" {
		declared, builtin = core.ClientCaps{Remote: in.Remote, HDR10: true, DolbyVision: true}, core.ClientCaps{Remote: in.Remote, HDR10: true, DolbyVision: true}
	}

	q := resolver.QualityQuestion{Quality: cfg.Quality, Caps: declared, Builtin: &builtin, User: u, Title: title, MediaType: in.MediaType, RemoteMaxBitrateKbps: cfg.Transcode.RemoteMaxBitrateKbps}
	if a.safety != nil {
		policy := a.safety.ForUser(u)
		q.ViewerMaxBitrateKbps = policy.MaxBitrateKbps
		q.RemoteDisabled = policy.RemoteDisabled
	}
	writeQualityJSON(w, http.StatusOK, resolver.ExplainQuality(q))
}

func writeQualityJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeQualityError(w http.ResponseWriter, code int, kind, message string) {
	writeQualityJSON(w, code, map[string]any{"error": map[string]string{"code": kind, "message": message}})
}
