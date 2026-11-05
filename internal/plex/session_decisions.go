package plex

import (
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

type decisionArm struct {
	items map[core.RatingKey]bool
	at    time.Time
}

// The public player session owns its decision state; the encoder's session=
// value is an independent delivery handle and cannot authorize another player.
func (s *Server) armDecision(pr *Request, key core.RatingKey) {
	if pr.User == nil || key == 0 {
		return
	}
	s.termMu.Lock()
	defer s.termMu.Unlock()
	if s.decisionArms == nil {
		s.decisionArms = make(map[string]decisionArm)
	}
	id := trackID(pr.User.ID, pr.ClientID, pr.SessionID)
	a := s.decisionArms[id]
	if a.items == nil {
		a.items = make(map[core.RatingKey]bool)
	}
	a.items[key] = true
	a.at = s.now()
	s.decisionArms[id] = a
}
func (s *Server) decisionArmed(pr *Request, key core.RatingKey) bool {
	if pr.User == nil {
		return false
	}
	s.termMu.Lock()
	defer s.termMu.Unlock()
	a, ok := s.decisionArms[trackID(pr.User.ID, pr.ClientID, pr.SessionID)]
	if !ok || s.now().Sub(a.at) > 3*time.Minute {
		return false
	}
	a.at = s.now()
	s.decisionArms[trackID(pr.User.ID, pr.ClientID, pr.SessionID)] = a
	return key == 0 || a.items[key]
}
