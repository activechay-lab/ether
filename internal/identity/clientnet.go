package identity

import (
	"github.com/braedonsaunders/ether/internal/frontend/kit/clientnet"
	"net/http"
	"net/netip"
)

// LAN reports whether an address belongs to a LAN or overlay network.
func LAN(ip netip.Addr) bool { return clientnet.LAN(ip) }

// ClientNet applies the shared frontend peer/proxy trust grammar.
func ClientNet(r *http.Request, trusted ...netip.Prefix) (netip.Addr, bool) {
	return clientnet.ClientNet(r, trusted...)
}

// ProxyPrefixes parses configured peer networks; invalid entries grant no trust.
func ProxyPrefixes(cidrs []string) []netip.Prefix { return clientnet.ProxyPrefixes(cidrs) }
