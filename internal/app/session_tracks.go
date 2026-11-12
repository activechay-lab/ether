package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/trackprefs"
)

type scopedTrackSource struct {
	*trackprefs.Service
	scope trackprefs.Scope
}

func (s scopedTrackSource) Select(ctx context.Context, viewer int64, key core.RatingKey, info core.MediaInfo, original string) (trackprefs.Selection, error) {
	selection, applied, err := s.Service.SelectScoped(ctx, viewer, key, info, original, s.scope)
	if err != nil || applied {
		return selection, err
	}
	return s.Service.Select(ctx, viewer, key, info, original)
}

// selectBrokerTracks translates wire stream indexes to the experience
// service's slice positions, then translates its result back for the encoder.
func selectBrokerTracks(ctx context.Context, prefs experience.TrackPreferences, viewer *core.Viewer, title *core.Title, info core.MediaInfo, opts core.DecideOptions, audioSet, subtitleSet bool, onError func(error)) (core.DecideOptions, error) {
	position := func(kind string, index int) (int, bool) {
		for i, stream := range info.Streams {
			if stream.Type == kind && stream.Index == index {
				return i, true
			}
		}
		return -1, false
	}
	selection := experience.TrackSelection{Audio: -1, Subtitle: -1, AudioSet: audioSet, SubtitleSet: subtitleSet}
	// An index that names no stream of that kind is no choice, as on real
	// Jellyfin: Streamyfin sends AudioStreamIndex 0 (the video stream) when
	// the item it read had no default audio index. The viewer's preferences
	// pick the track instead of failing the plan as NoCompatibleStream.
	if audioSet && opts.AudioStream >= 0 {
		var ok bool
		if selection.Audio, ok = position("audio", opts.AudioStream); !ok {
			selection.Audio, selection.AudioSet = -1, false
			opts.AudioStream = -1
		}
	}
	if subtitleSet && opts.SubtitleStream >= 0 {
		var ok bool
		if selection.Subtitle, ok = position("subtitle", opts.SubtitleStream); !ok {
			selection.Subtitle, selection.SubtitleSet = -1, false
			opts.SubtitleStream = -1
		}
	}
	selected, err := prefs.Select(ctx, viewer, title.Key, info, title.Language, selection)
	if err != nil && onError != nil {
		onError(err)
	}
	if selected.AudioSet {
		if selected.Audio < 0 {
			opts.AudioStream = -1
		} else if selected.Audio >= len(info.Streams) || info.Streams[selected.Audio].Type != "audio" {
			return opts, core.ErrNotAvailable
		} else {
			opts.AudioStream = info.Streams[selected.Audio].Index
		}
	}
	if selected.SubtitleSet {
		if selected.Subtitle < 0 {
			opts.SubtitleStream = -1
		} else if selected.Subtitle >= len(info.Streams) || info.Streams[selected.Subtitle].Type != "subtitle" {
			return opts, core.ErrNotAvailable
		} else {
			opts.SubtitleStream = info.Streams[selected.Subtitle].Index
		}
	}
	filters, err := prefs.AudioFilters(ctx, viewer, info, selected)
	if err != nil {
		if onError != nil {
			onError(err)
		}
	} else {
		opts.AudioFilters = filters
	}
	return opts, nil
}
