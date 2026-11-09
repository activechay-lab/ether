package plex

import (
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/sessions"
	"time"
)

func decisionOwner(pr *Request) sessions.DecisionOwner {
	if pr.User == nil {
		return sessions.DecisionOwner{}
	}
	return sessions.DecisionOwner{ID: trackID(pr.User.ID, pr.ClientID, pr.SessionID), UserID: pr.User.ID, ClientID: pr.ClientID, SessionID: pr.SessionID}
}
func (s *Server) armDecision(pr *Request, key frontend.RatingKey) {
	s.decisionArms.Arm(decisionOwner(pr), key)
}
func (s *Server) decisionArmed(pr *Request, key frontend.RatingKey) bool {
	return s.decisionArms.Armed(decisionOwner(pr), key)
}
func (s *Server) disarmDecision(pr *Request, key frontend.RatingKey) {
	s.decisionArms.Disarm(decisionOwner(pr), key)
}
func (s *Server) refreshDecisionArms(now time.Time) { s.decisionArms.Refresh(now) }
func (s *Server) clearOwnDecisionArms()             { s.decisionArms.ClearOwn() }
