package subtitles

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
)

// AutomaticPlayback is a detached, effective preference snapshot supplied by
// the authenticated session coordinator. SelectedAudio must be the language of
// the stream actually selected, never inferred from the title's language.
type AutomaticPlayback struct {
	Title                      *core.Title
	Viewer                     int64
	Playing, Manual            bool
	Languages, UnderstoodAudio []string
	SelectedAudio              string
	Preferences                Preferences
	Streams                    []core.StreamInfo
	Release                    string
}

// NeededAutomaticLanguage is conservative when playback or language evidence is
// unknown. Only the first valid preference is considered; suitable embedded
// tracks cover their language without consuming quota.
func NeededAutomaticLanguage(p AutomaticPlayback) string {
	if !p.Playing || p.Manual || p.Title == nil || p.Viewer <= 0 || p.Release == "" || len(p.Languages) == 0 {
		return ""
	}
	switch p.Preferences.Mode {
	case "always":
	case "foreign-only":
		if langtag.Normalize(p.SelectedAudio) == "" || len(p.UnderstoodAudio) == 0 {
			return ""
		}
		for _, lang := range p.UnderstoodAudio {
			if langtag.MatchTier(lang, p.SelectedAudio) > 0 {
				return ""
			}
		}
	default:
		return ""
	}
	if p.Preferences.Style == "forced" {
		return ""
	}
	wanted := ""
	for _, language := range p.Languages {
		if wanted = langtag.Normalize(language); wanted != "" {
			break
		}
	}
	if wanted == "" {
		return ""
	}
	var text []core.StreamInfo
	for _, stream := range p.Streams {
		if stream.Type != "subtitle" {
			continue
		}
		switch strings.ToLower(stream.Codec) {
		case "subrip", "srt", "ass", "ssa", "webvtt", "vtt", "mov_text", "text", "microdvd", "subviewer", "jacosub", "sami", "realtext", "stl", "ttml":
			text = append(text, stream)
		}
	}
	if len(missingForPreferences(&core.User{SubtitleLanguages: []string{wanted}}, text, p.Preferences)) == 0 {
		return ""
	}
	return wanted
}

// AutomaticTarget supplies the live session fence. Current must freshly check
// viewer authorization, effective rules, source revision and manual choices.
// Attach must atomically repeat those checks while publishing the actual track;
// a check-then-write implementation is not sufficient. The caller runs this
// worker off the timeline response, with the exact source's cancellation context.
type AutomaticTarget struct {
	Current func(context.Context) (AutomaticPlayback, error)
	Attach  func(context.Context, AutomaticPlayback, core.StreamInfo) error
}

// RunAutomatic handles one eligible playback attempt. It is intentionally not
// wired to Enrich: preparation and metadata must remain cache-only. This method
// does not establish client hot-add support or schedule itself.
func (s *Service) RunAutomatic(ctx context.Context, budget *AutomaticBudget, target AutomaticTarget) error {
	if s == nil || target.Current == nil || target.Attach == nil || !s.openSubtitlesEnabled() {
		return nil
	}
	p, err := target.Current(ctx)
	if err != nil {
		return err
	}
	lang := NeededAutomaticLanguage(p)
	if lang == "" {
		return nil
	}
	attach := func(st core.StreamInfo) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return target.Attach(ctx, p, st)
	}
	// Only a source-compatible cache entry is automatically selected.
	if sel, ok := s.loadSelection(p.Title, lang, p.Release, p.Preferences); ok && selectionSatisfies(sel, p.Preferences) {
		return attach(sidecarStream(0, lang, sel))
	}
	attempt, ok, err := budget.Begin(ctx, p.Viewer, p.Title.Key, s.now())
	if err != nil || !ok {
		s.log.Info("automatic subtitle", "viewer", p.Viewer, "key", p.Title.Key, "status", "reservation_or_backoff")
		return err
	}
	finish := func(status string) {
		// Context cancellation must not prevent recording an outcome. The spent
		// bit already persisted; this short write is diagnostic, never a refund.
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), automaticStatusTimeout)
		defer cancel()
		_ = budget.Finish(finishCtx, attempt, status)
		s.log.Info("automatic subtitle", "viewer", p.Viewer, "key", p.Title.Key, "status", status)
	}
	ctx = context.WithValue(ctx, automaticHTTPKey{}, budget)
	hash, _ := s.lookupVideoHash(p.Title, p.Release)
	candidates, err := s.SearchWithHash(ctx, p.Title, lang, p.Release, hash)
	if err != nil {
		finish("error")
		return err
	}
	var chosen *Candidate
	for _, c := range candidates {
		// Automatic choices require affirmative release compatibility. A high
		// popularity score alone must not spend quota on a different edition.
		if !c.HashMatch && releaseSig(c.Release) != releaseSig(p.Release) {
			continue
		}
		if langtag.MatchTier(lang, c.Language) == 0 || !selectionSatisfies(selection{Forced: c.Forced, SDH: c.HearingImp}, p.Preferences) {
			continue
		}
		copy := c
		chosen = &copy
		break
	}
	if chosen == nil {
		finish("no_match")
		return nil
	}
	current, err := target.Current(ctx)
	if err != nil {
		finish("cancelled")
		return err
	}
	if current.Viewer != p.Viewer || current.Title == nil || current.Title.Key != p.Title.Key || current.Release != p.Release || NeededAutomaticLanguage(current) != lang || current.Preferences != p.Preferences {
		finish("cancelled")
		return core.ErrSourceChanged
	}
	id := cacheID(p.Title, lang, chosen.FileID)
	if !s.cached(id, "srt") || !s.cached(id, "vtt") {
		ok, err = budget.Spend(ctx, attempt, s.now())
		if err != nil || !ok {
			finish("cancelled")
			return err
		}
		if _, err = s.fetchAndCache(ctx, id, *chosen, p.Title.Key); err != nil {
			if errors.Is(err, ErrQuota) {
				finish("quota")
			} else {
				finish("error")
			}
			return err
		}
	}
	sel := selection{ProviderTitle: "OpenSubtitles", ID: id, FileID: chosen.FileID, Language: lang, Release: chosen.Release, At: s.now().UTC(), Forced: chosen.Forced, SDH: chosen.HearingImp, Preference: prefSig(p.Preferences)}
	path := s.indexPath(p.Title, lang, p.Release, p.Preferences)
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		finish("error")
		return err
	}
	raw, _ := json.Marshal(sel)
	s.pickMu.Lock()
	err = writeFileAtomic(path, raw)
	s.pickMu.Unlock()
	if err != nil {
		finish("error")
		return err
	}
	if err = attach(sidecarStream(0, lang, sel)); err != nil {
		finish("cancelled")
		return err
	}
	finish("attached")
	return nil
}
