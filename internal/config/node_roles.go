package config

import (
	"errors"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// NodeRole is an opt-in responsibility, not a hardware capability. In
// particular transcode still requires the detected cluster.Caps.Transcode.
type NodeRole string

const (
	RoleServer     NodeRole = "server"
	RoleSource     NodeRole = "source"
	RoleTranscode  NodeRole = "transcode"
	RoleDelivery   NodeRole = "delivery"
	RoleBackground NodeRole = "background"
)

// NodeRoles combine responsibilities. Empty preserves the historical full
// server, including all five roles. Roles do not change provider budget weights.
// Treat configured slices as immutable snapshots for a process lifetime.
type NodeRoles []NodeRole

func (r NodeRoles) Has(role NodeRole) bool {
	switch role {
	case RoleServer, RoleSource, RoleTranscode, RoleDelivery, RoleBackground:
		return len(r) == 0 || slices.Contains(r, role)
	default:
		return false
	}
}

func (r NodeRoles) Validate() error {
	seen := make(map[NodeRole]bool, len(r))
	for _, role := range r {
		switch role {
		case RoleServer, RoleSource, RoleTranscode, RoleDelivery, RoleBackground:
		default:
			return errors.New("node roles contain an unknown role")
		}
		if seen[role] {
			return errors.New("node roles contain a duplicate role")
		}
		seen[role] = true
	}
	return nil
}

// FullServer reports whether all historical responsibilities are enabled.
func (r NodeRoles) FullServer() bool {
	return r.Validate() == nil && r.Has(RoleServer) && r.Has(RoleSource) && r.Has(RoleTranscode) && r.Has(RoleDelivery) && r.Has(RoleBackground)
}

// ValidateInternalAddress accepts an explicitly configured HTTPS origin for
// authenticated node transport. Empty means unconfigured, never public fallback.
// Errors deliberately omit the supplied address, which may contain credentials.
func ValidateInternalAddress(address string) error {
	if address == "" {
		return nil
	}
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return errors.New("internal address must be an HTTPS origin without credentials, query or fragment")
	}
	if strings.ContainsAny(u.Hostname(), " \t\r\n") {
		return errors.New("internal address host is invalid")
	}
	if strings.HasPrefix(u.Host, "[") {
		if _, err := netip.ParseAddr(u.Hostname()); err != nil {
			return errors.New("internal address host is invalid")
		}
	} else if strings.Contains(u.Hostname(), ":") {
		return errors.New("internal address IPv6 host must be bracketed")
	}
	if strings.HasSuffix(u.Host, ":") {
		return errors.New("internal address port is invalid")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("internal address port is invalid")
		}
	}
	return nil
}

// ValidateNodeSettings must be called with the effective database (including
// environment overrides) before constructing services. This foundation does not
// itself isolate listeners, provider pools, jobs or remote execution by role.
func (s Server) ValidateNodeSettings(database Database) error {
	if err := s.NodeRoles.Validate(); err != nil {
		return err
	}
	if err := ValidateInternalAddress(s.InternalAddress); err != nil {
		return err
	}
	if !s.NodeRoles.FullServer() && !database.IsPostgres() {
		return errors.New("specialist node roles require PostgreSQL")
	}
	return nil
}
