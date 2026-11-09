package transcode

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestDASHTrackCompaction(t *testing.T) {
	box := func(kind string, p []byte) []byte {
		b := make([]byte, 8+len(p))
		binary.BigEndian.PutUint32(b, uint32(len(b)))
		copy(b[4:], kind)
		copy(b[8:], p)
		return b
	}
	u32 := func(v ...uint32) []byte {
		b := make([]byte, 4*len(v))
		for i, n := range v {
			binary.BigEndian.PutUint32(b[i*4:], n)
		}
		return b
	}
	// Video has two explicit-size runs. Audio uses its tfhd default size.
	traf := func(id uint32, runs ...[]byte) []byte {
		p := box("tfhd", u32(0x20010, id, 3))
		for _, run := range runs {
			p = append(p, box("trun", run)...)
		}
		return box("traf", p)
	}
	v := traf(1, u32(0x201, 1, 0, 4), u32(0x201, 1, 0, 2))
	a := traf(2, u32(1, 2, 0))
	moof := box("moof", append(v, a...))
	kids, _ := readBoxes(moof, 8, len(moof))
	off := len(moof) + 8
	for i, k := range kids {
		runs, _ := readBoxes(moof, k.start+k.hdr, k.end)
		n := 0
		for _, run := range runs {
			if run.typ != "trun" {
				continue
			}
			p := run.payload(moof)
			delta := 0
			if i == 0 && n == 1 {
				delta = 7
			}
			if i == 1 {
				delta = 4
			}
			binary.BigEndian.PutUint32(p[8:], uint32(off+delta))
			n++
		}
	}
	// Video second run overlaps last audio sample deliberately: extraction
	// follows each declared run instead of assuming interleaving or contiguity.
	src := append(moof, box("mdat", []byte("ABCDabcdef"))...)
	original := bytes.Clone(src)
	for _, tt := range []struct {
		id   uint32
		want string
	}{{1, "ABCDde"}, {2, "abcdef"}} {
		got, err := isolateDASHTrack(src, tt.id)
		if err != nil {
			t.Fatal(err)
		}
		top, err := readBoxes(got, 0, len(got))
		if err != nil {
			t.Fatal(err)
		}
		if len(top) != 2 || top[1].typ != "mdat" || string(top[1].payload(got)) != tt.want {
			t.Fatalf("track %d payload %q", tt.id, got)
		}
		kids, _ := readBoxes(got, 8, top[0].end)
		payloadOffset := top[1].start + 8
		for _, k := range kids {
			if k.typ != "traf" {
				continue
			}
			hd, _ := child(got, k, "tfhd")
			if binary.BigEndian.Uint32(hd.payload(got)[4:]) != tt.id {
				t.Fatal("foreign track")
			}
			runs, _ := readBoxes(got, k.start+k.hdr, k.end)
			for _, run := range runs {
				if run.typ != "trun" {
					continue
				}
				p := run.payload(got)
				if int(binary.BigEndian.Uint32(p[8:])) != payloadOffset {
					t.Fatal("wrong data offset")
				}
				if tt.id == 1 {
					payloadOffset += int(binary.BigEndian.Uint32(p[12:]))
				}
			}
		}
	}
	if !bytes.Equal(src, original) {
		t.Fatal("source mutated")
	}
	bad := bytes.Clone(src)
	tf, _ := child(bad, kids[0], "tfhd")
	binary.BigEndian.PutUint32(tf.payload(bad), 1)
	if _, err := isolateDASHTrack(bad, 1); err == nil {
		t.Fatal("accepted absolute base")
	}
	bad = bytes.Clone(src)
	tr, _ := child(bad, kids[0], "trun")
	binary.BigEndian.PutUint32(tr.payload(bad)[8:], 0x7fffffff)
	if _, err := isolateDASHTrack(bad, 1); err == nil {
		t.Fatal("accepted out of range run")
	}
	if _, err := isolateDASHTrack(src[:len(src)-1], 1); err == nil {
		t.Fatal("accepted truncated mdat")
	}
}
