package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/playbackcontinuity"
	"time"
)

// ReopenResponsibility remains owned on every uncertain or post-acquisition error.
// Retire schedules exact-operation cleanup on Runtime.Tick; no new Prepare occurs.
type ReopenResponsibility struct {
	r                 *Runtime
	owner             nodewire.Identity
	operation         string
	old               playbackcontinuity.SourceBinding
	done              chan struct{}
	retiring, retired bool
	forgetAt          time.Time // forgetTerminal grace deadline
}

func (l *ReopenResponsibility) Done() <-chan struct{} { return l.done }
func (l *ReopenResponsibility) Err() error {
	l.r.mu.Lock()
	defer l.r.mu.Unlock()
	if l.retired {
		return nil
	}
	return ErrPending
}
func (l *ReopenResponsibility) Retire() { l.r.mu.Lock(); l.retiring = true; l.r.mu.Unlock() }
func (r *Runtime) reopenResponsibility(owner nodewire.Identity, old playbackcontinuity.SourceBinding, operation string) (*ReopenResponsibility, error) {
	key := owner.Cluster + "/" + owner.NodeID + "/" + owner.Boot + "/" + operation
	r.mu.Lock()
	defer r.mu.Unlock()
	if l := r.reopenLives[key]; l != nil {
		if l.old != old {
			return nil, ErrPending
		}
		return l, nil
	}
	if r.closed || len(r.reopenLives) >= r.o.HistoryCapacity {
		return nil, ErrDenied
	}
	life := &ReopenResponsibility{r: r, owner: owner, operation: operation, old: old, done: make(chan struct{})}
	r.reopenLives[key] = life
	return life, nil
}
func (r *Runtime) tickReopenLives(ctx context.Context) error {
	type pending struct {
		life     *ReopenResponsibility
		retiring bool
	}
	r.mu.Lock()
	var list []pending
	for _, l := range r.reopenLives {
		if !l.retired {
			list = append(list, pending{life: l, retiring: l.retiring})
		}
	}
	r.mu.Unlock()
	var result error
	for _, item := range list {
		op := "operation-status"
		if item.retiring {
			op = "prepare-retire"
		}
		response, err := r.sourceCall(ctx, Route{Source: item.life.owner}, sourceRequest{Op: op, Prepare: &PrepareRequest{Operation: item.life.operation}})
		if err != nil {
			result = ErrPending
			continue
		}
		if response.Retired || response.Phase == "retired" {
			r.mu.Lock()
			if !item.life.retired {
				item.life.retired = true
				close(item.life.done)
			}
			r.mu.Unlock()
		}
	}
	return result
}
