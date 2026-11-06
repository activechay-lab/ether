package clusterplayback

import (
	"context"
	"time"
)

// RecoveryEvent is an observation, never an authority or a retirement receipt.
// Nil impact measurements mean unknown, not zero client interruption.
type RecoveryEvent struct {
	SessionID         string     `json:"sessionId"`
	Role              string     `json:"role"`
	FailedNodeID      string     `json:"failedNodeId"`
	ReplacementNodeID string     `json:"replacementNodeId,omitempty"`
	Generation        int64      `json:"generation"`
	StartedAt         time.Time  `json:"startedAt"`
	CompletedAt       *time.Time `json:"completedAt,omitempty"`
	Status            string     `json:"status"`
	InterruptionMS    *int64     `json:"interruptionMs,omitempty"`
	ClientStalls      *int       `json:"clientStalls,omitempty"`
}

// RecoveryEventReader lets the recovery coordinator supply bounded history.
type RecoveryEventReader interface {
	RecoveryEvents(context.Context, int) ([]RecoveryEvent, error)
}
