package core

import (
	"net/http"
	"net/url"
)

// SessionPolicy describes adapter-owned session semantics. Identity is never a
// substitute for these capabilities; callers supply policies at construction.
type SessionPolicy struct {
	// ExternalAdmission keeps adapter-owned source admission, public session IDs,
	// progress identities and encoder lifetime rules.
	ExternalAdmission bool
	// DistributedBindings enables durable broker HLS bindings and item telemetry.
	DistributedBindings bool
	// MasterMetadata emits HLS version and verified independent-segment tags.
	MasterMetadata bool
	// PlainSourceGone uses the adapter plain-text missing-source response.
	PlainSourceGone bool
}

// DeliveryPolicy supplies the frontend representation and credential envelope.
type DeliveryPolicy struct {
	Route         func(string) (canonical, id, protocol string, ok bool)
	Session       func(*url.URL) (string, bool)
	GrantEnvelope func(*http.Request) bool
	Headers       []string
}
