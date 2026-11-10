package stream

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/segcache"
)

// nestedPar2Volumes builds the deterministic outer volumes testdata/nested-par2
// protects: a 120000-byte video in a two-volume RAR5 set, packed in a RAR5 set
// of 100000-byte volumes. See testdata/nested-par2/README.md.
func nestedPar2Volumes(t testing.TB) (video []byte, outer [][]byte) {
	t.Helper()
	video = mkvBytes(101, 120_000)
	inner := buildRAR(t, []archivetest.RARFile{{Name: "Movie.mkv", Data: video}},
		archivetest.RAROptions{Version: 5, VolumeSize: 70_000, NewNumbering: true})
	var files []archivetest.RARFile
	for i, v := range inner {
		files = append(files, archivetest.RARFile{Name: newStyle("Movie")(i), Data: v})
	}
	return video, buildRAR(t, files, archivetest.RAROptions{Version: 5, VolumeSize: 100_000, NewNumbering: true})
}

// Run with ETHER_WRITE_NESTED_PAR2=<dir> to write the volumes the fixture's
// par2 command is run on.
func TestWriteNestedPar2Volumes(t *testing.T) {
	dir := os.Getenv("ETHER_WRITE_NESTED_PAR2")
	if dir == "" {
		t.Skip("fixture generator")
	}
	_, a := nestedPar2Volumes(t)
	_, b := nestedPar2Volumes(t)
	for i, v := range a {
		if !bytes.Equal(v, b[i]) {
			t.Fatalf("volume %d is not deterministic", i)
		}
		if err := os.WriteFile(filepath.Join(dir, newStyle("release")(i)), v, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("wrote %d volumes", len(a))
}

func nestedPar2Post(t *testing.T) (*fakeFetcher, []byte, []byte) {
	t.Helper()
	video, outer := nestedPar2Volumes(t)
	index, err := os.ReadFile("testdata/nested-par2/nested.par2")
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := os.ReadFile("testdata/nested-par2/nested.vol0+8.par2")
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeFetcher()
	doc := postVolumes(t, f, 5120, outer, newStyle("release"),
		postFile{name: "nested.par2", data: index},
		postFile{name: "nested.vol0+8.par2", data: recovery})
	return f, doc, video
}

// The PAR2 set protects the outer volumes. An article lost from the video's
// bytes inside them is rebuilt from parity, so the nested video reads whole.
func TestNestedRARRepairFromPar2(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		part int
	}{
		{"first-volume-early", "release.part01.rar", 5},
		{"first-volume-late", "release.part01.rar", 18},
		{"second-volume", "release.part02.rar", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, doc, want := nestedPar2Post(t)
			f.setMissing(postID(tc.file, tc.part))
			cache, err := segcache.Open(t.TempDir(), 1<<20, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cache.Close()
			e := newTestEngine(t, f, cache, repairOpts())
			src := openSource(t, e, "nested-repair", doc)
			if src.Size() != int64(len(want)) {
				t.Fatalf("size %d", src.Size())
			}
			got := make([]byte, len(want))
			n, err := src.ReadAt(got, 0)
			if err != nil || n != len(want) || !bytes.Equal(got, want) {
				t.Fatalf("n=%d err=%v exact=%v stats=%+v", n, err, bytes.Equal(got, want), e.Stats())
			}
			if e.Stats().RepairSuccesses != 1 {
				t.Fatalf("stats %+v", e.Stats())
			}
		})
	}
}

func TestNestedRARPar2IntactMatchesFixture(t *testing.T) {
	f, doc, want := nestedPar2Post(t)
	e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
	if ok, err := e.Check(t.Context(), doc); !ok || err != nil {
		t.Fatalf("Check = %v %v", ok, err)
	}
	src := openSource(t, e, "nested-par2", doc)
	checkRandomReads(t, src, want, 3, 40)
	if st := e.Stats(); st.Par2Verified != 1 || st.Par2Mismatches != 0 {
		t.Fatalf("stats %+v (fixture out of date?)", st)
	}
}

func TestNestedRARRepairInsufficient(t *testing.T) {
	f, doc, want := nestedPar2Post(t)
	// More lost slices than recovery blocks: the read must fail, not conceal.
	for part := 1; part <= 20; part++ {
		f.setMissing(postID("release.part01.rar", part))
	}
	e := newTestEngine(t, f, nil, repairOpts())
	src, err := e.Open(t.Context(), "nested-lost", doc)
	if err != nil {
		return // lost inside the headers: refused at Open, also correct
	}
	defer src.Close()
	got := make([]byte, len(want))
	if _, err := src.ReadAt(got, 0); err == nil {
		t.Fatal("read succeeded over unrecoverable damage")
	}
}
