package mediabrowser

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// Jellyfin and Emby version picker ("Choose a version"). The Plex frontend
// already lists a title's ranked releases through the shared Variants service
// (frontend.Editions.Variants) and this file offers the same list to the
// MediaBrowser apps: detail and PlaybackInfo list an Auto source first, then
// up to frontend.MaxVariants ranked release variants, each an ordinary
// MediaSource whose Id is the scoped version handle that opens exactly that
// release (the handle machinery already refuses substitutes).
//
// Cost: only single-item detail and PlaybackInfo resolve variants, never a
// list, home row or search. Variants reads the resolver's cached search
// results (it does not spend indexer budget) and the broker resolves each
// release for real only when it is played, so a long play does not
// re-resolve. Ids are a keyed hash of (surface, title, release) and are
// stable for the life of the release.

// detailVariantBudget bounds how long an opened detail page waits for the
// release search it started, matching the Plex frontend's render budget.
const detailVariantBudget = 3 * time.Second

// refillDetailSearch starts the title's release search when its cached
// results are missing, as the Plex detail page does, and waits up to
// detailVariantBudget for it so the first open can already list versions.
// The resolver returns nil (nothing to wait for) when results are cached,
// a search is cooling off, or both refill slots are busy.
func (i *instance) refillDetailSearch(r *http.Request, title frontend.Title) {
	if i.host.Editions == nil || r == nil || (title.Type != "movie" && title.Type != "episode") {
		return
	}
	if !title.Released.IsZero() && title.Released.After(time.Now()) {
		return
	}
	t := title
	done := i.host.Editions.RefillSearchCache(&t)
	if done == nil {
		return
	}
	timer := time.NewTimer(detailVariantBudget)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-r.Context().Done():
	}
}

// autoVersionName is the label the app shows for the automatic pick; it
// mirrors the Plex frontend's own "Auto" entry.
const autoVersionName = "Auto"

// mediaSourceName builds the picker name for one variant from facts the
// release already carries: resolution + source + codec/HDR, then audio, then
// size, e.g. "2160p WEB-DL HEVC DV · TrueHD 7.1 · 18.4 GB". It never names
// the release group or a file path.
func mediaSourceName(v frontend.ReleaseVariant) string {
	info := v.Release.Info
	quality := []string{}
	switch {
	case info.Height >= 1800:
		quality = append(quality, "2160p")
	case info.Height >= 1000:
		quality = append(quality, "1080p")
	case info.Height >= 700:
		quality = append(quality, "720p")
	case info.Height > 0:
		quality = append(quality, strconv.Itoa(info.Height)+"p")
	}
	if source := frontend.SourceLabel(info.Source); source != "" {
		quality = append(quality, source)
	}
	if codec := videoCodecLabel(info.VideoCodec); codec != "" {
		quality = append(quality, codec)
	}
	if info.DolbyVision {
		quality = append(quality, "DV")
	} else if info.HDR10 {
		quality = append(quality, "HDR")
	}
	parts := []string{strings.Join(quality, " ")}
	if audio := audioLabel(info); audio != "" {
		parts = append(parts, audio)
	}
	if size := mediaSizeLabel(v.Release.Size); size != "" {
		parts = append(parts, size)
	}
	name := strings.Join(parts, " · ")
	if name == "" {
		name = v.Label
	}
	if name == "" {
		name = autoVersionName
	}
	return name
}

func videoCodecLabel(codec string) string {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "hevc", "h265", "h.265", "x265":
		return "HEVC"
	case "h264", "h.264", "avc", "x264":
		return "H.264"
	case "av1":
		return "AV1"
	case "vc1":
		return "VC-1"
	case "mpeg2", "mpeg2video":
		return "MPEG-2"
	}
	return ""
}

func audioLabel(info frontend.ReleaseInfo) string {
	codec := map[string]string{
		"aac": "AAC", "ac3": "DD", "eac3": "DD+", "truehd": "TrueHD",
		"dts": "DTS", "dtshd": "DTS-HD", "flac": "FLAC", "opus": "Opus", "mp3": "MP3",
	}[strings.ToLower(strings.TrimSpace(info.AudioCodec))]
	if codec == "" {
		codec = strings.ToUpper(strings.TrimSpace(info.AudioCodec))
	}
	parts := []string{}
	if codec != "" {
		parts = append(parts, codec)
	}
	if info.Atmos {
		parts = append(parts, "Atmos")
	}
	if channels := strings.TrimSpace(info.Channels); channels != "" {
		parts = append(parts, channels)
	}
	return strings.Join(parts, " ")
}

func mediaSizeLabel(size int64) string {
	const gb = 1 << 30
	if size >= gb {
		return strconv.FormatFloat(float64(size)/gb, 'f', 1, 64) + " GB"
	}
	if size >= 1<<20 {
		return strconv.FormatInt((size+(1<<20)/2)>>20, 10) + " MB"
	}
	return ""
}

