package clientnet

import (
	"net"
	"strings"
)

func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64
}

func usableIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// 100.64.0.0/10 (CGNAT, also Tailscale) and 198.18.0.0/15 (benchmarking).
		if isCGNAT(v4) {
			return false
		}
		if v4[0] == 198 && (v4[1] == 18 || v4[1] == 19) {
			return false
		}
	}
	return true
}

// IsPublicIP reports whether ip is a globally routable unicast address.
func IsPublicIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && usableIP(ip)
}

// ParsePlexDirectHost parses "<ip-label>.<hash>.plex.direct" hosts.
func ParsePlexDirectHost(host string) (ip net.IP, hash string, ok bool) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if !strings.HasSuffix(host, ".plex.direct") {
		return nil, "", false
	}
	labels := strings.Split(strings.TrimSuffix(host, ".plex.direct"), ".")
	if len(labels) != 2 || labels[0] == "" || labels[1] == "" {
		return nil, "", false
	}
	l := labels[0]
	if strings.Count(l, "-") == 3 {
		ip = net.ParseIP(strings.ReplaceAll(l, "-", ".")).To4()
	} else {
		ip = net.ParseIP(strings.ReplaceAll(l, "-", ":"))
	}
	if ip == nil {
		return nil, "", false
	}
	return ip, labels[1], true
}
