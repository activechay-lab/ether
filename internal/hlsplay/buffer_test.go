package hlsplay

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBufferBlockedFetchAndResume(t *testing.T) {
	b := Buffer{}
	b.Add(2, 4)
	b.Advance(5)
	if b.Played != 0 || len(b.Stalls) != 0 {
		t.Fatal("startup counted as playback")
	}
	b.Started = true
	b.Advance(11)
	if b.Played != 4 || b.Level != 0 || len(b.Stalls) != 1 || b.Stalls[0].DurationSeconds != 2 {
		t.Fatalf("blocked fetch: %+v", b)
	}
	b.Add(13, 4)
	b.Advance(14)
	if b.Stalls[0].DurationSeconds != 4 || b.Level != 3 || b.Played != 5 {
		t.Fatalf("resume: %+v", b)
	}
	b.Advance(19)
	if len(b.Stalls) != 2 || b.Stalls[1].DurationSeconds != 2 {
		t.Fatalf("terminal stall: %+v", b)
	}
}
func TestPlaylistSequenceAndOrigin(t *testing.T) {
	p, e := parsePlaylist("http://lab/media/index.m3u8", []byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:17\n#EXTINF:4,\n00017.ts?grant=opaque\n#EXTINF:4,\n00018.ts\n#EXT-X-ENDLIST\n"))
	if e != nil || len(p.entries) != 2 || p.entries[1].index != 18 || !p.end {
		t.Fatalf("%+v %v", p, e)
	}
	if _, e = resolve("http://lab/index.m3u8", "http://elsewhere/media.ts"); e == nil {
		t.Fatal("cross-origin credentials allowed")
	}
}

func TestInitialTimelineCannotBeRewrittenByReplacement(t *testing.T) {
	dir := t.TempDir()
	p := player{c: Config{Output: dir}, control: barrierControl{}}
	r := Report{}
	initial := playlist{entries: []entry{{index: 0, duration: 2}, {index: 1, duration: 2}, {index: 2, duration: 4}}}
	if e := p.captureInitial(&r, initial, []byte("initial authored plan")); e != nil {
		t.Fatal(e)
	}
	hash := r.InitialPlaylistSHA256
	changed := playlist{entries: []entry{{index: 0, duration: 4}, {index: 1, duration: 4}, {index: 2, duration: 4}}}
	if e := p.captureInitial(&r, changed, []byte("replacement plan")); e != nil {
		t.Fatal(e)
	}
	if r.InitialPlaylistSHA256 != hash || len(r.InitialTimeline) != 4 || r.InitialTimeline[2] != 4*time.Second || r.InitialTimeline[3] != 8*time.Second {
		t.Fatalf("initial timeline changed: %+v", r.InitialTimeline)
	}
	data, e := os.ReadFile(filepath.Join(dir, "initial-variant.private.m3u8"))
	if e != nil || string(data) != "initial authored plan" {
		t.Fatal("initial evidence overwritten", e)
	}
}
