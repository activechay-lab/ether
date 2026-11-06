package app

import (
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/hoststat"
	"github.com/braedonsaunders/ether/internal/placement"
)

// Runtime observations are local to this exact process. They are not membership
// heartbeats, remote readiness observations or assignment/retirement evidence.
type selfRuntimeStatus struct {
	NodeID           string                 `json:"nodeId"`
	Boot             string                 `json:"boot"`
	RuntimeSampledAt time.Time              `json:"runtimeSampledAt"`
	Protocol         uint32                 `json:"protocol"`
	Started          bool                   `json:"started"`
	ReadyExisting    bool                   `json:"readyExisting"`
	AcceptingNew     bool                   `json:"acceptingNew"`
	Draining         bool                   `json:"draining"`
	Quota            *runtimeQuotaSnapshot  `json:"quota,omitempty"`
	Resources        *hoststat.NodeSnapshot `json:"resources,omitempty"`
}

type runtimeQuotaSnapshot struct {
	SampledAt time.Time             `json:"sampledAt"`
	Status    placement.QuotaStatus `json:"status"`
}

func (a *App) privateAdmissionReady() bool {
	p := a.deployment
	return p != nil && p.ready.Load() && p.quotaOpen.Load() && !a.nodeDraining()
}

func (p *deploymentRuntime) quotaStatus() *placement.QuotaStatus {
	if q := p.quota.Load(); q != nil {
		status := q.Status
		return &status
	}
	return nil
}

// self is the SAME captured local identity emitted by clusterHandler. Nothing
// is inferred from Nodes or from a caller-supplied node/boot. Cached telemetry
// keeps each metric's original At/State; response time never refreshes its age.
func (a *App) selfRuntimeSnapshot(self cluster.Node) *selfRuntimeStatus {
	p := a.deployment
	if p == nil || self.ID == "" || self.Incarnation == "" {
		return nil
	}
	st := &selfRuntimeStatus{NodeID: self.ID, Boot: self.Incarnation, RuntimeSampledAt: time.Now().UTC(), Protocol: p.protocol.Load(), Started: p.started.Load(), ReadyExisting: p.ready.Load(), AcceptingNew: a.privateAdmissionReady(), Draining: a.nodeDraining()}
	if q := p.quota.Load(); q != nil {
		copy := *q
		st.Quota = &copy
	}
	if p.telemetry != nil {
		resources := p.telemetry.Snapshot()
		st.Resources = &resources
	}
	return st
}

// No membership call here: this also runs while the heartbeat owns its lock.
func (a *App) nodeDiagnostics() *cluster.Diagnostics {
	p := a.deployment
	if p == nil {
		return nil
	}
	d := &cluster.Diagnostics{SampledAt: time.Now().UTC(), ReadyExisting: p.ready.Load(), AcceptingNew: a.privateAdmissionReady(), Draining: a.nodeDraining()}
	if loss := p.readinessLoss.Load(); loss != nil {
		d.ReadinessReason = string(loss.reason)
	}
	if p.settings.Roles.Has("transcode") && !p.settings.Roles.FullServer() {
		capacity := p.settings.Capacity
		d.TranscodeCapacity = &capacity
		if p.encoder != nil {
			used := p.encoder.EncodingCount()
			d.Transcodes = &used
			d.Codecs = append([]string(nil), p.encoder.HWAccel().Encoders...)
		}
	}
	return d
}
