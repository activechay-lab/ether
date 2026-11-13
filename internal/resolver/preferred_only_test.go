package resolver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// A remembered healthy source belongs to playback. The resolver checks the
// explicit pick without paying to validate a fresh fallback it will discard.
func TestPreferredOnlyDoesNotValidateAutomaticFallback(t *testing.T) {
	var checks atomic.Int64
	svc := New(&fakeIndexer{releases: movieReleases()}, fakeHealth{unhealthy: map[string]bool{"sd720": true}, checks: &checks}, nil, testCfg(), Options{})
	defer svc.Close()
	req := core.ResolveRequest{Title: matrixTitle(), Client: webCaps(), Prefer: "sd720", PreferredOnly: true}
	got, err := svc.Resolve(context.Background(), req)
	if !errors.Is(err, core.ErrNotAvailable) || got != nil {
		t.Fatalf("preferred-only result %+v %v", got, err)
	}
	if checks.Load() != 1 {
		t.Fatalf("validated a fallback: checks=%d", checks.Load())
	}
	req.PreferredOnly = false
	got, err = svc.Resolve(context.Background(), req)
	if err != nil || got == nil || got.Release.ID == "sd720" {
		t.Fatalf("normal fallback contract lost %+v %v", got, err)
	}
}
