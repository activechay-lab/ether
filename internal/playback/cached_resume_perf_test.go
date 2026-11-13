package playback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type preferredOnlyResumeResolver struct {
	core.Resolver
	picks int
}

func (r *preferredOnlyResumeResolver) Resolve(_ context.Context, req core.ResolveRequest) (*core.Resolution, error) {
	if !req.PreferredOnly {
		return nil, errors.New("fresh automatic fallback must not be validated")
	}
	r.picks++
	return nil, core.ErrNotAvailable
}

func TestCachedResumeSkipsMetadataAdmissionAndFallbackValidation(t *testing.T) {
	rels := func() []*fakeRelease { return []*fakeRelease{mkv("rel1", 20<<20, 100*time.Minute)} }
	first, second, _, _ := restartHarnesses(t, rels, nil)
	first.prepare(owner, movieKey, "client1")
	if err := first.m.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	caps := defaultCaps("client1")
	start := time.Now()
	if err := second.m.ProbeMetadata(ctx, owner, movieKey, caps); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 100*time.Millisecond {
		t.Fatalf("cached probe took %s", elapsed)
	}
	if second.engine.openCount() != 0 || second.tr.probes != 0 || second.resolver.callCount() != 0 {
		t.Fatal("cached metadata opened, resolved, or probed a source")
	}
	local := core.WithLocalResponse(ctx)
	if live, err := second.m.CheckFileAvailability(local, owner, movieKey, caps, "rel1"); err != nil || !live {
		t.Fatalf("cached file verdict %v %v", live, err)
	}
	if _, err := second.m.CheckFileAvailability(local, owner, movieKey, caps, "missing"); !errors.Is(err, errFileCheckInconclusive) {
		t.Fatalf("other version must remain unchecked: %v", err)
	}
	if second.engine.openCount() != 0 || second.resolver.callCount() != 0 {
		t.Fatal("painting offered versions opened or resolved sources")
	}
	resolver := &preferredOnlyResumeResolver{Resolver: second.m.deps.Resolver}
	second.m.deps.Resolver = resolver
	start = time.Now()
	ps, err := second.m.PrepareVariant(ctx, owner, movieKey, caps, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if ps.Resolution.Release.ID != "rel1" || resolver.picks != 1 || second.tr.probes != 0 {
		t.Fatalf("resume failed to reuse source/probe: %+v picks=%d probes=%d", ps.Resolution, resolver.picks, second.tr.probes)
	}
	t.Logf("cached decision elapsed=%s opens=%d probes=%d preferredChecks=%d", time.Since(start), second.engine.openCount(), second.tr.probes, resolver.picks)
	t.Run("cold-probe-budget", func(t *testing.T) {
		release := make(chan struct{})
		h := newHarness(t, rels(), []string{"rel1"}, withNativeProbe(func(ctx context.Context, src core.MediaSource) (*core.MediaInfo, error) {
			select {
			case <-release:
				return nativeInfo(src), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}, nil))
		start := time.Now()
		err := h.m.ProbeMetadata(context.Background(), owner, movieKey, defaultCaps("cold-client"))
		elapsed := time.Since(start)
		close(release)
		if !errors.Is(err, core.ErrPrepareSlow) {
			t.Fatalf("cold probe did not remain detached: %v", err)
		}
		if elapsed >= 2*time.Second {
			t.Fatalf("cold metadata waited too long: %s", elapsed)
		}
		t.Logf("cold metadata budget elapsed=%s", elapsed)
	})
}
