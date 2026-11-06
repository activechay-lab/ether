package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/clusterconfig"
	"github.com/braedonsaunders/ether/internal/nodewire"
)

// issuePrivateSnapshot reopens projected Secret files on every rotation. The
// issuer is an explicit trusted deployment key, independent of encryption keys.
func issuePrivateSnapshot(d clusterconfig.Deployment, self nodewire.Identity) (*nodewire.Snapshot, error) {
	ca, err := clusterconfig.ReadSecretFile(d.CAFile, 16384)
	if err != nil {
		return nil, err
	}
	key, err := clusterconfig.ReadSecretFile(d.CAKeyFile, 8192)
	if err != nil {
		return nil, err
	}
	roots, err := clusterconfig.ReadSecretFile(d.RootsFile, 131072)
	if err != nil {
		return nil, err
	}
	cb, _ := pem.Decode(ca)
	kb, _ := pem.Decode(key)
	if cb == nil || cb.Type != "CERTIFICATE" || kb == nil || kb.Type != "PRIVATE KEY" {
		return nil, nodewire.ErrInvalid
	}
	parsed, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
	if err != nil {
		return nil, nodewire.ErrInvalid
	}
	signer, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, nodewire.ErrInvalid
	}
	_, leafKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nodewire.ErrInvalid
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, leafKey)
	if err != nil {
		return nil, nodewire.ErrInvalid
	}
	origin, err := url.Parse(d.InternalAddress)
	if err != nil {
		return nil, nodewire.ErrInvalid
	}
	now := time.Now()
	leaf, err := nodewire.Issue(cb.Bytes, signer, csr, self, []string{origin.Hostname()}, now, 12*time.Hour, time.Duration(d.ClockSkewMillis)*time.Millisecond)
	if err != nil {
		return nil, err
	}
	leafDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return nil, nodewire.ErrInvalid
	}
	return nodewire.NewSnapshot(self, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafDER}), roots, now)
}

// privateMember always queries current DB authority; no cached success survives
// a lost heartbeat, changed boot, removal, unknown role, or database outage.
func (a *App) privateMember(ctx context.Context, id nodewire.Identity) (cluster.Node, error) {
	deny := func(reason string) (cluster.Node, error) {
		return cluster.Node{}, fmt.Errorf("%w: membership %s", nodewire.ErrDenied, reason)
	}
	if id.Cluster != a.cfg.Get().Server.MachineIdentifier {
		return deny("cluster_mismatch")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	nodes, err := a.cluster.AllNodes(ctx)
	if err != nil {
		return deny("query_unavailable")
	}
	bound := time.Duration(0)
	if a.deployment != nil {
		bound = time.Duration(a.deployment.settings.ClockSkewMillis) * time.Millisecond
	}
	for _, n := range nodes {
		if n.ID != id.NodeID {
			continue
		}
		if n.Incarnation != id.Boot {
			return deny("boot_mismatch")
		}
		age := n.LastSeenAge(time.Now())
		if age < -bound {
			return deny(fmt.Sprintf("heartbeat_future age=%s bound=%s", age, bound))
		}
		if age >= clusterLiveAfter {
			return deny(fmt.Sprintf("heartbeat_stale age=%s", age))
		}
		if !n.Live {
			return deny("not_live")
		}
		if n.Caps.Roles.Validate() != nil {
			return deny("invalid_roles")
		}
		return n, nil
	}
	return deny("node_missing")
}

func (a *App) privateEndpoint(ctx context.Context, id nodewire.Identity) (string, error) {
	n, err := a.privateMember(ctx, id)
	if err != nil {
		return "", err
	}
	if n.InternalAddress == "" {
		return "", errors.New("private endpoint unavailable")
	}
	return n.InternalAddress, nil
}
