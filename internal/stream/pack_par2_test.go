package stream

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/par2/par2test"
)

// A pack's PAR2 index lists every episode. The episode NZB cut from it keeps
// that index, and the integrity check looks up only the stream's own file.
func TestEpisodeCutFromPackKeepsPar2Verification(t *testing.T) {
	ep1 := mkvBytes(61, 400_000)
	ep2 := mkvBytes(62, 450_000)
	ep3 := mkvBytes(63, 420_000)
	idx := par2Index(16_384,
		par2test.File{Name: "Show.S01E01.1080p.mkv", Data: ep1},
		par2test.File{Name: "Show.S01E02.1080p.mkv", Data: ep2},
		par2test.File{Name: "Show.S01E03.1080p.mkv", Data: ep3},
	)
	cut := func(t *testing.T, f *fakeFetcher, posted2 []byte) []byte {
		t.Helper()
		pack := post(t, f, 100_000,
			postFile{name: "Show.S01E01.1080p.mkv", data: ep1},
			postFile{name: "Show.S01E02.1080p.mkv", data: posted2},
			postFile{name: "Show.S01E03.1080p.mkv", data: ep3},
			postFile{name: "Show.S01.par2", data: idx},
			postFile{name: "Show.S01.vol000+01.par2", data: randBytes(9, 120_000)},
		)
		out, err := debrid.EpisodeNZB(pack, &core.ReleaseTarget{Season: 1, Episode: 2})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	t.Run("right episode verifies", func(t *testing.T) {
		f := newFakeFetcher()
		doc := cut(t, f, ep2)
		e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
		ok, err := e.Check(context.Background(), doc)
		if !ok || err != nil {
			t.Fatalf("Check = %v %v", ok, err)
		}
		src := openSource(t, e, "ep2", doc)
		if src.Name() != "Show.S01E02.1080p.mkv" || src.Size() != int64(len(ep2)) {
			t.Fatalf("name %q size %d", src.Name(), src.Size())
		}
		if st := e.Stats(); st.Par2Loaded != 1 || st.Par2Verified != 1 || st.Par2Mismatches != 0 {
			t.Fatalf("stats %+v", st)
		}
	})

	t.Run("corrupted head is rejected", func(t *testing.T) {
		f := newFakeFetcher()
		bad := bytes.Clone(ep2)
		bad[1000] ^= 0x55 // what the poster hashed differs in the first 16 KiB
		doc := cut(t, f, bad)
		e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
		ok, err := e.Check(context.Background(), doc)
		if ok || !errors.Is(err, ErrPar2Mismatch) || !IsRejection(err) {
			t.Fatalf("Check = %v %v", ok, err)
		}
		if _, err := e.Open(context.Background(), "ep2", doc); !errors.Is(err, ErrPar2Mismatch) {
			t.Fatalf("Open = %v", err)
		}
	})
}
