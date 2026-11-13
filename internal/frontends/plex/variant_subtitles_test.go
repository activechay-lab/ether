package plex

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestAppleTVPersistedVersionEmbeddedSubtitles(t *testing.T) {
	f, pb := newVariantPlaybackFixture(t)
	info := sampleInfo
	info.Streams = append([]core.StreamInfo(nil), sampleInfo.Streams...)
	info.Streams = append(info.Streams,
		core.StreamInfo{Index: 2, Type: "subtitle", Codec: "pgssub", Language: "eng"},
		core.StreamInfo{Index: 7, Type: "subtitle", Codec: "srt", Language: "fra"})
	info.Estimated = false
	pb.info = info
	// The editions surface now supplies persisted facts for each release.
	f.resolver.byKey[fightClub.Key][1].Info = info
	headers := []string{"X-Plex-Product", "Plex for Apple TV", "X-Plex-Platform", "tvOS"}
	detail := f.get(t, "/library/metadata/"+fightClub.Key.String(), headers...)
	if detail.Code != 200 {
		t.Fatal(detail.Body.String())
	}
	pgs := ""
	for _, tag := range subtitleTags(detail.Body.String()) {
		if strings.Contains(tag, `codec="pgs"`) && strings.Contains(tag, `index="2"`) {
			pgs = streamAttr(tag, "id")
		}
	}
	if pgs == "" || !strings.Contains(detail.Body.String(), `codec="srt"`) {
		t.Fatalf("persisted variant tracks missing: %s", detail.Body.String())
	}
	decision := f.get(t, "/video/:/transcode/universal/decision?path=/library/metadata/"+fightClub.Key.String()+"&mediaIndex=1&session=appletv-pgs&subtitleStreamID="+pgs, headers...)
	if decision.Code != 200 {
		t.Fatalf("decision: %d %s", decision.Code, decision.Body.String())
	}
	found := false
	for _, tag := range subtitleTags(decision.Body.String()) {
		if strings.Contains(tag, `codec="pgs"`) && strings.Contains(tag, `selected="1"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("selected PGS missing from alternate decision: %s", decision.Body.String())
	}
}
