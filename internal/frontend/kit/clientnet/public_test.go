package clientnet

import (
	"net"
	"testing"
)

func TestIsPublicIPAndParsePlexDirectHost(t *testing.T) {
	for _, raw := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !IsPublicIP(net.ParseIP(raw)) {
			t.Fatalf("expected public IP for %s", raw)
		}
	}
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.5", "100.64.1.2", "198.18.0.1", "169.254.1.1", "fd00::1"} {
		if IsPublicIP(net.ParseIP(raw)) {
			t.Fatalf("expected non-public IP for %s", raw)
		}
	}
	ip, hash, ok := ParsePlexDirectHost("8-8-8-8.abcdef.plex.direct.")
	if !ok || !ip.Equal(net.ParseIP("8.8.8.8")) || hash != "abcdef" || !IsPublicIP(ip) {
		t.Fatalf("unexpected v4 parse: ip=%v hash=%q ok=%v", ip, hash, ok)
	}
	ip, _, ok = ParsePlexDirectHost("192-168-1-5.abcdef.plex.direct")
	if !ok || IsPublicIP(ip) {
		t.Fatalf("expected private plex.direct IP: ip=%v ok=%v", ip, ok)
	}
	if _, _, ok := ParsePlexDirectHost("not-plex.example.com"); ok {
		t.Fatal("expected non-plex.direct host to fail")
	}
}
