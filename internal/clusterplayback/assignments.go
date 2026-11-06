package clusterplayback

import (
	"context"
	"encoding/json"
	"time"
)

type Assignment struct {
	SessionID        string     `json:"sessionId"`
	SourceNodeID     string     `json:"sourceNodeId,omitempty"`
	EncoderNodeID    string     `json:"encoderNodeId"`
	ControllerNodeID string     `json:"controllerNodeId"`
	DeliveryNodeID   string     `json:"deliveryNodeId,omitempty"`
	Generation       int64      `json:"generation"`
	MovedAt          *time.Time `json:"movedAt,omitempty"`
}

// Assignments reads durable active routes. Delivery has no fixed owner, and
// route creation time is not a last-move observation. Both remain unknown.
func (r *Runtime) Assignments(ctx context.Context) ([]Assignment, error) {
	rows, err := r.o.DB.QueryContext(ctx, `SELECT r.route FROM clusterplayback_routes r JOIN placement_sessions p ON p.session=r.session AND p.generation=r.generation WHERE p.retired=0 ORDER BY r.session LIMIT 201`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Assignment{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var route Route
		if err := json.Unmarshal([]byte(raw), &route); err != nil {
			return nil, err
		}
		out = append(out, Assignment{SessionID: route.Fence.Session, SourceNodeID: route.Source.NodeID, EncoderNodeID: route.Fence.Target.NodeID, ControllerNodeID: route.Fence.Controller.ID, Generation: route.Fence.Generation})
	}
	return out, rows.Err()
}
