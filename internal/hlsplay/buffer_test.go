package hlsplay

import "testing"

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
