package app

import (
	"context"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/sessions"
	"github.com/braedonsaunders/ether/internal/subtitles"
	"github.com/braedonsaunders/ether/internal/trackprefs"
)

// automaticSubtitleWork runs only in the shared broker's bounded post-playing
// worker. No prepare/detail/prefetch path calls it.
func (a *App) automaticSubtitleWork(ctx context.Context, b *sessions.Broker, start sessions.PlayingSubtitleSession) {
	if a.subtitles == nil || a.playback == nil || a.trackprefs == nil || a.db == nil || !a.cfg.Read().OpenSubtitles.AutomaticPreferred {
		return
	}
	lease, err := a.playback.LeaseSubtitleSource(ctx, start.SourceID, start.SourceRevision)
	if err != nil {
		return
	}
	defer lease.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(lease.Context, cancel)
	defer stop()
	intents := sessions.NewSubtitleIntents(a.db)
	intent, err := intents.Snapshot(ctx, start.Access.Viewer.ID, start.Item)
	if err != nil || intent.Manual || start.ManualSubtitle {
		return
	}
	current := func(ctx context.Context) (subtitles.AutomaticPlayback, error) {
		if !a.cfg.Read().OpenSubtitles.AutomaticPreferred {
			return subtitles.AutomaticPlayback{}, core.ErrNotAvailable
		}
		live, err := b.CurrentSubtitleSession(ctx, start.Access, start.ID)
		if err != nil {
			return subtitles.AutomaticPlayback{}, err
		}
		if live.SourceID != start.SourceID || live.SourceRevision != start.SourceRevision || live.ManualSubtitle {
			return subtitles.AutomaticPlayback{}, core.ErrSourceChanged
		}
		now, err := intents.Snapshot(ctx, start.Access.Viewer.ID, start.Item)
		if err != nil {
			return subtitles.AutomaticPlayback{}, err
		}
		if now != intent {
			return subtitles.AutomaticPlayback{}, core.ErrSourceChanged
		}
		title, err := a.catalog.Get(ctx, start.Item)
		if err != nil {
			return subtitles.AutomaticPlayback{}, err
		}
		scope := trackprefs.Scope{Device: start.Access.Device.ID}
		if title.ShowKey != 0 {
			scope.Series = strconv.FormatInt(int64(title.ShowKey), 10)
		} else if title.Key.Kind() == core.KindEpisode {
			scope.Series = strconv.FormatInt(int64(core.ShowKey(title.Key.TMDB())), 10)
		}
		if section, ok := brokerSectionFor(func(core.MediaType) (int, bool) { return 0, false })(title.Type); ok {
			scope.Library = strconv.Itoa(section)
		}
		rules, understood, manual, err := a.trackprefs.AutomaticSubtitleRules(ctx, start.Access.Viewer.ID, start.Item, title.Language, scope)
		if err != nil {
			return subtitles.AutomaticPlayback{}, err
		}
		source, ok := a.playback.Session(start.SourceID)
		if !ok || core.SourceRevision(source) != start.SourceRevision {
			return subtitles.AutomaticPlayback{}, core.ErrSourceChanged
		}
		release := ""
		if source.Resolution != nil {
			release = source.Resolution.Release.Name
		}
		audio := ""
		for _, stream := range source.Info.Streams {
			if stream.Type == "audio" && stream.Index == live.Options.AudioStream {
				audio = stream.Language
				break
			}
		}
		return subtitles.AutomaticPlayback{Title: title, Viewer: start.Access.Viewer.ID, Playing: true, Manual: manual, Languages: rules.SubtitleLanguages, UnderstoodAudio: understood, SelectedAudio: audio, Preferences: subtitles.Preferences{Mode: rules.SubtitleMode, Style: rules.SubtitleStyle}, Streams: source.Info.Streams, Release: release}, nil
	}
	selection := "server_selection_unsupported"
	var selectTrack func(core.StreamInfo) error
	if frontendHostPolicy(start.Access.Protocol).AutomaticSubtitleSelection && a.frontends != nil {
		if inst, ok := a.frontends.registry.Instance(start.Access.Protocol); ok {
			if selector, ok := inst.Handler().(interface {
				SelectAutomaticSubtitle(frontend.SessionAccess, core.RatingKey, core.StreamInfo) error
			}); ok {
				selectTrack = func(track core.StreamInfo) error {
					return selector.SelectAutomaticSubtitle(start.Access, start.Item, track)
				}
			} else {
				selection = "frontend_selection_handler_unavailable"
			}
		} else {
			selection = "frontend_not_mounted"
		}
	}
	target := subtitles.AutomaticTarget{Current: current, Attach: func(ctx context.Context, expected subtitles.AutomaticPlayback, track core.StreamInfo) error {
		latest, err := current(ctx)
		if err != nil {
			return err
		}
		if latest.Release != expected.Release || latest.Preferences != expected.Preferences || subtitles.NeededAutomaticLanguage(latest) != subtitles.NeededAutomaticLanguage(expected) {
			return core.ErrSourceChanged
		}
		err = lease.WithPublication(ctx, func(attach func(core.StreamInfo) (core.StreamInfo, error)) error {
			return intents.Publish(ctx, start.Access.Viewer.ID, start.Item, intent, func() error {
				var err error
				track, err = attach(track)
				if err != nil {
					return err
				}
				if selectTrack != nil {
					if err = selectTrack(track); err != nil {
						selection = "server_selection_failed"
					} else {
						selection = "server_selected"
					}
				}
				return nil
			})
		})
		if err != nil {
			return err
		}
		access := subtitles.ReleaseAccess{URL: lease.InputURL, Duration: lease.Session.Info.Duration, Info: &lease.Session.Info, Source: lease.Session.Source.ReleaseID(), Hold: lease.Hold}
		if a.transcoder != nil {
			access.FFmpeg = a.transcoder.FFmpegPath()
		}
		a.subtitles.StartCorrection(lease.Context, latest.Title, track.ExternalURL, access)
		a.log.Info("automatic subtitle attached", "viewer", start.Access.Viewer.ID, "key", start.Item, "protocol", start.Access.Protocol, "stream", track.Index, "selection", selection)
		return nil
	}}
	if err = a.subtitles.RunAutomatic(ctx, subtitles.NewAutomaticBudget(a.db), target); err != nil && ctx.Err() == nil {
		// Provider errors can contain upstream response bodies/URLs. Log only a
		// bounded status; owner details come from the secret-free budget ledger.
		a.log.Warn("automatic subtitle unavailable", "viewer", start.Access.Viewer.ID, "key", start.Item, "status", "error")
	}
}
