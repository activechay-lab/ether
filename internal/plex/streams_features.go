package plex

import (
	"context"
	"io"
	"net/http"
	"sort"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/reqmemo"
	"github.com/braedonsaunders/ether/internal/plex/model"
	"github.com/braedonsaunders/ether/internal/sessions"
)

// TrackPreferences is optional. Selections are stream slice positions; saved
// show choices match language/flags so they survive changes in release order.
type TrackPreferences = frontend.PlaybackTrackPreferences
type MarkerProvider = frontend.MarkerRanges

func (s *Server) trackScope(ctx context.Context, key core.RatingKey) frontend.TrackScope {
	scope := frontend.TrackScope{Device: FromContext(ctx).ClientID}
	wanted := core.TypeMovie
	if key.Kind() == core.KindShow || key.Kind() == core.KindSeason || key.Kind() == core.KindEpisode {
		wanted = core.TypeShow
		scope.Series = strconv.FormatInt(int64(core.ShowKey(key.TMDB())), 10)
	}
	for _, section := range s.LibrarySections() {
		if section.Type == wanted {
			scope.Library = strconv.Itoa(section.ID)
			break
		}
	}
	return scope
}

func (s *Server) trackPreferences() experience.TrackPreferences {
	return experience.TrackPreferences{Source: s.d.TrackPreferences, GetTitle: func(ctx context.Context, key core.RatingKey) (*core.Title, error) {
		if s.d.Catalog == nil {
			return nil, core.ErrNotAvailable
		}
		return &core.Title{Key: key, Language: s.showLanguage(ctx, key)}, nil
	}}
}
func (s *Server) preferredSelection(ctx context.Context, u *core.User, key core.RatingKey, info core.MediaInfo, original string, sel selection) selection {
	if s.d.TrackPreferences == nil || u == nil {
		return sel
	}
	selected, err := s.trackPreferences().SelectScoped(ctx, u, key, info, original, s.trackScope(ctx, key), experience.TrackSelection{Audio: sel.audio, AudioSet: sel.audioSet, Subtitle: sel.subtitle, SubtitleSet: sel.subtitleSet}, func(err error) { s.log.Warn("scoped stream preferences unavailable", "error", err) })
	if err != nil {
		s.log.Warn("stream preferences unavailable", "error", err)
		return sel
	}
	sel.audio, sel.audioSet = selected.Audio, selected.AudioSet
	sel.subtitle, sel.subtitleSet = selected.Subtitle, selected.SubtitleSet
	return sel
}
func (s *Server) applyStreamPreferences(ctx context.Context, pr *Request, p *playRequest, t *core.Title, info core.MediaInfo) {
	sel := selection{audio: p.audioSel, audioSet: p.audioSel >= 0, subtitle: p.subSel, subtitleSet: p.subSet}
	sel = s.preferredSelection(ctx, pr.User, p.key, info, t.Language, sel)
	p.audioSel = sel.audio
	p.subSel, p.subSet = sel.subtitle, sel.subtitleSet
	if s.d.TrackPreferences != nil && pr.User != nil {
		prefs := s.trackPreferences()
		if filters, err := prefs.AudioFilters(ctx, pr.User, info, experience.TrackSelection{Audio: p.audioSel}); err == nil {
			p.opts.AudioFilters = filters
		}
		scope := s.trackScope(ctx, p.key)
		if pr.ClientID != "" {
			scope.Device = pr.ClientID
		}
		if opts, err := prefs.ScopedLimits(ctx, pr.User, scope, p.opts); err == nil {
			p.opts = opts
		}
	}
}
func (s *Server) rememberStreamRequest(r *http.Request, user *core.User, key core.RatingKey) {
	if s.d.TrackPreferences == nil || user == nil {
		return
	}
	for _, kind := range []string{"audio", "subtitle"} {
		q := r.URL.Query()
		v := q.Get(kind + "StreamID")
		if !q.Has(kind + "StreamID") {
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			if kind == "audio" {
				continue
			}
			id = 0 // A malformed subtitle selector means None.
		}
		if id == -1 {
			if resetter, ok := s.d.TrackPreferences.(interface {
				ForgetChoice(context.Context, int64, core.RatingKey, string) error
			}); ok {
				if err := resetter.ForgetChoice(r.Context(), user.ID, key, kind); err != nil {
					s.log.Warn("reset stream preference", "error", err)
				}
			}
			continue
		}
		var track *core.StreamInfo
		if kind == "subtitle" && id == 0 {
		} else {
			pr := FromContext(r.Context())
			binding, rerr := s.resolveIssuedStream(sessions.WithPlexServe(r.Context(), pr.Remote, ""), user, pr.ClientID, id)
			if rerr != nil {
				if !streamMiss(rerr) {
					return
				}
				continue
			}
			if binding.Key != key || binding.Info.Type != kind {
				continue
			}
			track = &binding.Info
		}
		if err = s.d.TrackPreferences.Remember(r.Context(), user.ID, key, kind, track); err != nil {
			s.log.Warn("save stream preference", "error", err)
		}
	}
}
func (s *Server) addStreamFeatures(ctx context.Context, pr *Request, t *core.Title, m *model.Metadata) {
	info, _ := s.mediaInfoFor(ctx, pr, t)
	s.addProbedStreamFeatures(ctx, t.Key, info, m)
}
func (s *Server) addProbedStreamFeatures(ctx context.Context, key core.RatingKey, info core.MediaInfo, m *model.Metadata) {
	if info.Estimated {
		return
	}
	if info.Duration > 0 {
		m.Duration = info.Duration.Milliseconds()
	}
	partID := int64(key)
	if len(m.Media) > 0 && len(m.Media[0].Part) > 0 {
		partID = m.Media[0].Part[0].ID
	}
	for i, c := range info.Chapters {
		if c.Start < 0 || c.End <= c.Start || c.End > info.Duration {
			continue
		}
		m.Chapter = append(m.Chapter, model.Chapter{ID: tagID("chapter", strconv.FormatInt(partID, 10)+"/"+strconv.Itoa(i)+"/"+strconv.FormatInt(c.Start.Milliseconds(), 10)+"/"+strconv.FormatInt(c.End.Milliseconds(), 10)), Index: i + 1, Title: c.Title, StartTimeOffset: c.Start.Milliseconds(), EndTimeOffset: c.End.Milliseconds()})
	}
	if len(m.Chapter) > 0 {
		m.ChapterSource = "media"
	}
	ranges, err := experience.PlaybackMarkerRanges(ctx, s.d.Markers, key, info)
	if err != nil {
		s.log.Debug("marker provider unavailable", "error", err)
	}

	// PMS lists markers in timeline order and puts final="1" only on the
	// last credits marker (a stinger scene splits credits into several).
	sort.SliceStable(ranges, func(i, j int) bool { return ranges[i].StartMS < ranges[j].StartMS })
	lastCredits := -1
	for i, v := range ranges {
		if v.Type == "credits" {
			lastCredits = i
		}
	}
	id := int64(0)
	for i, v := range ranges {
		if v.Type != "intro" && v.Type != "credits" {
			continue
		}
		id++
		m.Marker = append(m.Marker, model.Marker{ID: id, Type: v.Type, StartTimeOffset: v.StartMS, EndTimeOffset: v.EndMS, Final: model.Ptr(v.Final && i == lastCredits)})
	}
}

