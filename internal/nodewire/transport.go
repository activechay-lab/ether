package nodewire

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Snapshot owns all parsed credentials and roots. No mutable internals escape.
type Snapshot struct {
	local           Identity
	certPEM, keyPEM []byte
	roots           *x509.CertPool
}

// NewSnapshot accepts a leaf/key PEM and a bounded explicit CA bundle. No system
// roots, peer-supplied anchors, shared-secret key derivation or CA generation.
func NewSnapshot(local Identity, certPEM, keyPEM, rootsPEM []byte, now time.Time) (*Snapshot, error) {
	if !local.valid() || now.IsZero() || len(certPEM) > 65536 || len(keyPEM) > 8192 || len(rootsPEM) > 131072 {
		return nil, ErrInvalid
	}
	cert, e := tls.X509KeyPair(certPEM, keyPEM)
	if e != nil || len(cert.Certificate) == 0 || len(cert.Certificate) > 8 {
		return nil, ErrInvalid
	}
	if _, ok := cert.PrivateKey.(ed25519.PrivateKey); !ok {
		return nil, ErrInvalid
	}
	roots := x509.NewCertPool()
	rest := rootsPEM
	count := 0
	for len(rest) > 0 {
		b, r := pem.Decode(rest)
		if b == nil || b.Type != "CERTIFICATE" || len(b.Headers) != 0 {
			return nil, ErrInvalid
		}
		c, e := x509.ParseCertificate(b.Bytes)
		if e != nil || !c.IsCA || !c.BasicConstraintsValid || c.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, ErrInvalid
		}
		roots.AddCert(c)
		rest = r
		count++
		if count > 16 {
			return nil, ErrInvalid
		}
	}
	if count == 0 {
		return nil, ErrInvalid
	}
	s := &Snapshot{local: local, certPEM: append([]byte(nil), certPEM...), keyPEM: append([]byte(nil), keyPEM...), roots: roots}
	chain := make([]*x509.Certificate, 0, len(cert.Certificate))
	for _, der := range cert.Certificate {
		c, e := x509.ParseCertificate(der)
		if e != nil {
			return nil, ErrInvalid
		}
		chain = append(chain, c)
	}
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth} {
		if _, e = s.verify(chain, local, "", usage, now); e != nil {
			return nil, e
		}
	}
	return s, nil
}
func (s *Snapshot) credential() tls.Certificate {
	c, _ := tls.X509KeyPair(s.certPEM, s.keyPEM)
	return c
}
func (s *Snapshot) verify(chain []*x509.Certificate, expected Identity, hostname string, usage x509.ExtKeyUsage, now time.Time) (Identity, error) {
	if len(chain) == 0 || len(chain) > 8 {
		return Identity{}, ErrDenied
	}
	leaf := chain[0]
	if leaf == nil {
		return Identity{}, ErrDenied
	}
	id, e := CertificateIdentity(leaf)
	if e != nil || id.Cluster != s.local.Cluster || expected.valid() && id != expected || leaf.IsCA || leaf.NotAfter.Sub(leaf.NotBefore) > MaxLifetime || leaf.SignatureAlgorithm != x509.PureEd25519 {
		return Identity{}, ErrDenied
	}
	if _, ok := leaf.PublicKey.(ed25519.PublicKey); !ok {
		return Identity{}, ErrDenied
	}
	if len(leaf.DNSNames)+len(leaf.IPAddresses) > 8 || len(leaf.EmailAddresses) != 0 {
		return Identity{}, ErrDenied
	}
	intermediates := x509.NewCertPool()
	for _, c := range chain[1:] {
		intermediates.AddCert(c)
	}
	_, e = leaf.Verify(x509.VerifyOptions{Roots: s.roots, Intermediates: intermediates, DNSName: hostname, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{usage}})
	if e != nil {
		return Identity{}, fmt.Errorf("%w: certificate chain: %v", ErrDenied, e)
	}
	return id, nil
}

// Policy must consult current membership/incarnation and revocation (including
// serial deny lists if used). Unavailable authority MUST return an error. It
// grants no placement/source rights; consumers still authorize each operation.
type Policy func(context.Context, Identity, *x509.Certificate) error

// Guard atomically replaces immutable credentials/trust. Policy is mandatory and
// called outside locks on each check, including operations on reused connections.
type Guard struct {
	denialMu      sync.Mutex
	denialAt      time.Time
	denialSkipped uint64
	current       atomic.Pointer[Snapshot]
	policy        Policy
	now           func() time.Time
}

func NewGuard(s *Snapshot, p Policy, now func() time.Time) (*Guard, error) {
	if s == nil || s.roots == nil || len(s.certPEM) == 0 || len(s.keyPEM) == 0 || p == nil || now == nil {
		return nil, ErrInvalid
	}
	g := &Guard{policy: p, now: now}
	g.current.Store(s)
	return g, nil
}

