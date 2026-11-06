package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/nodesource"
	"time"
)

type sourceCredential struct {
	source  *ownedSource
	expires time.Time
}
type trackedAuthority struct{ r *Runtime }

func (a trackedAuthority) Verify(ctx context.Context, peer nodesource.Peer, credential string) (nodesource.Grant, error) {
	r := a.r
	r.mu.Lock()
	entry, ok := r.credentials[credential]
	if !ok || entry.source.retiring {
		r.mu.Unlock()
		return nodesource.Grant{}, ErrDenied
	}
	source := entry.source
	source.operations++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); source.operations--; r.mu.Unlock() }()
	grant, err := r.authority.Verify(ctx, peer, credential)
	if err != nil || grant.Scope != scopeForRoute(source.route) {
		return nodesource.Grant{}, ErrDenied
	}
	return grant, nil
}
func (r *Runtime) acquireSource(ctx context.Context, scope nodesource.Scope) (nodesource.Hold, error) {
	r.mu.Lock()
	source := r.sources[sourceKey(scope.Fence, scope.SourceGeneration)]
	if source == nil || source.binding || source.retiring || scope != scopeForRoute(source.route) {
		r.mu.Unlock()
		return nodesource.Hold{}, ErrDenied
	}
	source.operations++
	r.mu.Unlock()
	defer func() { r.mu.Lock(); source.operations--; r.mu.Unlock() }()
	return r.authority.Acquire(ctx, scope)
}
func (r *Runtime) trackCredentialLocked(source *ownedSource, grant nodesource.Grant) bool {
	now := time.Now()
	for key, entry := range r.credentials {
		if entry.source.retiring || !entry.expires.After(now) {
			delete(r.credentials, key)
		}
	}
	if len(r.credentials) >= 4096 {
		return false
	}
	r.credentials[grant.Credential] = sourceCredential{source: source, expires: grant.Expires}
	return true
}
func (r *Runtime) sourceJoined(source *ownedSource) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return source.sealed && source.retiring && !source.binding && !source.issuing && source.operations == 0
}

func (a trackedAuthority) CheckLocal(ctx context.Context, peer nodesource.Peer, credential string) error {
	r := a.r
	r.mu.Lock()
	entry, ok := r.credentials[credential]
	denied := r.closed || !ok || entry.source.retiring || entry.source.sealed
	r.mu.Unlock()
	if denied {
		return ErrDenied
	}
	return r.authority.CheckLocal(ctx, peer, credential)
}