// SubtitleSource resolves cached OpenSubtitles and synchronized sidecars.
type SubtitleSource interface {
	OpenFormat(string, string) (io.ReadCloser, string, error)
}

func showLanguageMemoKey(show core.RatingKey) string {
	return "plex:show-language:" + strconv.FormatInt(int64(show), 10)
}

// showLanguage is a series' original language, the default an episode's
// track selection falls back to. A read request asks once per show.
func (s *Server) showLanguage(ctx context.Context, show core.RatingKey) string {
	lang, _ := reqmemo.Do(ctx, showLanguageMemoKey(show), func() (string, error) {
		if t, err := s.d.Catalog.Get(ctx, show); err == nil && t != nil {
			return t.Language, nil
		}
		return "", nil
	})
	return lang
}

// preloadShowLanguages resolves, in one batch, the series of every episode
// whose streams this render selects without a language of its own. A list
// of episodes from distinct shows otherwise loaded each show on its own
// (On Deck: hundreds). Shows the batch cannot vouch for are left to Get.
func (s *Server) preloadShowLanguages(ctx context.Context, pr *Request, ts []core.Title, o renderOpts) {
	if !reqmemo.Active(ctx) || s.d.TrackPreferences == nil || s.d.Catalog == nil || pr == nil || pr.User == nil || !o.media || (!o.detail && !o.detailEpisodes) {
		return
	}
	resolver, ok := s.d.Catalog.(interface {
		ResolveShowParents(context.Context, []core.RatingKey) (map[core.RatingKey]core.Title, error)
	})
	if !ok {
		return
	}
	seen := map[core.RatingKey]bool{}
	var shows []core.RatingKey
	for i := range ts {
		if ts[i].Type != core.TypeEpisode || ts[i].Language != "" || ts[i].Key.TMDB() <= 0 {
			continue
		}
		show := core.ShowKey(ts[i].Key.TMDB())
		if !seen[show] {
			seen[show] = true
			shows = append(shows, show)
		}
	}
	if len(shows) < 2 {
		return
	}
	got, err := resolver.ResolveShowParents(ctx, shows)
	if err != nil {
		return
	}
	for key, t := range got {
		lang := t.Language
		_, _ = reqmemo.Do(ctx, showLanguageMemoKey(key), func() (string, error) { return lang, nil })
	}
}

// preloadTrackChoices keeps detailed list rendering from reading each title's
// remembered audio and subtitle choices separately.
func (s *Server) preloadTrackChoices(ctx context.Context, pr *Request, ts []core.Title, o renderOpts) {
	if pr == nil || pr.User == nil || !o.media || (!o.detail && !o.detailEpisodes) {
		return
	}
	batch, ok := s.d.TrackPreferences.(interface {
		PreloadChoices(context.Context, int64, []core.RatingKey) error
	})
	if !ok {
		return
	}
	keys := make([]core.RatingKey, 0, len(ts))
	for _, t := range ts {
		keys = append(keys, t.Key)
	}
	if err := batch.PreloadChoices(ctx, pr.User.ID, keys); err != nil {
		s.log.Warn("preload stream preferences", "err", err)
	}
}
