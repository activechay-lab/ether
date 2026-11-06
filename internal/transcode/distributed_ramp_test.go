package transcode

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestDistributedRampBounds(t *testing.T) {
	m, q := distributedFixture()
	q.Info.Duration = 29 * time.Second
	q.Decision.Kind, q.Decision.VideoCopy, q.Decision.Container = core.DecisionTranscode, false, ContainerMPEGTS
	q = AuthorDistributedRamp(q, fastStartConfig())
	if q.Ramp.Version != 1 || !m.SupportsDistributedRequest(q) {
		t.Fatal("authored ramp rejected")
	}
	for _, change := range []func(*core.HLSRequest){
		func(q *core.HLSRequest) { q.Ramp.Version = 2 },
		func(q *core.HLSRequest) { q.Ramp.Segments = 17 },
		func(q *core.HLSRequest) { q.Ramp.Duration = q.SegmentDuration },
		func(q *core.HLSRequest) { q.Ramp.Duration = -1 },
		func(q *core.HLSRequest) { q.Ramp.Version = 0 },
		func(q *core.HLSRequest) { q.Decision.VideoCopy = true },
		func(q *core.HLSRequest) { q.StartOffset = time.Second },
		func(q *core.HLSRequest) { q.LiveInput = true },
		func(q *core.HLSRequest) { q.Protocol = core.ProtocolDASH },
		func(q *core.HLSRequest) { q.Info.Duration = 0 },
		func(q *core.HLSRequest) { n := 1; q.SoftSubtitleStream = &n },
	} {
		bad := q
		change(&bad)
		if ValidDistributedRamp(bad) || m.SupportsDistributedRequest(bad) {
			t.Fatalf("invalid ramp admitted: %+v", bad.Ramp)
		}
	}
	legacy := q
	legacy.Ramp = core.HLSRamp{}
	legacy.Decision.VideoCopy = true
	legacy.SegmentDuration = 0
	got := AuthorDistributedRamp(legacy, fastStartConfig())
	if got.Ramp != legacy.Ramp || got.SegmentDuration != 0 {
		t.Fatal("unsupported request changed")
	}
}
