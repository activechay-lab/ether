package core

import (
	"context"
	"sync/atomic"
)

// A caller that probes the winning release before using it (playback's
// Prepare and detail warm-ups) marks its Resolve context WithProbeFollows.
// The resolver then lets a debrid health check hand its byte proof to that
// probe: the check confirms the link and starts the head fetch, and the
// probe's first read joins it. A link that does not serve fails the probe,
// and playback's existing failover opens the next candidate.

type probeFollowsKey struct{}

// WithProbeFollows marks ctx as a Resolve whose caller probes the release.
func WithProbeFollows(ctx context.Context) context.Context {
	return context.WithValue(ctx, probeFollowsKey{}, true)
}

// ProbeFollows reports whether ctx was marked WithProbeFollows.
func ProbeFollows(ctx context.Context) bool {
	v, _ := ctx.Value(probeFollowsKey{}).(bool)
	return v
}

// HealthDeferral records whether a health check handed its proof to the
// caller's probe. A deferred pass is not a verified release: it must not be
// cached as verified or reported as observed.
type HealthDeferral struct{ deferred atomic.Bool }

type healthDeferralKey struct{}

// WithHealthDeferral lets health checks under ctx defer their byte proof.
func WithHealthDeferral(ctx context.Context) (context.Context, *HealthDeferral) {
	d := &HealthDeferral{}
	return context.WithValue(ctx, healthDeferralKey{}, d), d
}

// HealthDeferralFrom returns ctx's recorder, or nil when checks under ctx
// must prove the bytes themselves.
func HealthDeferralFrom(ctx context.Context) *HealthDeferral {
	d, _ := ctx.Value(healthDeferralKey{}).(*HealthDeferral)
	return d
}

// Defer records a deferred check.
func (d *HealthDeferral) Defer() {
	if d != nil {
		d.deferred.Store(true)
	}
}

// Deferred reports whether a check under the recorder was deferred.
func (d *HealthDeferral) Deferred() bool { return d != nil && d.deferred.Load() }
