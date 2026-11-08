package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestSelectProfileDownload(t *testing.T) {
	q := fixtureQuality()

	// A 4K/HDR Apple TV asking for a download gets the compact mobile
	// profile instead: SDR 1080p, WEB-DL first, MP4-friendly codecs.
	caps := appleTVCaps()
	caps.Download = true
	p := SelectProfile(q, caps, nil)
	if p.MaxHeight != 1080 || p.AllowHDR || p.AllowDolbyVision {
		t.Fatalf("download profile not clamped to SDR 1080p: %+v", p)
	}
	best := func(ids ...string) string {
		top := ids[0]
		for _, id := range ids {
			if p.CustomFormatScores[id] > p.CustomFormatScores[top] {
				top = id
			}
		}
		return top
	}
	if got := best(core.SourceFormat(core.SourceRemux), core.SourceFormat(core.SourceBluRay), core.SourceFormat(core.SourceWEBDL), core.SourceFormat(core.SourceWEBRip)); got != core.SourceFormat(core.SourceWEBDL) {
		t.Fatalf("download sources: %s scores highest, want WEB-DL (%v)", got, p.CustomFormatScores)
	}
	if got := best(core.CodecFormat("hevc"), core.CodecFormat("av1"), core.CodecFormat("h264")); got != core.CodecFormat("h264") {
		t.Fatalf("download codecs: %s scores highest, want h264", got)
	}
	for _, a := range []string{"truehd", "dts", "dtshd"} {
		if p.CustomFormatScores[core.AudioFormat(a)] >= p.CustomFormatScores[core.AudioFormat("ac3")] {
			t.Fatalf("download audio prefers non-MP4-friendly %q: %v", a, p.CustomFormatScores)
		}
	}
	if p.MaxSizeMBPerMin > 80 {
		t.Fatalf("download MaxSizeMBPerMin = %v, want <= 80", p.MaxSizeMBPerMin)
	}

	// A client without HEVC only gets H.264 for downloads.
	caps = webCaps()
	caps.Download = true
	p = SelectProfile(q, caps, nil)
	if p.CustomFormatScores[core.CodecFormat("hevc")] != 0 || p.CustomFormatScores[core.CodecFormat("h264")] <= 0 {
		t.Fatalf("no-HEVC download codec scores = %v", p.CustomFormatScores)
	}

	// The client's requested height (videoResolution) still clamps.
	caps.MaxHeight = 720
	if p = SelectProfile(q, caps, nil); p.MaxHeight != 720 {
		t.Fatalf("download MaxHeight = %d, want 720", p.MaxHeight)
	}

	// Non-download selection is untouched.
	if p = SelectProfile(q, appleTVCaps(), nil); p.Name != "4K" || !p.AllowHDR {
		t.Fatalf("playback profile changed: %+v", p)
	}
}
