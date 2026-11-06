// Package nodewire supplies private, boot-bound mTLS primitives. It starts no services.
package nodewire

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"
)

var ErrInvalid = errors.New("nodewire: invalid identity or credentials")
var ErrDenied = errors.New("nodewire: peer denied")

const MaxLifetime = 24 * time.Hour

const minimumBackdate = 5 * time.Minute

// Identity binds the shared cluster, persistent node ID and unique process boot.
// Boot must be the membership incarnation, never a separately generated TLS boot.
type Identity struct{ Cluster, NodeID, Boot string }

func token(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return s != "." && s != ".."
}
func (i Identity) valid() bool { return token(i.Cluster) && token(i.NodeID) && token(i.Boot) }
func (i Identity) uri() *url.URL {
	return &url.URL{Scheme: "ether-node", Host: "identity", Path: "/v1/" + i.Cluster + "/" + i.NodeID + "/" + i.Boot}
}

// NewBoot is available for runtimes without an incarnation generator. Existing
// cluster runtimes must supply their already-generated membership incarnation.
func NewBoot() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", ErrInvalid
	}
	return hex.EncodeToString(b[:]), nil
}

// CertificateIdentity rejects all extra URI identities and noncanonical aliases.
func CertificateIdentity(c *x509.Certificate) (Identity, error) {
	if c == nil || len(c.URIs) != 1 {
		return Identity{}, ErrInvalid
	}
	u := c.URIs[0]
	if u == nil {
		return Identity{}, ErrInvalid
	}
	p := strings.Split(u.Path, "/")
	if len(p) != 5 || p[0] != "" || p[1] != "v1" {
		return Identity{}, ErrInvalid
	}
	i := Identity{p[2], p[3], p[4]}
	if !i.valid() || *u != *i.uri() {
		return Identity{}, ErrInvalid
	}
	return i, nil
}

// Issue signs only a checked Ed25519 CSR with an explicitly supplied Ed25519 CA.
// CSR extensions/names are never copied; enrollment must authorize id and hosts
// independently before calling Issue. Returns leaf DER, never a CA private key.
// Backdating covers clock corrections without relaxing verification. The useful
// lifetime is capped so the entire encoded validity span stays within MaxLifetime.
func Issue(caDER []byte, caKey ed25519.PrivateKey, csrDER []byte, id Identity, hosts []string, now time.Time, lifetime, clockSkew time.Duration) ([]byte, error) {
	if clockSkew < 0 || clockSkew >= MaxLifetime || !id.valid() || len(caDER) > 16384 || len(csrDER) > 8192 || len(caKey) != ed25519.PrivateKeySize || lifetime <= 0 || lifetime > MaxLifetime || now.IsZero() || len(hosts) == 0 || len(hosts) > 8 {
		return nil, ErrInvalid
	}
	ca, e := x509.ParseCertificate(caDER)
	if e != nil || !ca.IsCA || !ca.BasicConstraintsValid || ca.KeyUsage&x509.KeyUsageCertSign == 0 || now.Before(ca.NotBefore) || now.Add(lifetime).After(ca.NotAfter) {
		return nil, ErrInvalid
	}
	pub, ok := ca.PublicKey.(ed25519.PublicKey)
	if !ok || !pub.Equal(caKey.Public()) {
		return nil, ErrInvalid
	}
	csr, e := x509.ParseCertificateRequest(csrDER)
	if e != nil || csr.CheckSignature() != nil || csr.SignatureAlgorithm != x509.PureEd25519 {
		return nil, ErrInvalid
	}
	if _, ok := csr.PublicKey.(ed25519.PublicKey); !ok {
		return nil, ErrInvalid
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return nil, ErrInvalid
	}
	serial.Add(serial, big.NewInt(1))
	notBefore := now.Add(-max(clockSkew, minimumBackdate))
	if notBefore.Before(ca.NotBefore) {
		notBefore = ca.NotBefore
	}
	notAfter := minTime(now.Add(lifetime), notBefore.Add(MaxLifetime))
	t := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{}, NotBefore: notBefore, NotAfter: notAfter, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, URIs: []*url.URL{id.uri()}}
	seen := map[string]bool{}
	for _, h := range hosts {
		if !validHost(h) || seen[h] {
			return nil, ErrInvalid
		}
		seen[h] = true
		if ip := net.ParseIP(h); ip != nil {
			t.IPAddresses = append(t.IPAddresses, ip)
		} else {
			t.DNSNames = append(t.DNSNames, h)
		}
	}
	der, e := x509.CreateCertificate(rand.Reader, t, ca, csr.PublicKey, caKey)
	if e != nil {
		return nil, ErrInvalid
	}
	return der, nil
}
func validHost(h string) bool {
	if len(h) == 0 || len(h) > 253 {
		return false
	}
	if net.ParseIP(h) != nil {
		return true
	}
	for _, s := range strings.Split(h, ".") {
		if len(s) == 0 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
			return false
		}
		for _, c := range s {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
