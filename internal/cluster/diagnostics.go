package cluster

import "time"

// Diagnostics are heartbeat-carried observations, never placement authority.
// Missing counters mean unmeasured; zero is reserved for an actual observation.
type Diagnostics struct {
	Clock             *ClockStatus `json:"clock,omitempty"`
	SampledAt         time.Time    `json:"sampledAt"`
	ReadyExisting     bool         `json:"readyExisting"`
	AcceptingNew      bool         `json:"acceptingNew"`
	Draining          bool         `json:"draining"`
	ReadinessReason   string       `json:"readinessReason,omitempty"`
	TranscodeCapacity *int         `json:"transcodeCapacity,omitempty"`
	Transcodes        *int         `json:"transcodes,omitempty"`
	// Source counters describe NNTP provider slots only, excluding gateway and HTTP sockets.
	SourceConnectionCapacity *int     `json:"sourceConnectionCapacity,omitempty"`
	SourceConnections        *int     `json:"sourceConnections,omitempty"`
	DeliveryBytesPerSecond   *int64   `json:"deliveryBytesPerSecond,omitempty"`
	Codecs                   []string `json:"codecs,omitempty"`
}
