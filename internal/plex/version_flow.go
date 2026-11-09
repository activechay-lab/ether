package plex

import (
	"context"
	"database/sql"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/sessions"
	"time"
)

const versionPreplayTTL = sessions.VersionPreplayTTL
const (
	versionRead   = sessions.VersionRead
	versionOffer  = sessions.VersionOffer
	versionFreeze = sessions.VersionFreeze
	versionEnd    = sessions.VersionEnd
	versionTouch  = sessions.VersionTouch
)

type versionSnapshot struct {
	variants []frontend.ReleaseVariant
	frozen   bool
	until    time.Time
}
type versionFlowStore struct{ flows *sessions.VersionFlows }

func newVersionFlowStore(s *Server, db *sql.DB) *versionFlowStore {
	flows := sessions.NewVersionFlows(s.ctx, db)
	s.wg.Add(1)
	go func() { defer s.wg.Done(); flows.Run() }()
	return &versionFlowStore{flows: flows}
}
func (v *versionFlowStore) call(ctx context.Context, k playIdentityKey, vs []frontend.ReleaseVariant, mode int, now time.Time, ttl time.Duration) (versionSnapshot, error) {
	snap, err := v.flows.Call(ctx, sessions.VersionFlowKey{UserID: k.userID, ClientID: k.clientID, Key: k.key}, vs, mode, now, ttl)
	return versionSnapshot{snap.Variants, snap.Frozen, snap.Until}, err
}

func (s *Server) endVersionFlow(ctx context.Context, k playIdentityKey) {
	s.playIDs.mu.Lock()
	if e := s.playIDs.m[k]; e != nil {
		e.frozen = false
		e.resolved = false
		e.variants = nil
		e.flowUntil = time.Time{}
		e.selected = 0
		e.admittedRelease = ""
		e.autoAligned = ""
	}
	s.playIDs.mu.Unlock()
	if s.versionFlows != nil {
		if _, err := s.versionFlows.call(ctx, k, nil, versionEnd, s.now(), 0); err != nil {
			s.log.Warn("end version flow", "err", err)
		}
	}
}
