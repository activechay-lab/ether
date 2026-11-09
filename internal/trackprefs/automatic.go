package trackprefs

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

// AutomaticSubtitleRules uses the same scoped rules, inherited language order
// and remembered choice as SelectScoped. The bool is true for any remembered
// manual subtitle selection, including off, even if that track is now missing.
func (s *Service) AutomaticSubtitleRules(ctx context.Context, user int64, key core.RatingKey, original string, scope Scope) (Rules, []string, bool, error) {
	r, _, err := s.ScopedRules(ctx, user, scope)
	if err != nil {
		return Rules{}, nil, false, err
	}
	_, manual, err := s.choice(ctx, user, key, "subtitle")
	if err != nil {
		return Rules{}, nil, false, err
	}
	r = s.sharedLanguages(ctx, user, r)
	server := s.serverLanguage()
	understood := viewerLanguages(r.AudioLanguages, server)
	r.SubtitleLanguages = subtitleOrder(r, server, original)
	return r, understood, manual, nil
}
