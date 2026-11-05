package livetv

import (
	"context"
	"sync"
	"time"
)

// SourceProgress is the owner-visible progress of one manual source import.
// Read counts bytes the panel has sent, so a multi-gigabyte guide can be
// watched while it downloads.
type SourceProgress struct {
	Phase           string    `json:"phase"` // starting, playlist, guide, saving
	Read            int64     `json:"read"`
	Total           int64     `json:"total,omitempty"`
	Channels        int       `json:"channels,omitempty"`
	Programmes      int       `json:"programmes,omitempty"`
	SavedChannels   int       `json:"savedChannels,omitempty"`
	SavedProgrammes int       `json:"savedProgrammes,omitempty"`
	Started         time.Time `json:"started"`
	Updated         time.Time `json:"updated"`
}

type progressTracker struct {
	mu  sync.Mutex
	p   SourceProgress
	now func() time.Time
}

func newProgressTracker(now func() time.Time) *progressTracker {
	at := now()
	return &progressTracker{p: SourceProgress{Phase: "starting", Started: at, Updated: at}, now: now}
}

func (t *progressTracker) setPhase(phase string) {
	t.mu.Lock()
	t.p.Phase = phase
	// Read, Total and the saved counts describe the step in flight.
	t.p.Read, t.p.Total = 0, 0
	t.p.SavedChannels, t.p.SavedProgrammes = 0, 0
	t.p.Updated = t.now()
	t.mu.Unlock()
}

func (t *progressTracker) setTotal(total int64) {
	if total <= 0 {
		return
	}
	t.mu.Lock()
	t.p.Total = total
	t.mu.Unlock()
}

func (t *progressTracker) addRead(n int64) {
	if n <= 0 {
		return
	}
	t.mu.Lock()
	t.p.Read += n
	t.p.Updated = t.now()
	t.mu.Unlock()
}

func (t *progressTracker) setChannels(n int) {
	if n < 0 {
		return
	}
	t.mu.Lock()
	t.p.Channels = n
	t.p.Updated = t.now()
	t.mu.Unlock()
}

func (t *progressTracker) setProgrammes(n int) {
	if n < 0 {
		return
	}
	t.mu.Lock()
	t.p.Programmes = n
	t.p.Updated = t.now()
	t.mu.Unlock()
}

func (t *progressTracker) setSavedChannels(n int) {
	if n < 0 {
		return
	}
	t.mu.Lock()
	t.p.SavedChannels = n
	t.p.Updated = t.now()
	t.mu.Unlock()
}

func (t *progressTracker) setSavedProgrammes(n int) {
	if n < 0 {
		return
	}
	t.mu.Lock()
	t.p.SavedProgrammes = n
	t.p.Updated = t.now()
	t.mu.Unlock()
}

func (t *progressTracker) snapshot() SourceProgress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.p
}

type progressKey struct{}

func withImportProgress(ctx context.Context, t *progressTracker) context.Context {
	return context.WithValue(ctx, progressKey{}, t)
}

func importProgress(ctx context.Context) *progressTracker {
	t, _ := ctx.Value(progressKey{}).(*progressTracker)
	return t
}

// beginImportProgress registers a manual import's tracker; the owner API's
// progress query names it. Automatic refreshes have no tracker.
func (s *Service) beginImportProgress(id string) *progressTracker {
	t := newProgressTracker(s.o.Now)
	s.progressMu.Lock()
	if s.progress == nil {
		s.progress = map[string]*progressTracker{}
	}
	s.progress[id] = t
	s.progressMu.Unlock()
	return t
}

func (s *Service) endImportProgress(id string) {
	s.progressMu.Lock()
	delete(s.progress, id)
	s.progressMu.Unlock()
}

// ImportProgress returns a live snapshot for the owner endpoint.
func (s *Service) ImportProgress(id string) (SourceProgress, bool) {
	s.progressMu.Lock()
	t := s.progress[id]
	s.progressMu.Unlock()
	if t == nil {
		return SourceProgress{}, false
	}
	return t.snapshot(), true
}