// Replace preserves this process identity. Use a new Guard for a new boot.
func (g *Guard) Replace(s *Snapshot) error {
	old := g.current.Load()
	if s == nil || s.roots == nil || len(s.certPEM) == 0 || len(s.keyPEM) == 0 || s.local != old.local {
		return ErrInvalid
	}
	g.current.Store(s)
	return nil
}
func (g *Guard) check(ctx context.Context, cs tls.ConnectionState, expected Identity, hostname string, usage x509.ExtKeyUsage) (Identity, error) {
	if len(cs.VerifiedChains) == 0 {
		return Identity{}, ErrDenied
	}
	id, e := g.current.Load().verify(cs.PeerCertificates, expected, hostname, usage, g.now())
	if e != nil {
		g.logDenial("certificate", expected, e)
		return Identity{}, e
	}
	// Give policy its own parsed copy, not TLS's shared certificate slices.
	leaf, e := x509.ParseCertificate(append([]byte(nil), cs.PeerCertificates[0].Raw...))
	if e == nil {
		e = g.policy(ctx, id, leaf)
	}
	if e != nil {
		g.logDenial("membership_policy", id, e)
		return Identity{}, ErrDenied
	}
	return id, nil
}

// Request rechecks current trust, expiry and live membership for every request.
func (g *Guard) Request(r *http.Request) (Identity, error) {
	if r == nil || r.TLS == nil || !r.TLS.HandshakeComplete {
		return Identity{}, ErrDenied
	}
	return g.check(r.Context(), *r.TLS, Identity{}, "", x509.ExtKeyUsageClientAuth)
}

// RemoteAuthenticate maps the existing remotetranscode boot-only Controller DTO.
// Its separate Security.Authorize callback remains mandatory.
func (g *Guard) RemoteAuthenticate(r *http.Request) (string, error) {
	id, e := g.Request(r)
	if e != nil {
		return "", e
	}
	return id.Boot, nil
}

// BindServer is also usable as nodesource.ClientOptions.BindServer. That hook
// alone runs at handshake: use RecheckServer on every subsequent operation.
func (g *Guard) BindServer(expected Identity, hostname string) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		return g.RecheckServer(context.Background(), cs, expected, hostname)
	}
}
func (g *Guard) RecheckServer(ctx context.Context, cs tls.ConnectionState, expected Identity, hostname string) error {
	if !expected.valid() || !validHost(hostname) {
		return ErrInvalid
	}
	_, e := g.check(ctx, cs, expected, hostname, x509.ExtKeyUsageServerAuth)
	return e
}

// ClientTLS returns a fresh independent config for an explicit hostname AND
// expected identity. Rebuild the transport after root/client credential rotation.
func (g *Guard) ClientTLS(expected Identity, hostname string) (*tls.Config, error) {
	if !expected.valid() || !validHost(hostname) {
		return nil, ErrInvalid
	}
	s := g.current.Load()
	if expected.Cluster != s.local.Cluster {
		return nil, ErrInvalid
	}
	return &tls.Config{Time: g.now, MinVersion: tls.VersionTLS12, ServerName: hostname, RootCAs: s.roots.Clone(), Certificates: []tls.Certificate{s.credential()}, VerifyConnection: g.BindServer(expected, hostname), SessionTicketsDisabled: true}, nil
}
func (g *Guard) serverSnapshot() *tls.Config {
	s := g.current.Load()
	return &tls.Config{Time: g.now, MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: s.roots.Clone(), Certificates: []tls.Certificate{s.credential()}, SessionTicketsDisabled: true, VerifyConnection: func(cs tls.ConnectionState) error {
		_, e := g.check(context.Background(), cs, Identity{}, "", x509.ExtKeyUsageClientAuth)
		return e
	}}
}

// ServerTLS selects current credentials/trust per new handshake. Request remains
// required: neither handshake policy nor TLS session state is current authority.
func (g *Guard) ServerTLS() *tls.Config {
	c := g.serverSnapshot()
	c.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) { return g.serverSnapshot(), nil }
	return c
}

// Bound repeated denials per Guard without a peer-controlled map. Successful
// unrelated requests do not reset the rate limit or conceal the failure reason.
func (g *Guard) logDenial(stage string, peer Identity, err error) {
	now := time.Now()
	g.denialMu.Lock()
	if !g.denialAt.IsZero() && now.Sub(g.denialAt) < time.Minute {
		g.denialSkipped++
		g.denialMu.Unlock()
		return
	}
	skipped := g.denialSkipped
	g.denialAt, g.denialSkipped = now, 0
	g.denialMu.Unlock()
	slog.Warn("private TLS peer denied", "component", "nodewire", "stage", stage, "node", peer.NodeID, "boot", peer.Boot, "reason", err, "suppressed", skipped)
}
