package plextv

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/remote"
)

// The owner's public host name: a typed scheme is never downgraded, and a
// bare name only becomes https when Ether can actually serve it (#381).
func TestPublicHostURLsHonourTypedScheme(t *testing.T) {
	for _, tc := range []struct {
		host       string
		port       int
		secure     string
		customCert bool
		want       []string
	}{
		{"https://ether.example.com", 32400, remote.SecurePreferred, false, []string{"https://ether.example.com"}},
		{"https://ether.example.com:443", 32400, remote.SecurePreferred, false, []string{"https://ether.example.com:443"}},
		{" HTTPS://ether.example.com/ ", 32400, remote.SecurePreferred, false, []string{"https://ether.example.com"}},
		{"http://ether.example.com:8080", 32400, remote.SecurePreferred, false, []string{"http://ether.example.com:8080"}},
		// A bare name reaches Ether's own listener, whose certificate names
		// plex.direct hosts only: plain http unless https can validate.
		{"ether.example.com", 32400, remote.SecurePreferred, false, []string{"http://ether.example.com:32400"}},
		{"ether.example.com", 32400, remote.SecurePreferred, true, []string{"https://ether.example.com:32400", "http://ether.example.com:32400"}},
		{"ether.example.com", 443, remote.SecurePreferred, false, []string{"https://ether.example.com"}},
		{"ether.example.com:443", 32400, remote.SecurePreferred, false, []string{"https://ether.example.com"}},
		{"ether.example.com", 32400, remote.SecureRequired, false, []string{"https://ether.example.com:32400"}},
		{"ftp://ether.example.com", 32400, remote.SecurePreferred, false, nil},
	} {
		got := publicHostURLs(tc.host, tc.port, tc.secure, tc.customCert)
		if !slices.Equal(got, tc.want) {
			t.Errorf("publicHostURLs(%q, %d, %s, %v) = %q, want %q", tc.host, tc.port, tc.secure, tc.customCert, got, tc.want)
		}
		for _, u := range got {
			if !remote.IsPublishableURI(u) {
				t.Errorf("publicHostURLs(%q) produced unpublishable %q", tc.host, u)
			}
		}
	}
}

// Away from home is checked through the published public host itself, and
// a failed self-check from inside the network is never "unreachable".
func TestRegistrarSelfChecksPublicHost(t *testing.T) {
	machine := testMachineID
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/identity" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"MediaContainer":{"machineIdentifier":%q}}`, machine)
	}))
	defer srv.Close()
	// Every dial lands on the fixture, as a reverse proxy at
	// https://example.com would (the fixture's certificate names example.com).
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	tr.TLSClientConfig = &tls.Config{RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}

	f := newFakePlexTV(t, testMachineID)
	f.addUser("server-token-42", owner())
	cfg := testConfig()
	cfg.Plex.Token = "server-token-42"
	cfg.Plex.Claimed = true
	cfg.Plex.RemoteAccess = true
	cfg.Plex.UseUPnP = false
	cfg.Plex.PublicHost = "https://example.com"
	store := &memConfig{cfg: cfg}
	r, err := NewRegistrar(RegistrarOptions{
		Config: store, Client: newServerClient(t, f), WAN: &fakeWAN{}, Mapper: &fakeMapper{},
		Reach: &remote.ReachabilityChecker{Client: &http.Client{Transport: tr}, Timeout: 2 * time.Second},
		LAN:   func() ([]net.IP, error) { return []net.IP{net.ParseIP("192.168.1.5")}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r.runDue(ctx)
	st := r.Status()
	if !slices.Contains(st.PublishedURIs, "https://example.com") {
		t.Fatalf("published %q, want https://example.com", st.PublishedURIs)
	}
	// plex.tv's verdict is about the direct port, not the proxy route.
	no := false
	st.PlexReachable = &no
	if got := st.WANCheck(); got.State != "reachable" || got.Reason != "self_check" || got.Address != "https://example.com" {
		t.Fatalf("WANCheck = %+v, want reachable self_check https://example.com", got)
	}

	machine = "another-server"
	r.RequestReachabilityCheck()
	r.runDue(ctx)
	if got := r.Status().WANCheck(); got.State != "unreachable" || got.Reason != "wrong_identity" {
		t.Fatalf("WANCheck = %+v, want unreachable wrong_identity", got)
	}

	srv.Close()
	r.RequestReachabilityCheck()
	r.runDue(ctx)
	if got := r.Status().WANCheck(); got.State != "unknown" || got.Reason != "self_check_failed" || got.Address != "https://example.com" {
		t.Fatalf("WANCheck = %+v, want unknown self_check_failed", got)
	}
}
