package app

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/braedonsaunders/ether/internal/identity"
)

// lanOnly keeps the owner UI off the internet: with remote access on, the
// Plex API is reachable through the port forward, but /ether/ answers only
// loopback, private (RFC 1918/4193), link-local and CGNAT (Tailscale)
// clients. Ether binds one port, so this has to be enforced per request.
//
// A client a proxy forwarded from outside is refused whichever machine the
// proxy runs on: forwarding headers naming a public hop deny even from an
// untrusted private peer (they can only lock their sender out, never let
// anyone in). allowRemote is the owner's opt-in ("Open this console from
// outside your network"); the console's own sign-in still admits only the
// owner's Plex account.
func lanOnly(next http.Handler, proxyCIDRs func() []string, allowRemote func() bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowRemote != nil && allowRemote() {
			next.ServeHTTP(w, r)
			return
		}
		var cidrs []string
		if proxyCIDRs != nil {
			cidrs = proxyCIDRs()
		}
		_, local := identity.ClientNet(r, identity.ProxyPrefixes(cidrs)...)
		if !local || forwardedFromOutside(r) {
			http.Error(w, "The Ether console is only available on your home network. To open it from outside, turn on \"Open this console from outside your network\" under Server › General › How apps connect.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// forwardedFromOutside reports a forwarding header that names a public
// address anywhere in its chain.
func forwardedFromOutside(r *http.Request) bool {
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for _, v := range r.Header.Values("Forwarded") {
		for _, element := range strings.Split(v, ",") {
			for _, param := range strings.Split(element, ";") {
				if k, val, ok := strings.Cut(strings.TrimSpace(param), "="); ok && strings.EqualFold(k, "for") {
					val = strings.Trim(strings.TrimSpace(val), "\"")
					if h, _, err := net.SplitHostPort(val); err == nil {
						val = h
					}
					hops = append(hops, val)
				}
			}
		}
	}
	hops = append(hops, r.Header.Values("X-Real-IP")...)
	for _, hop := range hops {
		hop = strings.Trim(strings.TrimSpace(hop), "[]")
		if addr, err := netip.ParseAddr(hop); err == nil && !identity.LAN(addr.Unmap()) {
			return true
		}
	}
	return false
}
