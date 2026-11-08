package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/langtag"
	"github.com/braedonsaunders/ether/internal/trackprefs"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// TrackPreferenceSource supplies persisted viewer rules and show choices.
type TrackPreferenceSource interface {
	Select(context.Context, int64, core.RatingKey, core.MediaInfo, string) (trackprefs.Selection, error)
	GetRules(context.Context, int64) (trackprefs.Rules, error)
}

// TrackSelection holds positions in MediaInfo.Streams, not StreamInfo.Index.
// Set flags distinguish an omitted selection from position zero, and an
// explicit subtitle-off selection (-1) from an omitted subtitle choice.
type TrackSelection struct {
	Audio, Subtitle       int
	AudioSet, SubtitleSet bool
}

// TrackPreferences applies persisted choices beneath explicit client choices.
// GetTitle is optional; an episode with no original language uses its show's
// language when it can be loaded, and otherwise retains the empty language.
type TrackPreferences struct {
	Source   TrackPreferenceSource
	GetTitle func(context.Context, core.RatingKey) (*core.Title, error)
}

// Select returns the original selection unchanged if preferences are absent
// or their lookup fails. A lookup error is returned for the caller to log.
func (p TrackPreferences) Select(ctx context.Context, viewer *core.User, key core.RatingKey, info core.MediaInfo, original string, selected TrackSelection) (TrackSelection, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if p.Source == nil || viewer == nil {
		return selected, nil
	}
	if original == "" && key.Kind() == core.KindEpisode && p.GetTitle != nil {
		if show, err := p.GetTitle(ctx, core.ShowKey(key.TMDB())); err == nil && show != nil {
			original = show.Language
		}
	}
	preferred, err := p.Source.Select(ctx, viewer.ID, key, info, original)
	if err != nil {
		return selected, err
	}
	if !selected.AudioSet {
		selected.Audio, selected.AudioSet = preferred.Audio, true
	}
	if !selected.SubtitleSet {
		selected.Subtitle, selected.SubtitleSet = preferred.Subtitle, true
	}
	return selected, nil
}

// AudioFilters applies the viewer's rules for the selected stream's channel
// count. Missing preferences return no filters; lookup failures are reported.
func (p TrackPreferences) AudioFilters(ctx context.Context, viewer *core.User, info core.MediaInfo, selected TrackSelection) ([]string, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if p.Source == nil || viewer == nil {
		return nil, nil
	}
	rules, err := p.Source.GetRules(ctx, viewer.ID)
	if err != nil {
		return nil, err
	}
	channels := 0
	if selected.Audio >= 0 && selected.Audio < len(info.Streams) {
		channels = info.Streams[selected.Audio].Channels
	}
	return rules.AudioFilters(channels), nil
}

// TextForImageSubtitle is a text track that can stand in for the image
// subtitle at pos: same language, forced flag and hearing-impaired style,
// embedded before external. An image track is burned into the video for
// most clients, a full transcode; a text track rides beside a remux. -1
// when there is none.
func TextForImageSubtitle(info core.MediaInfo, pos int) int {
	if pos < 0 || pos >= len(info.Streams) {
		return -1
	}
	img := info.Streams[pos]
	if img.Type != "subtitle" || !transcode.IsImageSubtitle(img.Codec) || img.Language == "" {
		return -1
	}
	best := -1
	for i, st := range info.Streams {
		if st.Type != "subtitle" || !transcode.IsTextSubtitle(st.Codec) || st.Forced != img.Forced ||
			trackprefs.SDH(st) != trackprefs.SDH(img) || langtag.Normalize(st.Language) != langtag.Normalize(img.Language) {
			continue
		}
		if st.External && st.ExternalURL == "" {
			continue
		}
		if !st.External {
			return i
		}
		if best < 0 {
			best = i
		}
	}
	return best
}

// SelectScoped applies scoped rules before unscoped preferences. A failed
// scoped lookup falls back; explicit client choices always retain priority.
// Plex automatically substitutes a matching text subtitle for an image track.
func (p TrackPreferences) SelectScoped(ctx context.Context, viewer *core.User, key core.RatingKey, info core.MediaInfo, original string, scope frontend.TrackScope, selected TrackSelection, scopedError func(error)) (TrackSelection, error) {
	if p.Source == nil || viewer == nil {
		return selected, nil
	}
	if original == "" && key.Kind() == core.KindEpisode && p.GetTitle != nil {
		if show, err := p.GetTitle(ctx, core.ShowKey(key.TMDB())); err == nil && show != nil {
			original = show.Language
		}
	}
	var preferred trackprefs.Selection
	var err error
	applied := false
	if scoped, ok := p.Source.(interface {
		SelectScoped(context.Context, int64, core.RatingKey, core.MediaInfo, string, trackprefs.Scope) (trackprefs.Selection, bool, error)
	}); ok {
		preferred, applied, err = scoped.SelectScoped(ctx, viewer.ID, key, info, original, trackprefs.Scope(scope))
		if err != nil {
			if scopedError != nil {
				scopedError(err)
			}
			applied, err = false, nil
		}
	}
	if !applied {
		preferred, err = p.Source.Select(ctx, viewer.ID, key, info, original)
	}
	if err != nil {
		return selected, err
	}
	if !selected.AudioSet {
		selected.Audio, selected.AudioSet = preferred.Audio, true
	}
	if !selected.SubtitleSet {
		subtitle := preferred.Subtitle
		if text := TextForImageSubtitle(info, subtitle); text >= 0 {
			subtitle = text
		}
		selected.Subtitle, selected.SubtitleSet = subtitle, true
	}
	return selected, nil
}

// ScopedLimits applies positive preference ceilings without raising an existing
// request or policy ceiling. Missing scoped support leaves options unchanged.
func (p TrackPreferences) ScopedLimits(ctx context.Context, viewer *core.User, scope frontend.TrackScope, opts core.DecideOptions) (core.DecideOptions, error) {
	if p.Source == nil || viewer == nil {
		return opts, nil
	}
	if scoped, ok := p.Source.(interface {
		ScopedRules(context.Context, int64, trackprefs.Scope) (trackprefs.Rules, bool, error)
	}); ok {
		rules, _, err := scoped.ScopedRules(ctx, viewer.ID, trackprefs.Scope(scope))
		if err != nil {
			return opts, err
		}
		opts.MaxBitrateKbps = trackprefs.LimitBitrate(opts.MaxBitrateKbps, rules.MaxBitrateKbps)
		opts.MaxHeight = trackprefs.LimitBitrate(opts.MaxHeight, rules.MaxHeight)
	}
	return opts, nil
}
