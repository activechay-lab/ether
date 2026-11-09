package newznab

import (
	"sync"
	"time"
)

// Event records one indexer's observed operation; no URLs or keys are exposed.
type Event struct {
	Indexer         string
	Kind            string
	Purpose         string
	Success         bool
	Empty           bool // network search succeeded with zero releases
	Cached          bool // answered from Ether's cache without an HTTP request
	BudgetExhausted bool // held back locally by Ether's budget or rate-limit pause
	SpeculativeHeld bool // held back specifically by the background speculative share
	RateLimited     bool // remote indexer answered HTTP 429 or quota error to a sent request
	Results         int
	Duration        time.Duration
	AverageAge      time.Duration
	At              time.Time
}
type eventBus struct {
	mu   sync.RWMutex
	next int
	subs map[int]func(Event)
}

func (m *Manager) Subscribe(fn func(Event)) func() {
	m.events.mu.Lock()
	if m.events.subs == nil {
		m.events.subs = map[int]func(Event){}
	}
	id := m.events.next
	m.events.next++
	m.events.subs[id] = fn
	m.events.mu.Unlock()
	return func() { m.events.mu.Lock(); delete(m.events.subs, id); m.events.mu.Unlock() }
}
func (m *Manager) emitEvent(e Event) {
	m.events.mu.RLock()
	fns := make([]func(Event), 0, len(m.events.subs))
	for _, fn := range m.events.subs {
		fns = append(fns, fn)
	}
	m.events.mu.RUnlock()
	for _, fn := range fns {
		func() {
			defer func() {
				if v := recover(); v != nil {
					m.log.Warn("indexer subscriber panicked", "recover", v)
				}
			}()
			fn(e)
		}()
	}
}
