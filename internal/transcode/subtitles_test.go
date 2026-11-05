package transcode

import (
	"strings"
	"testing"
	"time"
)

func TestProgressParser(t *testing.T) {
	var p progressParser
	lines := []string{"frame=240", "fps=48.5", "bitrate=1234.5kbits/s", "out_time_us=10000000", "speed=2.5x", "progress=continue"}
	var got Progress
	var ok bool
	for _, l := range lines {
		got, ok = p.line(l)
	}
	if !ok || got.Frame != 240 || got.FPS != 48.5 || got.OutTime != 10*time.Second || got.Speed != 2.5 || got.Ended {
		t.Errorf("got %+v ok=%v", got, ok)
	}
	if got, ok = p.line("progress=end"); !ok || !got.Ended {
		t.Errorf("end: %+v", got)
	}
}

func TestLineWriter(t *testing.T) {
	var lines []string
	w := &lineWriter{fn: func(s string) { lines = append(lines, s) }}
	_, _ = w.Write([]byte("a=1\nb="))
	_, _ = w.Write([]byte("2\r\n\nc=3\r"))
	if strings.Join(lines, ",") != "a=1,b=2,c=3" {
		t.Errorf("lines %q", lines)
	}
	tb := newTailBuffer(2)
	tb.add("1")
	tb.add("2")
	tb.add("3")
	if tb.String() != "2\n3" {
		t.Errorf("tail %q", tb.String())
	}
}
