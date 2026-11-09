package silo

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
)

func (i *instance) subtitleRoutes() error {
	if err := i.router.Handle("POST", "/api/v2/subtitles/search", http.HandlerFunc(i.searchSubtitles)); err != nil {
		return err
	}
	return i.router.Handle("POST", "/api/v2/subtitles/download", http.HandlerFunc(i.downloadSubtitle))
}

type subtitleSearchBody struct {
	FileID    string   `json:"media_file_id"`
	Languages []string `json:"languages"`
}

type subtitleDownloadBody struct {
	FileID          string  `json:"media_file_id"`
	Provider        string  `json:"provider"`
	SubtitleID      string  `json:"subtitle_id"`
	Language        string  `json:"language"`
	ReleaseName     string  `json:"release_name"`
	Score           float64 `json:"score"`
	HearingImpaired bool    `json:"hearing_impaired"`
}

func (i *instance) searchSubtitles(w http.ResponseWriter, r *http.Request) {
	viewer, _, err := i.viewer(r)
	if err != nil || viewer == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var body subtitleSearchBody
	if decode(r, &body) != nil || body.FileID == "" || body.Languages == nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	key, ok := i.authorizedFile(w, r, viewer, body.FileID)
	if !ok {
		return
	}
	warnings := []string{}
	results := []any{}
	if i.host.Subtitles == nil || !i.host.Subtitles.Enabled() {
		warnings = append(warnings, "Subtitle search is not configured")
		reply(w, http.StatusOK, map[string]any{"results": results, "warnings": warnings})
		return
	}
	languages := body.Languages
	if len(languages) > 8 {
		languages = languages[:8]
		warnings = append(warnings, "Only the first 8 languages were searched")
	}
	seen := map[string]bool{}
	for _, requested := range languages {
		language := frontend.NormalizeLanguage(requested)
		if !subtitleLanguage.MatchString(language) {
			warnings = append(warnings, "Skipped an unrecognized language")
			continue
		}
		offers, err := i.host.Subtitles.Search(r.Context(), viewer, device(r), key, language)
		if err != nil {
			if errors.Is(err, frontend.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			warnings = append(warnings, "Subtitle search is unavailable")
			continue
		}
		for _, offer := range offers {
			language := frontend.NormalizeLanguage(offer.Language)
			if !subtitleLanguage.MatchString(language) || offer.ID == "" || seen[offer.ID] {
				continue
			}
			seen[offer.ID] = true
			wireID, err := i.wireID(r.Context(), "subtitle", offer.ID)
			if err != nil {
				http.Error(w, "Unavailable", http.StatusServiceUnavailable)
				return
			}
			name := offer.Name
			if name == "" {
				name = "Subtitle"
			}
			format := offer.Format
			if format == "" {
				format = "srt"
			}
			results = append(results, map[string]any{
				"id": wireID, "provider": "opensubtitles", "language": language, "release_name": name,
				"format": format, "score": offer.Rating, "downloads": offer.Downloads, "hearing_impaired": offer.HearingImpaired,
			})
		}
	}
	reply(w, http.StatusOK, map[string]any{"results": results, "warnings": warnings})
}

func (i *instance) downloadSubtitle(w http.ResponseWriter, r *http.Request) {
	viewer, _, err := i.viewer(r)
	if err != nil || viewer == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var body subtitleDownloadBody
	if decode(r, &body) != nil || body.FileID == "" || body.SubtitleID == "" || body.Provider == "" || body.Language == "" {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(body.Provider, "opensubtitles") || !subtitleLanguage.MatchString(frontend.NormalizeLanguage(body.Language)) {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	key, ok := i.authorizedFile(w, r, viewer, body.FileID)
	if !ok {
		return
	}
	if i.host.Subtitles == nil || !i.host.Subtitles.Enabled() {
		http.NotFound(w, r)
		return
	}
	nativeID, err := i.nativeID(r.Context(), "subtitle", body.SubtitleID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := i.host.Subtitles.Download(r.Context(), viewer, device(r), key, nativeID); err != nil {
		if errors.Is(err, frontend.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return
	}
	reply(w, http.StatusOK, map[string]any{"subtitle": map[string]any{
		"id": body.SubtitleID, "media_file_id": body.FileID, "provider": "opensubtitles",
		"language": frontend.NormalizeLanguage(body.Language), "format": "srt", "release_name": body.ReleaseName,
		"score": body.Score, "hearing_impaired": body.HearingImpaired,
		"created_at": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"timing":     map[string]any{"offset_ms": 0, "scale": 1.0},
	}})
}

func (i *instance) authorizedFile(w http.ResponseWriter, r *http.Request, viewer *frontend.Viewer, fileID string) (frontend.RatingKey, bool) {
	if i.host.SourceOffers == nil || i.host.Experience == nil {
		http.NotFound(w, r)
		return 0, false
	}
	var client frontend.ClientCaps
	if i.host.Capabilities != nil {
		built, err := i.host.Capabilities.Build(i.identityContext(r), frontend.CapabilityRequest{Viewer: viewer, Device: caps.DeviceInfo{Protocol: "silo", ClientID: device(r).ID, Product: device(r).Client, Version: device(r).Version, DeviceName: device(r).DeviceName}, UserAgent: r.UserAgent()})
		if err != nil {
			http.Error(w, "Unavailable", http.StatusServiceUnavailable)
			return 0, false
		}
		client = built.Client
	}
	offer, err := i.host.SourceOffers.Resolve(r.Context(), frontend.VersionScope{Viewer: viewer, Device: device(r), Protocol: "silo", Caps: client}, fileID)
	if err != nil || offer.Target.Item == 0 {
		http.NotFound(w, r)
		return 0, false
	}
	if _, err = i.host.Experience.Item(i.identityContext(r), frontend.ItemRequest{Viewer: viewer, Device: device(r), Key: offer.Target.Item}); err != nil {
		http.NotFound(w, r)
		return 0, false
	}
	return offer.Target.Item, true
}

var subtitleLanguage = regexp.MustCompile(`^(?:[a-z]{2,3}(?:-[0-9A-Za-z]{1,8})*|x(?:-[0-9A-Za-z]{1,8})+)$`)

// tryServeSessionSubtitleResource intercepts ASS sidecar
// (subtitles/{track}.ass) and Matroska font attachment routes
// (subtitles/{track}/fonts and subtitles/{track}/fonts/{index}) after viewer
// authentication, delegating extraction and authorization to the host
// SessionSubtitleArtifacts capability.
func (i *instance) tryServeSessionSubtitleResource(w http.ResponseWriter, r *http.Request, _ sessionMedia, access frontend.SessionAccess, sessionID, resource string) bool {
	rest, ok := strings.CutPrefix(resource, "subtitles/")
	if !ok || rest == "" {
		return false
	}
	if trackStr, hasASS := strings.CutSuffix(rest, ".ass"); hasASS && !strings.Contains(trackStr, "/") {
		i.serveSessionASSSubtitle(w, r, access, sessionID, trackStr)
		return true
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 2 && parts[1] == "fonts" {
		i.serveSessionSubtitleFonts(w, r, access, sessionID, parts[0], "")
		return true
	}
	if len(parts) == 3 && parts[1] == "fonts" {
		i.serveSessionSubtitleFonts(w, r, access, sessionID, parts[0], parts[2])
		return true
	}
	return false
}

func parseSessionSubtitleTrackIndex(trackStr string) (int, bool) {
	trackStr = strings.TrimSuffix(trackStr, ".ass")
	trackStr = strings.TrimSuffix(trackStr, ".vtt")
	trackIndex, err := strconv.Atoi(trackStr)
	if err != nil || trackIndex < 0 {
		return -1, false
	}
	return trackIndex, true
}

func parseSubtitleSyncQueryMS(r *http.Request) int {
	raw := strings.TrimSpace(r.URL.Query().Get("sync_ms"))
	if raw == "" {
		return 0
	}
	val, err := strconv.Atoi(raw)
	if err != nil || val < -10000 || val > 10000 {
		return 0
	}
	return val
}

func writeSubtitleArtifactError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, frontend.ErrSubtitleFontsUnavailable):
		problem(w, http.StatusUnprocessableEntity, "urn:ether:problem:subtitle-fonts-unavailable", "Subtitle font attachments unavailable", "Attached font payload exceeds the 48 MiB limit and could not be narrowed to script-referenced font attachments.")
	case errors.Is(err, frontend.ErrSubtitleAuthoringUnavailable):
		problem(w, http.StatusUnprocessableEntity, "urn:ether:problem:subtitle-fidelity-unavailable", "Authored subtitle fidelity unavailable", "Complete authored ASS/SSA subtitle styling is unavailable for this stream.")
	case errors.Is(err, frontend.ErrUnauthorized):
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
	case errors.Is(err, frontend.ErrSourceGone):
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
	default:
		http.NotFound(w, r)
	}
}

func (i *instance) serveSessionASSSubtitle(w http.ResponseWriter, r *http.Request, access frontend.SessionAccess, sessionID, trackStr string) {
	trackIndex, ok := parseSessionSubtitleTrackIndex(trackStr)
	if !ok {
		http.NotFound(w, r)
		return
	}
	artifacts, ok := i.host.Sessions.(frontend.SessionSubtitleArtifacts)
	if !ok {
		http.NotFound(w, r)
		return
	}
	requireAuthored := false
	if snap, found := i.rememberedPlan(sessionID); found {
		requireAuthored = snap.requireAuthored
	}
	if q := strings.TrimSpace(r.URL.Query().Get("require_authored_fidelity")); q == "1" || strings.EqualFold(q, "true") {
		requireAuthored = true
	}
	artifact, err := artifacts.ServeASSSubtitle(r.Context(), access, sessionID, trackIndex, parseSubtitleSyncQueryMS(r), requireAuthored)
	if err != nil {
		writeSubtitleArtifactError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/x-ssa; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(artifact.Data)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(artifact.Data)
	}
}

func (i *instance) serveSessionSubtitleFonts(w http.ResponseWriter, r *http.Request, access frontend.SessionAccess, sessionID, trackStr, fontIdxStr string) {
	fontIndex := -1
	if fontIdxStr != "" {
		parsed, err := strconv.Atoi(fontIdxStr)
		if err != nil || parsed < 0 || parsed >= frontend.MaxSubtitleFontAttachments {
			http.NotFound(w, r)
			return
		}
		fontIndex = parsed
	}
	trackIndex, ok := parseSessionSubtitleTrackIndex(trackStr)
	if !ok {
		http.NotFound(w, r)
		return
	}
	artifacts, ok := i.host.Sessions.(frontend.SessionSubtitleArtifacts)
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	if fontIndex >= 0 {
		att, err := artifacts.SubtitleFontAttachment(r.Context(), access, sessionID, trackIndex, fontIndex)
		if err != nil {
			writeSubtitleArtifactError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", att.MIMEType)
		w.Header().Set("Content-Length", strconv.Itoa(len(att.Data)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(att.Data)
		}
		return
	}

	items, err := artifacts.SubtitleFontBundle(r.Context(), access, sessionID, trackIndex)
	if err != nil {
		writeSubtitleArtifactError(w, r, err)
		return
	}
	if items == nil {
		items = []frontend.SubtitleFontBundleItem{}
	}
	reply(w, http.StatusOK, map[string]any{"items": items})
}
