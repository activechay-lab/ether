package clusterplayback

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
	"testing"
	"time"
)

func TestRampAdmissionSnapshot(t *testing.T) {
	q := core.HLSRequest{SessionID: "ramp", Info: core.MediaInfo{Duration: 29 * time.Second, Streams: []core.StreamInfo{{Type: "video", Codec: "h264"}}}, Decision: core.Decision{Kind: core.DecisionTranscode, Container: "mpegts"}}
	q = transcode.AuthorDistributedRamp(q, config.Transcode{FastStart: true})
	if q.Ramp.Segments != 2 {
		t.Fatal("ramp not authored")
	}
	initial := newInitialAdmission(Route{}, q)
	raw, err := json.Marshal(initial.request)
	if err != nil {
		t.Fatal(err)
	}
	var restored core.HLSRequest
	if err = json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	restored.Resume = &core.HLSResume{FirstSegment: 3, Generation: 2}
	resumed := transcode.AuthorDistributedRamp(cloneAdmissionRequest(restored), config.Transcode{FastStart: true, FastStartSegments: 8})
	q.Ramp.Segments = 9
	if resumed.Ramp != initial.request.Ramp || resumed.SegmentDuration != initial.request.SegmentDuration || resumed.Resume.FirstSegment != 3 {
		t.Fatal("durable ramp changed during recovery")
	}
}