// releaseVariants lists this client's ranked releases for one title, or nil
// when no variants service is wired or fewer than two (Auto alone) exist.
// It never returns an error to the caller: a missing list falls back to the
// single Auto source exactly as before.
func (i *instance) releaseVariants(r *http.Request, scope frontend.VersionScope, title frontend.Title) []frontend.ReleaseVariant {
	if i.host.Editions == nil || r == nil || scope.Viewer == nil {
		return nil
	}
	t := title
	variants, err := i.host.Editions.Variants(i.identityContext(r), frontend.ResolveRequest{Title: &t, Client: scope.Caps, User: scope.Viewer}, frontend.MaxVariants)
	if err != nil || len(variants) < 2 {
		return nil
	}
	return variants
}

// releaseVersionSource renders one non-Auto variant as a MediaSource whose Id
// is the scoped handle that opens exactly that release. It reuses the same
// handle machinery (and dialect shaping) the edition versions use. It returns
// false when this viewer may not be issued the release, so the source is
// omitted rather than offered with an unusable Id.
func (i *instance) releaseVersionSource(r *http.Request, scope frontend.VersionScope, key frontend.RatingKey, v frontend.ReleaseVariant) (map[string]any, bool) {
	if i.host.VersionHandles == nil || v.Release.ID == "" {
		return nil, false
	}
	handle, err := i.host.VersionHandles.Issue(i.identityContext(r), scope, key, v.Release.ID)
	if err != nil || handle == "" {
		return nil, false
	}
	source := i.detailMediaSourceDTO(handle, v.Info)
	source["Name"] = mediaSourceName(v)
	i.mediaSourceDialect(source)
	if i.protocol == "emby" {
		i.embyMediaSourceDTO(source, ids.RatingKeyGUID(key), v.Info)
	}
	if i.protocol == "jellyfin" {
		i.jellyfinMediaSourceDTO(source, v.Info, false)
	}
	return source, true
}

// releaseVersionSources appends one Auto source (today's source) followed by
// each offered non-Auto variant that this viewer may be issued. It returns
// nil when there is no usable version list, so the caller keeps today's
// single-source shape.
func (i *instance) releaseVersionSources(r *http.Request, scope frontend.VersionScope, key frontend.RatingKey, title frontend.Title, auto map[string]any) []any {
	return i.releaseVersionSourcesFor(r, scope, key, i.releaseVariants(r, scope, title), auto)
}

// releaseVersionSourcesFor is releaseVersionSources with an already-resolved
// variant list, so PlaybackInfo does not resolve it twice.
func (i *instance) releaseVersionSourcesFor(r *http.Request, scope frontend.VersionScope, key frontend.RatingKey, variants []frontend.ReleaseVariant, auto map[string]any) []any {
	if len(variants) < 2 {
		return nil
	}
	out := make([]any, 0, len(variants))
	if auto != nil {
		auto["Name"] = autoVersionName
		out = append(out, auto)
	}
	offered := 0
	for _, v := range variants {
		if v.Auto {
			continue
		}
		source, ok := i.releaseVersionSource(r, scope, key, v)
		if !ok {
			continue
		}
		out = append(out, source)
		offered++
	}
	if offered == 0 {
		return nil
	}
	return out
}

// issueReleaseHandle returns the scoped handle for one release, or "" when
// this viewer cannot be issued it.
// autoSwitchPlan plans Auto's switch to release exactly as a client pick of
// that version is planned: through its scoped handle and exact release. The
// session is then bound to the handle returned as the MediaSource Id, so the
// stream the client opens with it matches the session.
func (i *instance) autoSwitchPlan(r *http.Request, scope frontend.VersionScope, base frontend.PlanRequest, release string) (frontend.PlanRequest, frontend.PlayPlan, string, bool) {
	handle := i.issueReleaseHandle(r, scope, base.Item, release)
	if handle == "" {
		return base, frontend.PlayPlan{}, "", false
	}
	alt := base
	alt.Version, alt.ResolvedRelease = handle, release
	alt.Intent.ExactVersion = true
	plan, err := i.host.Sessions.Plan(r.Context(), alt)
	if err != nil || plan.Estimated {
		return base, frontend.PlayPlan{}, "", false
	}
	return alt, plan, handle, true
}

func (i *instance) issueReleaseHandle(r *http.Request, scope frontend.VersionScope, key frontend.RatingKey, release string) string {
	if i.host.VersionHandles == nil || release == "" {
		return ""
	}
	handle, err := i.host.VersionHandles.Issue(i.identityContext(r), scope, key, release)
	if err != nil {
		return ""
	}
	return handle
}

