package clusterplayback

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestPreparedMetadataPreservesCompleteDetachedIndex(t *testing.T) {
	original := PreparedSource{PlayID: "prepared", Revision: "exact", Info: core.MediaInfo{Size: 100, Duration: 10 * time.Second, Keyframes: []core.KeyframePoint{{Offset: 0, Time: 0}, {Offset: 20, Time: time.Second}, {Offset: 99, Time: 9 * time.Second}}}}
	raw, e := json.Marshal(sourceResponse{Prepared: original})
	if e != nil {
		t.Fatal(e)
	}
	var reply sourceResponse
	if e = json.Unmarshal(raw, &reply); e != nil {
		t.Fatal(e)
	}
	if len(reply.Prepared.Info.Keyframes) != 3 || reply.Prepared.Info.Keyframes[1] != original.Info.Keyframes[1] {
		t.Fatal("keyframe index vanished or changed")
	}
	copied := copyPlaySession(&core.PlaySession{Info: reply.Prepared.Info})
	copied.Info.Keyframes[1].Offset = 55
	if reply.Prepared.Info.Keyframes[1].Offset != 20 {
		t.Fatal("snapshot shared keyframe ownership")
	}
	reply.Prepared.Info.Keyframes[2].Offset = 88
	if original.Info.Keyframes[2].Offset != 99 {
		t.Fatal("wire metadata shared original ownership")
	}
	original.Info.Keyframes = make([]core.KeyframePoint, maxPreparedKeyframes+1)
	if _, e = json.Marshal(original); e == nil {
		t.Fatal("oversize index silently truncated")
	}
}
