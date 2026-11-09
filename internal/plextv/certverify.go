package plextv

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/remote"
)

const (
	// CertReasonChain means the leaf did not chain to a trusted root.
	CertReasonChain = "chain"
	// CertReasonIdentity means the leaf is not *.<this server's hash>.plex.direct.
	CertReasonIdentity = "identity"
	// CertReasonHandshake means a local plex.direct TLS connection did not succeed.
	CertReasonHandshake = "handshake"
	// CertReasonDates means the leaf is outside its validity window.
	CertReasonDates = "dates"
)

type trustDialFunc func(ctx context.Context, network, addr string, cfg *tls.Config) error

type certTrust struct {
	fingerprint string
	verified    bool
	reason      string
	// misses counts consecutive failed handshakes for this certificate.
	misses int
}

// trustHandshakeConfirm is how many handshakes in a row must fail before the
// console says the server did not accept a secure connection. One failure is
// not proof: every restart checks before its listener serves, and a dial can
// blip. Until it is confirmed, the earlier result stands and the registrar
// looks again at its next tick.
const trustHandshakeConfirm = 2

// pending reports a handshake failure that is not confirmed yet.
func (t certTrust) pending() bool {
	return t.misses > 0 && t.misses < trustHandshakeConfirm
}

// VerifyTrust checks the installed certificate and remembers the result.
// Status polls read that memory; they do not call this.
// VerifyTrust checks the installed plex.direct certificate and then a TLS
// handshake for serverName (a plex.direct host) against each of dialAddrs in
// turn: Ether's own listener first, then the advertised LAN address. Dialing
// Ether directly needs no DNS, so a router that blocks *.plex.direct lookups
// (DNS rebinding protection) or a Docker host without hairpin NAT does not
// make a working certificate look broken (#251).
func (m *CertManager) VerifyTrust(ctx context.Context, identityHash, serverName string, dialAddrs ...string) (bool, string) {
	m.mu.RLock()
	pair := m.cert
	roots := m.trustRoots
	dial := m.trustDial
	now := time.Now()
	if m.o.Now != nil {
		now = m.o.Now()
	}
	m.mu.RUnlock()
	if pair == nil || len(pair.Certificate) == 0 {
		return false, CertReasonChain
	}
	leaf := pair.Leaf
	if leaf == nil {
		parsed, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return false, CertReasonChain
		}
		leaf = parsed
	}
	var intermediates []*x509.Certificate
	for _, raw := range pair.Certificate[1:] {
		c, err := x509.ParseCertificate(raw)
		if err == nil {
			intermediates = append(intermediates, c)
		}
	}
	ok, reason := verifyPlexDirect(leaf, intermediates, roots, identityHash, now)
	if ok {
		dialErr := errors.New("plextv: no local plex.direct address")
	dial:
		for _, addr := range dialAddrs {
			if addr == "" {
				continue
			}
			for attempt := 0; attempt < 2; attempt++ {
				if dialErr = handshakePlexDirect(ctx, dial, roots, serverName, addr); dialErr == nil {
					break dial
				}
			}
		}
		if dialErr != nil {
			ok, reason = false, CertReasonHandshake
		}
	}
	fp := Fingerprint(leaf)
	m.mu.Lock()
	defer m.mu.Unlock()
	next := certTrust{fingerprint: fp, verified: ok, reason: reason}
	if reason == CertReasonHandshake {
		next.misses = 1
		if m.trust.fingerprint == fp {
			next.misses = m.trust.misses + 1
		}
		if next.misses < trustHandshakeConfirm {
			// The chain, name and dates just passed. Keep an earlier
			// verified result for this certificate; otherwise (a fresh
			// start) the console shows "checking", not a warning.
			next.verified, next.reason = m.trust.fingerprint == fp && m.trust.verified, ""
		}
	}
	m.trust = next
	return next.verified, next.reason
}

// trustPending reports an unconfirmed handshake failure for the installed
// certificate, which the registrar re-checks at its next tick.
func (m *CertManager) trustPending(fingerprint string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.trust.fingerprint == fingerprint && m.trust.pending()
}

func verifyPlexDirect(leaf *x509.Certificate, intermediates []*x509.Certificate, roots *x509.CertPool, identityHash string, now time.Time) (bool, string) {
	identityHash = strings.ToLower(strings.TrimSpace(identityHash))
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return false, CertReasonDates
	}
	name := "x." + identityHash + ".plex.direct"
	if identityHash == "" || leaf.VerifyHostname(name) != nil {
		return false, CertReasonIdentity
	}
	pool := x509.NewCertPool()
	for _, c := range intermediates {
		pool.AddCert(c)
	}
	opts := x509.VerifyOptions{
		DNSName:       name,
		Intermediates: pool,
		Roots:         roots,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := leaf.Verify(opts); err != nil {
		return false, CertReasonChain
	}
	return true, ""
}

func handshakePlexDirect(ctx context.Context, dial trustDialFunc, roots *x509.CertPool, serverName, addr string) error {
	if serverName == "" || addr == "" {
		return errors.New("plextv: no local plex.direct address")
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName, RootCAs: roots}
	if dial != nil {
		return dial(ctx, "tcp", addr, cfg)
	}
	d := &tls.Dialer{Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// localPlexDirectDial is host:port of a local https plex.direct connection.
func localPlexDirectDial(conns []remote.Connection) string {
	for _, c := range conns {
		if !c.Local || c.Protocol != "https" {
			continue
		}
		u, err := url.Parse(c.URI)
		if err != nil || u.Hostname() == "" {
			continue
		}
		if _, _, ok := remote.ParsePlexDirectHost(u.Hostname()); !ok {
			continue
		}
		return u.Host
	}
	return ""
}
