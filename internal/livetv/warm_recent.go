package livetv

import "time"

// warmWatchedWindow is how long after a channel was last watched its
// recent-activity entry remains recorded.
const warmWatchedWindow = 2 * time.Hour

// activeWatchGrace bounds direct noteWatchedLocked marks when no session struct
// is attached; closing the last session on a channel clears activeWatched at once.
const activeWatchGrace = 2 * time.Minute

// noteWatchedLocked records that channelID is being watched now; s.mu held.
func (s *Service) noteWatchedLocked(channelID string) {
	now := s.o.Now()
	if s.watched == nil {
		s.watched = map[string]time.Time{}
	}
	if s.activeWatched == nil {
		s.activeWatched = map[string]time.Time{}
	}
	_, existed := s.activeWatched[channelID]
	s.watched[channelID] = now
	s.activeWatched[channelID] = now
	s.wakeMaintenance()
	s.wakeBreakLoop()
	s.wakePersonalLoop()
	if !existed {
		if w, ok := s.o.Graphics.(interface{ WakeUpcoming() }); ok {
			w.WakeUpcoming()
		}
	}
}

// onChannelSessionClosed clears active-viewer demand for channelID as soon as
// no open session or watched broadcast remains on it.
func (s *Service) onChannelSessionClosed(channelID string) {
	if s == nil || channelID == "" {
		return
	}
	s.mu.Lock()
	stillOpen := false
	for _, ss := range s.sessions {
		if ss.info.Channel.ID == channelID {
			stillOpen = true
			break
		}
	}
	s.mu.Unlock()
	if stillOpen || s.broadcasts.hasViewers(channelID) {
		return
	}
	s.mu.Lock()
	delete(s.activeWatched, channelID)
	s.mu.Unlock()
	s.cancelGraphics(channelID)
}

// activeWatchedChannels returns strictly the channels with an active viewer now
// (open session, broadcast with attached viewers, or fresh direct watch mark).
// An enabled channel nobody is watching is never included.
func (s *Service) activeWatchedChannels() map[string]bool {
	now := s.o.Now()
	out := map[string]bool{}
	for id := range s.broadcasts.watchedChannelIDs() {
		out[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.sessions {
		id := ss.info.Channel.ID
		if id == "" {
			continue
		}
		if s.watched == nil {
			s.watched = map[string]time.Time{}
		}
		if s.activeWatched == nil {
			s.activeWatched = map[string]time.Time{}
		}
		s.watched[id] = now
		s.activeWatched[id] = now
		out[id] = true
	}
	for id, at := range s.activeWatched {
		if out[id] {
			continue
		}
		if now.Sub(at) > activeWatchGrace {
			delete(s.activeWatched, id)
			continue
		}
		out[id] = true
	}
	return out
}

func (s *Service) isActivelyWatched(channelID string) bool {
	if channelID == "" {
		return false
	}
	return s.activeWatchedChannels()[channelID]
}

// recentlyWatched is the set of channels playing now or watched within
// warmWatchedWindow; older entries are forgotten.
func (s *Service) recentlyWatched() map[string]bool {
	now := s.o.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.sessions {
		if s.watched == nil {
			s.watched = map[string]time.Time{}
		}
		s.watched[ss.info.Channel.ID] = now
	}
	out := map[string]bool{}
	for id, at := range s.watched {
		if now.Sub(at) > warmWatchedWindow {
			delete(s.watched, id)
			continue
		}
		out[id] = true
	}
	return out
}
