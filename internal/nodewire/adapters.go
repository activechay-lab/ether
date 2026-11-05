package nodewire

import (
	"context"
	"crypto/x509"

	"github.com/braedonsaunders/ether/internal/nodesource"
)

// SourcePeer fits nodesource.Options.Peer and rechecks current direct-CA trust,
// validity and live policy on each invocation. That API supplies only a leaf,
// so intermediate-CA chains must instead use Request at a private HTTP wrapper
// and an explicitly composed peer mapper. Authority.Verify remains mandatory,
// including membership/revocation checks during long byte copies.
func (g *Guard) SourcePeer(c *x509.Certificate) (nodesource.Peer, error) {
	if c == nil {
		return nodesource.Peer{}, ErrDenied
	}
	leaf, e := x509.ParseCertificate(append([]byte(nil), c.Raw...))
	if e != nil {
		return nodesource.Peer{}, ErrDenied
	}
	id, e := g.current.Load().verify([]*x509.Certificate{leaf}, Identity{}, "", x509.ExtKeyUsageClientAuth, g.now())
	if e != nil {
		return nodesource.Peer{}, e
	}
	if g.policy(context.Background(), id, leaf) != nil {
		return nodesource.Peer{}, ErrDenied
	}
	return nodesource.Peer{NodeID: id.NodeID, Boot: id.Boot}, nil
}
