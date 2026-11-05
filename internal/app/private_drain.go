package app

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/clusterconfig"
	"github.com/braedonsaunders/ether/internal/sessions"
)

func (a *App) distributedHLS() sessions.DistributedHLS {
	if a.deployment == nil {
		return nil
	}
	runtime, _ := a.deployment.playback.(sessions.DistributedHLS)
	return runtime
}
func (a *App) privatePending(ctx context.Context) (int, error) {
	rt, ok := a.deployment.playback.(interface {
		Pending(context.Context) (int, error)
	})
	if !ok {
		return -1, errors.New("retirement accounting unavailable")
	}
	pending, err := rt.Pending(ctx)
	if a.playback != nil {
		pending += len(a.playback.Sessions())
	}
	return pending, err
}
func (a *App) pollDrain(ctx context.Context) {
	req, err := clusterconfig.ReadDrainControl(a.opts.DataDir, clusterconfig.DrainRequestFile)
	if err != nil || req.Boot != a.cluster.Self().Incarnation || req.Operation == "" || req.State != "requested" {
		return
	}
	a.draining.Store(true)
	if a.playback != nil {
		a.playback.SetDraining(true)
	}
	pending, err := a.privatePending(ctx)
	req.Pending = pending
	req.State = "draining"
	if err == nil && pending == 0 {
		req.State = "drained"
	}
	_ = clusterconfig.WriteDrainControl(a.opts.DataDir, clusterconfig.DrainStatusFile, req)
}

// drainPrivate keeps membership, TLS, existing-media handlers and independent
// Tick/cleanup alive. Deadline ends waiting, never proves ownership retirement.
func (a *App) drainPrivate() {
	a.draining.Store(true)
	if a.playback != nil {
		a.playback.SetDraining(true)
	}
	a.stepDownForShutdown()
	deadline := time.NewTimer(time.Duration(a.deployment.settings.DrainSeconds) * time.Second)
	defer deadline.Stop()
	expired := false
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		op, done := context.WithTimeout(context.Background(), time.Second)
		pending, err := a.privatePending(op)
		done()
		if err == nil && pending == 0 {
			return
		}
		select {
		case <-deadline.C:
			if !expired {
				a.log.Warn("private runtime drain deadline reached; keeping membership and cleanup alive with unresolved ownership")
				expired = true
			}
		case <-tick.C:
		}
	}
}
