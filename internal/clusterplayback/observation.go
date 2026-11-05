package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
	"time"
)

// ObserveExecution obtains a fresh detached exact-generation backend sample.
// RecoveryBuffer.DeliveredUntil and Goodput describe worker-to-controller output;
// neither proves final viewer delivery. Callers need independent delivery facts.
func (r *Runtime) ObserveExecution(ctx context.Context, id string, maxAge time.Duration) (*remotetranscode.ObservedSession, error) {
	if maxAge <= 0 || maxAge > time.Minute {
		return nil, ErrDenied
	}
	r.mu.Lock()
	x := r.executions[id]
	if x == nil || x.session == nil || x.stopping || x.retired {
		r.mu.Unlock()
		return nil, ErrPending
	}
	session, route := x.session, x.route
	r.mu.Unlock()
	record, e := r.o.Placement.Lookup(ctx, id)
	if e != nil || record.Fence != route.Fence || record.Retired || !record.Authority {
		return nil, ErrDenied
	}
	sample, e := session.Observe(ctx)
	if e != nil {
		return nil, e
	}
	now := time.Now()
	if sample.Identity() != remoteIdentity(route) || now.Sub(sample.ReceivedAt()) > maxAge || sample.ReceivedAt().Sub(sample.RequestStarted()) > maxAge || sample.SampledAt().After(now.Add(r.o.ClockSkew)) || now.Sub(sample.SampledAt()) > maxAge+r.o.ClockSkew {
		return nil, ErrPending
	}
	r.mu.Lock()
	same := r.executions[id] == x && !x.stopping && !x.retired
	r.mu.Unlock()
	if !same {
		return nil, ErrPending
	}
	return sample, nil
}
