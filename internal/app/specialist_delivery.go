package app

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/clusterplayback"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontends/plex"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/safety"
)

// buildDeliveryRole constructs only live identity reading, private transport and
// the real fixed public representation sender. It has no catalogue/frontends,
// registrar, providers, resolver, source ownership, cache or encoder.
func (a *App) buildDeliveryRole(ctx context.Context, cfg config.Config) error {
	var err error
	a.users, err = auth.New(ctx, a.db, auth.Options{Logger: a.log})
	if err != nil {
		return err
	}
	a.users.SetBus(a.clusterNode())
	a.safety, err = safety.New(ctx, a.db)
	if err != nil {
		return err
	}
	a.safety.SetBus(a.clusterNode())
	a.closers = append(a.closers, a.users.Close, a.safety.Close)
	if err = a.buildPrivateCredentials(); err != nil {
		return err
	}
	if err = a.buildClusterPlayback(ctx, cfg); err != nil {
		return err
	}
	a.certs, err = plextv.NewCertManager(plextv.CertManagerOptions{Dir: filepath.Join(a.opts.DataDir, "certs"), Shared: a.sharedCerts(), CustomCertFile: cfg.Plex.CertFile, CustomKeyFile: cfg.Plex.KeyFile, Logger: a.log})
	if err != nil {
		return err
	}
	a.certs.SetRenewalAllowed(func() bool { return false })
	a.certs.SetEventBus(a.cluster)
	a.closers = append(a.closers, a.certs.Close)
	runtime := a.deployment.playback.(*clusterplayback.Runtime)
	a.handler, err = runtime.NewPublicDelivery(a.authorizeDeliveryPlex)
	return err
}
func (a *App) authorizeDeliveryPlex(ctx context.Context, r *http.Request, _ string) error {
	// Use the shipping Plex parser: native media loaders may retain only the
	// query credential, including its established case-insensitive spelling.
	token := plex.ParseRequest(r, a.trustedProxyPrefixes()...).Token
	if token == "" || core.IsGrantCredential(token) || auth.IsOwnerSession(token) {
		return core.ErrUnauthorized
	}
	user, receipt, err := a.users.Accept(ctx, token)
	if err != nil || user == nil {
		return core.ErrUnauthorized
	}
	if err = a.users.ReceiptCurrent(ctx, receipt); err != nil {
		return core.ErrUnauthorized
	}
	// This reader has no upstream token validator: nondelegated acceptance is
	// exclusively an enrolled durable credential. Recheck it in the authority,
	// because another process can rotate it before our cache receives the bus.
	// Delegation receipts already check their exact durable row and expiry.
	if !user.Delegated {
		if err := a.users.DurableTokenCurrent(ctx, token, user.ID); err != nil {
			return core.ErrUnauthorized
		}
	}
	fresh, err := a.users.PolicyUser(ctx, user.ID)
	if err != nil || fresh == nil || fresh.ID != user.ID {
		return core.ErrUnauthorized
	}
	ip, lan := identity.ClientNet(r, a.trustedProxyPrefixes()...)
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() {
		return core.ErrUnauthorized
	}
	if _, err := a.safety.ConfirmPlayback(ctx, fresh, !lan, time.Now()); err != nil {
		return core.ErrUnauthorized
	}
	// The owner receives the ORIGINAL credential and repeats the complete public
	// Plex grant/session/current-media policy. No viewer assertion crosses TLS.
	return nil
}