// playbackTitle loads the title facts the variants service needs. It is one
// cached catalogue read on the single-item PlaybackInfo path.
func (i *instance) playbackTitle(r *http.Request, v *frontend.Viewer, key frontend.RatingKey) (frontend.Title, bool) {
	if i.host.Experience == nil || key == 0 || v == nil {
		return frontend.Title{}, false
	}
	detail, err := i.host.Experience.Item(i.identityContext(r), frontend.ItemRequest{Viewer: v, Device: device(r), Key: key})
	if err != nil || detail.Title.Title == "" {
		return frontend.Title{}, false
	}
	return detail.Title, true
}

// detailSources builds the MediaSources list for a single item page. With an
// explicit release or existing editions it keeps the historical shape; with
// neither, and when the ranked release variants are available, it lists Auto
// followed by the variants. Returns nil when no source applies.
func (i *instance) detailSources(r *http.Request, v *frontend.Viewer, token string, d frontend.ItemDetail, dto map[string]any, key frontend.RatingKey, releaseID string) []any {
	sources := make([]any, 0, len(d.Versions))
	scope, scopeErr := i.versionScope(r, v)
	if releaseID == "" && len(d.Versions) == 0 && scopeErr == nil {
		i.refillDetailSearch(r, d.Title)
		if versionSources := i.releaseVersionSources(r, scope, key, d.Title, i.defaultMediaSource(r, v, token, key)); versionSources != nil {
			return versionSources
		}
	}
	needDefault := scopeErr != nil || len(d.Versions) == 0
	for _, version := range d.Versions {
		if releaseID != "" && version.ID != releaseID {
			continue
		}
		if scopeErr != nil {
			break
		}
		handle, e := i.host.VersionHandles.Issue(i.identityContext(r), scope, key, version.ID)
		if e != nil || handle == "" {
			needDefault = true
			continue
		}
		source := i.detailMediaSourceDTO(handle, version.Media)
		source["Name"] = version.Name
		i.versionSourceFacts(source, version)
		plan := frontend.PlanRequest{Viewer: v, Device: scope.Device, Protocol: i.protocol, Item: key, Version: handle, Caps: scope.Caps, Intent: frontend.PlayIntent{Estimate: true, ExactVersion: true, Options: frontend.DecideOptions{DirectPlay: true}}}
		if path := i.lazySourceURL(r, token, plan); path != "" {
			source["Path"] = path
		}
		if i.protocol == "jellyfin" {
			offerJellyfinStatic(source, frontend.DecideOptions{DirectPlay: true, DirectStream: true})
		}
		i.mediaSourceDialect(source)
		if i.protocol == "emby" {
			i.embyMediaSourceDTO(source, ids.RatingKeyGUID(d.Title.Key), version.Media)
		}
		if i.protocol == "jellyfin" {
			i.jellyfinMediaSourceDTO(source, version.Media, false)
			if name, _ := source["Name"].(string); name == "" {
				if itemName, ok := dto["Name"].(string); ok && itemName != "" {
					source["Name"] = itemName
				}
			}
		}
		sources = append(sources, source)
	}
	if needDefault {
		sources = append(sources, i.defaultMediaSource(r, v, token, key))
	}
	if len(sources) == 0 {
		return nil
	}
	return sources
}

// videoTranscode reports that a plan would re-encode video for this client.
// A remux or audio-only conversion leaves VideoCopy true and does not count.
func videoTranscode(plan frontend.PlayPlan) bool {
	return !plan.Decision.VideoCopy && plan.Media.Video() != nil
}

// maxAutoTranscodeTrials bounds the extra candidates Auto tries before it
// accepts a video transcode, per the owner's spec.
const maxAutoTranscodeTrials = 3

// avoidVideoTranscode returns the release id Auto should play instead of its
// own pick when the automatic choice would force a video transcode and a
// ranked alternative would not. It plans at most maxAutoTranscodeTrials next
// variants in rank order with an estimate (no probe, no session), and returns
// "" when none avoids a video transcode (keep Auto).
func (i *instance) avoidVideoTranscode(ctx context.Context, req frontend.PlanRequest, variants []frontend.ReleaseVariant) string {
	if i.host.Sessions == nil || len(variants) < 2 {
		return ""
	}
	trials := 0
	for idx := 1; idx < len(variants) && trials < maxAutoTranscodeTrials; idx++ {
		v := variants[idx]
		if v.Release.ID == "" {
			continue
		}
		trials++
		candidate := req
		candidate.Version = v.Release.ID
		candidate.ResolvedRelease = ""
		candidate.Intent.ExactVersion = false
		candidate.Intent.Estimate = true
		plan, err := i.host.Sessions.Plan(ctx, candidate)
		if err != nil {
			continue
		}
		if !videoTranscode(plan) {
			return v.Release.ID
		}
	}
	return ""
}
