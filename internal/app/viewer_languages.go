package app

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// syncLanguages keeps auth_users.audio_languages and subtitle_languages
// aligned with the viewer's ordered lists in trackprefs (Audio & subtitles).
// Track selection and every protocol read the trackprefs lists; the account
// columns stay the same facts for callers that already load them from the
// user row (release language ranking, Plex accounts, failover).
func (a *App) syncLanguages(ctx context.Context, user int64, audio, subtitles []string) error {
	if user <= 0 || a.users == nil {
		return nil
	}
	current, err := a.users.ActiveUser(ctx, user)
	if err != nil {
		return err
	}
	if sameLanguages(current.AudioLanguages, audio) && sameLanguages(current.SubtitleLanguages, subtitles) {
		return nil
	}
	next := *current
	next.AudioLanguages = append([]string(nil), audio...)
	next.SubtitleLanguages = append([]string(nil), subtitles...)
	return a.users.UpdateUser(ctx, next)
}

func sameLanguages(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(strings.TrimSpace(a[i]), strings.TrimSpace(b[i])) {
			return false
		}
	}
	return true
}

// effectiveAudioLanguages is the viewer's ordered audio languages as playback
// resolves them: the stored trackprefs list, which layers the viewer's own
// choice over user 0, the plex.tv account profile and the quality profile.
// The account row is used when the viewer has no trackprefs list. The version
// estimate on a detail page uses this so a release that declares a dub is
// listed in the language the viewer will actually hear.
func (a *App) effectiveAudioLanguages(ctx context.Context, v *core.Viewer) []string {
	if v == nil || v.ID <= 0 {
		return nil
	}
	if a.trackprefs != nil {
		if rules, err := a.trackprefs.GetRules(ctx, v.ID); err == nil && len(rules.AudioLanguages) > 0 {
			return rules.AudioLanguages
		}
	}
	return v.AudioLanguages
}

// withEffectiveAudioLanguages copies the viewer with the resolved audio
// languages so resolver calls rank and estimate against what playback picks.
func (a *App) withEffectiveAudioLanguages(ctx context.Context, v *core.Viewer) *core.Viewer {
	if v == nil {
		return nil
	}
	langs := a.effectiveAudioLanguages(ctx, v)
	if len(langs) == 0 || sameLanguages(langs, v.AudioLanguages) {
		return v
	}
	cp := *v
	cp.AudioLanguages = append([]string(nil), langs...)
	return &cp
}
