package plex

import (
	"context"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

type shelfCut struct {
	at    time.Time
	items []core.Title
}

// A Plex shelf has no continuation token. Retain its filtered, section-specific
// ordered candidate window across the initial response and subsequent pages.
func (s *Server) shelfItems(ctx context.Context, pr *Request, h core.Hub, hc hubCtx, build func() []core.Title) []core.Title {
	if hc.section == 0 {
		return build()
	}
	rev := experience.HomeRevision{}
	if src := s.d.HomeCache; src != nil && src.Revision != nil {
		var err error
		rev, err = src.Revision(ctx, pr.User)
		if err != nil {
			return build()
		}
	}
	hard := fmt.Sprintf("%+v", pr.User)
	if s.d.HomeCache != nil {
		hard, _ = s.homeStamps(pr, rev)
	}
	key := fmt.Sprintf("%s|%s|%d|%d|%s|%s", hard, pr.ClientID, hc.section, hc.related, h.ID, pr.Language)
	s.shelfMu.Lock()
	now := s.now()
	if cut, ok := s.shelfCuts[key]; ok && now.Sub(cut.at) < 5*time.Minute && !now.Before(cut.at) {
		items := append([]core.Title(nil), cut.items...)
		s.shelfMu.Unlock()
		return items
	}
	s.shelfMu.Unlock()
	items := build()
	s.shelfMu.Lock()
	defer s.shelfMu.Unlock()
	if cut, ok := s.shelfCuts[key]; ok && now.Sub(cut.at) < 5*time.Minute {
		return append([]core.Title(nil), cut.items...)
	}
	if s.shelfCuts == nil {
		s.shelfCuts = map[string]shelfCut{}
	}
	for k, cut := range s.shelfCuts {
		if now.Sub(cut.at) >= 5*time.Minute {
			delete(s.shelfCuts, k)
		}
	}
	s.shelfCuts[key] = shelfCut{now, append([]core.Title(nil), items...)}
	return items
}
