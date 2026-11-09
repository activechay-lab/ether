package subtext

import (
	"strings"
	"testing"
	"time"
)

func TestAcceptsUploadFormat(t *testing.T) {
	for _, f := range []string{"srt", "vtt", "ass", "ssa", "smi", "txt", "psb"} {
		if !AcceptsUploadFormat(f) {
			t.Errorf("AcceptsUploadFormat(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"", "SRT", ".srt", "sub", "webvtt"} {
		if AcceptsUploadFormat(f) {
			t.Errorf("AcceptsUploadFormat(%q) = true, want false", f)
		}
	}
}

func TestParseAndRenderCues(t *testing.T) {
	srt := "1\r\n00:00:01,000 --> 00:00:02,500\r\n<i>Hello</i> &amp; welcome\nLine two\r\n \r\n2\r\n00:00:03,000 --> 00:00:04,000\r\nWorld\r\n"
	cues, err := ParseCues(srt)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 || cues[0].Start != time.Second || cues[0].End != 2500*time.Millisecond {
		t.Fatalf("unexpected cues: %+v", cues)
	}

	vtt := RenderVTT(cues)
	if !strings.HasPrefix(vtt, "WEBVTT\n\n") || !strings.Contains(vtt, "<i>Hello</i> &amp; welcome") {
		t.Fatalf("unexpected VTT:\n%s", vtt)
	}
	roundTrip, err := ParseCues(vtt)
	if err != nil || len(roundTrip) != 2 {
		t.Fatalf("ParseCues(vtt) = %v, %v", roundTrip, err)
	}

	ass := RenderASS(cues)
	if !strings.HasPrefix(ass, ASSHeader) || !strings.Contains(ass, `Dialogue: 0,0:00:01.00,0:00:02.50,Default,,0,0,0,,{\i1}Hello{\i0} & welcome\NLine two`) {
		t.Fatalf("unexpected ASS:\n%s", ass)
	}

	renderedSRT := RenderSRT(cues)
	if !strings.Contains(renderedSRT, "1\n00:00:01,000 --> 00:00:02,500\n") {
		t.Fatalf("unexpected SRT:\n%s", renderedSRT)
	}
}
