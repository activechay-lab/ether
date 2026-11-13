package plex

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/identity"
	"testing"
)

type testPlex struct{ calls int }

func (p *testPlex) Authenticate(_ context.Context, token string) (*core.User, error) {
	p.calls++
	if token != "plex" {
		return nil, core.ErrUnauthorized
	}
	return &core.User{ID: 7, PlexID: 9}, nil
}
func TestPlexProviderKeepsGrantsOutOfPlex(t *testing.T) {
	p := &testPlex{}
	provider := PlexProvider{Auth: p}
	if _, err := provider.Verify(context.Background(), identity.Proof{Token: "eg1_fake"}); !errors.Is(err, core.ErrUnauthorized) || p.calls != 0 {
		t.Fatalf("grant accepted: %v, calls %d", err, p.calls)
	}
	got, err := provider.Verify(context.Background(), identity.Proof{Token: "plex"})
	if err != nil || got.ViewerID != 7 || got.Subject != "9" {
		t.Fatalf("verification: %+v %v", got, err)
	}
}
