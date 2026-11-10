package plex

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestUnselectedSubtitleIsNeverBurned is the #313 invariant: a subtitle the
// client shows as None is never burned into the picture. The stored choice
// is per viewer, so a PGS picked on the Apple TV must not turn Plex Web's
// remux, which shows None, into a full transcode. Once Plex Web is shown
// the track selected (a fresh detail page, or its own pick) it is burned,
// and a request that names a track or None always wins.
func TestUnselectedSubtitleIsNeverBurned(t *testing.T) {
	f, _ := newSubtitleFixture(t)
	f.trans.decision = core.Decision{Kind: core.DecisionTranscode, VideoCodec: "h264", AudioCodec: "aac", Container: "mpegts", BurnSubtitles: true}
	f.playback.info = core.MediaInfo{
		Container: "mkv", Duration: 14 * time.Minute, BitrateKbps: 40000,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160, BitDepth: 10, Default: true},
			{Index: 1, Type: "audio", Codec: "truehd", Language: "en", Channels: 8, Default: true},
			{Index: 2, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "nl"},
		},
	}
	key := fightClub.Key.String()
	web := []string{"X-Plex-Product", "Plex Web", "X-Plex-Platform", "Chrome", "X-Plex-Client-Identifier", "web-313"}
	meta := f.get(t, "/library/metadata/"+key, web...)
	pgsID := ""
	for _, tag := range subtitleTags(meta.Body.String()) {
		if strings.Contains(tag, `codec="pgs"`) {
			pgsID = streamAttr(tag, "id")
			if strings.Contains(tag, `selected="1"`) {
				t.Fatalf("PGS selected before any pick: %s", tag)
			}
		}
	}
	if pgsID == "" {
		t.Fatalf("no PGS stream in metadata:\n%s", meta.Body.String())
	}
	decide := func(session, extra string) (int, string) {
		t.Helper()
		rec := f.get(t, fmt.Sprintf("/video/:/transcode/universal/decision?path=/library/metadata/%s&protocol=dash&directPlay=0&directStream=1&session=%s%s", key, session, extra), web...)
		if rec.Code != 200 {
			t.Fatalf("decision %s: %d %s", session, rec.Code, rec.Body.String())
		}
		return f.trans.lastOpts.SubtitleStream, selectedTag(subtitleTags(rec.Body.String()))
	}

	// Picked on another device; Plex Web still shows None.
	putSubtitle(t, f, key, pgsID)
	if sub, row := decide("s1", ""); sub != -1 || row != "" {
		t.Fatalf("unshown stored PGS: subtitle stream %d, selected row %q", sub, row)
	}
	// Plex Web loads the detail page again and is shown the PGS selected.
	f.get(t, "/library/metadata/"+key, web...)
	if sub, row := decide("s2", ""); sub != 2 || !strings.Contains(row, `decision="burn"`) {
		t.Fatalf("shown PGS: subtitle stream %d, selected row %q", sub, row)
	}
	// The request names None, then the track.
	if sub, _ := decide("s3", "&subtitleStreamID=0"); sub != -1 {
		t.Fatalf("subtitleStreamID=0 burned stream %d", sub)
	}
	if sub, _ := decide("s4", "&subtitleStreamID="+pgsID); sub != 2 {
		t.Fatalf("named PGS: subtitle stream %d", sub)
	}
	// Plex Web picks None itself; the other device's later PGS pick is not
	// what Plex Web shows.
	if rec := f.request(t, "PUT", "/library/parts/"+key+"?subtitleStreamID=0&allParts=1", headerMap(web...)); rec.Code != 200 {
		t.Fatalf("select None: %d", rec.Code)
	}
	putSubtitle(t, f, key, pgsID)
	if sub, row := decide("s5", ""); sub != -1 || row != "" {
		t.Fatalf("PGS burned after this client picked None: subtitle stream %d, row %q", sub, row)
	}
}
