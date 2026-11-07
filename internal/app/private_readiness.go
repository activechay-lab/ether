package app

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodewire"
)

// The first degraded decision, actual readiness loss (if grace expires), and
// restoration are logged once each. Reason changes preserve the first failure.
// These diagnostics describe the first failed decision in an unavailable
// interval. They are never consulted by readiness, admission or authority.
type privateReadinessReason string

const (
	readinessCluster     privateReadinessReason = "cluster_mismatch"
	readinessMissing     privateReadinessReason = "self_missing"
	readinessBoot        privateReadinessReason = "boot_mismatch"
	readinessStale       privateReadinessReason = "age_at_or_above_maximum"
	readinessNotLive     privateReadinessReason = "not_live"
	readinessRoles       privateReadinessReason = "invalid_roles"
	readinessPool        privateReadinessReason = "set_pool"
	readinessTarget      privateReadinessReason = "set_target"
	readinessRuntime     privateReadinessReason = "runtime_not_ready"
	readinessPublic      privateReadinessReason = "public_not_ready"
	readinessPublication privateReadinessReason = "capability_not_published"
	readinessListener    privateReadinessReason = "private_listener_failed"
	readinessIdentity    privateReadinessReason = "mounted_identity_changed"
	readinessRotation    privateReadinessReason = "credential_rotation_failed"
	readinessClosed      privateReadinessReason = "private_closed"
	readinessQuota       privateReadinessReason = "initial_quota_read"
)

type privateReadinessSnapshot struct {
	ready        bool
	reason       privateReadinessReason
	at           time.Time
	age          time.Duration
	hasHeartbeat bool
	deadline     time.Time
	errorClass   string
}

// No error text, SQL state/code or identity is retained. Driver error interfaces
// classify SQL failures without exposing their potentially sensitive messages.
func privateReadinessError(err error) string {
	var state interface{ SQLState() string }
	var code interface{ Code() int }
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "cancel"
	case errors.As(err, &state), errors.As(err, &code), errors.Is(err, sql.ErrConnDone), errors.Is(err, sql.ErrTxDone), errors.Is(err, sql.ErrNoRows):
		return "sql"
	default:
		return "other"
	}
}

func (a *App) recordPrivateReadiness(s privateReadinessSnapshot) {
	p := a.deployment
	if p == nil || a.log == nil {
		return
	}
	if s.reason == "" {
		if previous := p.readinessLoss.Swap(nil); previous != nil {
			a.log.Info("private readiness restored", "component", "private-runtime", "role", privateReadinessRole(p.settings.Roles), "previous_reason", previous.reason, "evaluated_at", time.Now().UTC())
		}
		return
	}
	if s.at.IsZero() {
		s.at = time.Now()
	}
	if s.errorClass == "" {
		s.errorClass = "none"
	}
	s.ready = p.ready.Load()
	if previous := p.readinessLoss.Load(); previous != nil {
		if !previous.ready || s.ready {
			return
		}
		// Keep the first failure while recording that grace has now expired.
		s = *previous
		s.ready = false
		if !p.readinessLoss.CompareAndSwap(previous, &s) {
			return
		}
	} else if !p.readinessLoss.CompareAndSwap(nil, &s) {
		return
	}
	stage := "lifecycle"
	switch s.reason {
	case readinessCluster, readinessMissing, readinessBoot, readinessStale, readinessNotLive, readinessRoles:
		stage = "membership"
	case readinessQuota:
		stage = "quota"
	case readinessPool:
		stage = "placement_pool"
	case readinessTarget:
		stage = "placement_target"
	}
	attrs := []any{"component", "private-runtime", "stage", stage, "reason", s.reason, "role", privateReadinessRole(p.settings.Roles), "evaluated_at", s.at.UTC(), "error_category", s.errorClass, "ready_existing", s.ready}
	if !s.deadline.IsZero() {
		attrs = append(attrs, "context_deadline", s.deadline.UTC())
	}
	if s.hasHeartbeat {
		attrs = append(attrs, "heartbeat_age_monotonic", s.age, "heartbeat_age_max_exclusive", clusterLiveAfter, "readiness_grace", privateReadinessGrace)
	}
	if s.ready {
		a.log.Warn("private readiness degraded; existing service retained", attrs...)
	} else {
		a.log.Warn("private readiness unavailable", attrs...)
	}
}

func privateReadinessRole(roles config.NodeRoles) string {
	if roles.FullServer() {
		return "full_server"
	}
	if len(roles) == 1 {
		switch roles[0] {
		case config.RoleServer, config.RoleSource, config.RoleDelivery, config.RoleTranscode, config.RoleBackground:
			return string(roles[0])
		}
	}
	return "mixed_or_unknown"
}

// privateSelfReady is deliberately separate from privateMember. Self readiness
// observes an already joined local process, not remote authority. DB timestamps
// lose Go's monotonic component and must not be subtracted from the local clock
// to decide whether to remove a healthy delivery endpoint.
func (a *App) privateSelfReady(id nodewire.Identity, observation *privateReadinessSnapshot) error {
	self := a.cluster.Self()
	observation.at = time.Now()
	switch {
	case id.Cluster != a.cfg.Get().Server.MachineIdentifier:
		observation.reason = readinessCluster
	case self.ID != id.NodeID:
		observation.reason = readinessMissing
	case self.Incarnation != id.Boot:
		observation.reason = readinessBoot
	case self.Caps.Roles.Validate() != nil:
		observation.reason = readinessRoles
	default:
		if local, ok := a.cluster.(interface{ HeartbeatAge() (time.Duration, bool) }); ok {
			age, owned := local.HeartbeatAge()
			observation.age, observation.hasHeartbeat = age, owned
			if !owned {
				observation.reason = readinessNotLive
			} else if age >= clusterLiveAfter {
				observation.reason = readinessStale
			} else {
				return nil
			}
		} else if a.cluster.Mode() == cluster.ModeSolo {
			return nil
		} else {
			observation.reason = readinessNotLive
		}
	}
	return nodewire.ErrDenied
}

// A failed placement transaction or a late heartbeat must not immediately
// withdraw existing media. This monotonic grace is exclusively a routing
// decision; it grants no placement, renewal, source or delivery authority.
const privateReadinessGrace = 20 * time.Second

type privateReadinessWindow struct {
	failedAt    time.Time
	established bool
}

func (w *privateReadinessWindow) ready(now time.Time, reconciled, services bool, reason privateReadinessReason) bool {
	if !services {
		w.established = false
		w.failedAt = time.Time{}
		return false
	}
	if reconciled {
		w.established = true
		w.failedAt = time.Time{}
		return true
	}
	switch reason {
	case readinessStale, readinessPool, readinessTarget:
		if w.established {
			if w.failedAt.IsZero() {
				w.failedAt = now
			}
			return now.Sub(w.failedAt) < privateReadinessGrace
		}
	}
	w.established = false
	return false
}
