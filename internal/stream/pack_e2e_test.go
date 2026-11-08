package stream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/par2/par2test"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// packIndexer serves one fixed NZB, like an indexer returning a season pack.
type packIndexer struct {
	core.Indexer
	nzb []byte
}

func (p packIndexer) FetchNZB(context.Context, core.Release) ([]byte, error) { return p.nzb, nil }

// TestSeasonPackEndToEnd runs a Usenet season pack through the whole path an
// episode request takes, against the in-memory article fetcher: the indexer
// returns the pack NZB, debrid.Indexer narrows it to the requested episode
// (naming obfuscated files from PAR2 first), and the stream engine checks,
// opens and reads it. It asserts the right episode's bytes are served and
// that PAR2 verification ran. Synthetic: no provider or real pack involved.
func TestSeasonPackEndToEnd(t *testing.T) {
	eps := [][]byte{mkvBytes(71, 1_200_000), mkvBytes(72, 1_300_000), mkvBytes(73, 1_250_000)}
	real := []string{"Show.S01E01.1080p.mkv", "Show.S01E02.1080p.mkv", "Show.S01E03.1080p.mkv"}
	idx := par2Index(16_384,
		par2test.File{Name: real[0], Data: eps[0]},
		par2test.File{Name: real[1], Data: eps[1]},
		par2test.File{Name: real[2], Data: eps[2]},
	)
	for _, tc := range []struct {
		name       string
		obfuscated bool
	}{{"plain pack", false}, {"obfuscated pack", true}} {
		t.Run(tc.name, func(t *testing.T) {
			const want = 2 // episode requested
			f := newFakeFetcher()
			var files []postFile
			for i, d := range eps {
				name := real[i]
				if tc.obfuscated {
					name = obfName(i + 1)
				}
				files = append(files, postFile{name: name, data: d})
			}
			indexName := "Show.S01.par2"
			if tc.obfuscated {
				indexName = "d41d8cd98f00b204e9800998ecf8427e.par2"
			}
			files = append(files, postFile{name: indexName, data: idx}, postFile{name: "Show.S01.vol000+01.par2", data: randBytes(9, 120_000)})
			pack := post(t, f, 100_000, files...)

			e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
			ix := debrid.NewIndexer(packIndexer{nzb: pack}, nil)
			ix.SetPackNamer(e)
			name := "Show.S01.1080p.WEB-DL.COMPLETE"
			rel := core.Release{ID: "pack", Name: name, Info: releaseparse.Parse(name), Protocol: core.ProtocolUsenet,
				Target: &core.ReleaseTarget{Season: 1, Episode: want}}
			if !rel.Info.FullSeason {
				t.Fatalf("fixture is not a season pack: %+v", rel.Info)
			}
			doc, err := ix.FetchNZB(context.Background(), rel)
			if err != nil {
				t.Fatalf("FetchNZB: %v", err)
			}
			if ok, err := e.Check(context.Background(), doc); !ok || err != nil {
				t.Fatalf("Check = %v %v", ok, err)
			}
			src := openSource(t, e, "pack-e2", doc)
			if src.Name() != real[want-1] || src.Size() != int64(len(eps[want-1])) {
				t.Fatalf("played %q (%d bytes), want %q (%d bytes)", src.Name(), src.Size(), real[want-1], len(eps[want-1]))
			}
			got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
			if err != nil || !bytes.Equal(got, eps[want-1]) {
				t.Fatalf("content differs from episode %d: %v", want, err)
			}
			st := e.Stats()
			if st.Par2Loaded < 1 || st.Par2Verified < 1 || st.Par2Mismatches != 0 {
				t.Fatalf("verification did not run cleanly: %+v", st)
			}
			if tc.obfuscated && st.Par2Named < 1 {
				t.Fatalf("obfuscated pack was not named from PAR2: %+v", st)
			}
		})
	}
}

// A season posted as one RAR set holds every episode as a member, and the set's
// name (often obfuscated) says nothing about them. The episode NZB keeps the
// set whole and hints the wanted episode; the engine plays that member.
func TestSeasonInOneArchiveEndToEnd(t *testing.T) {
	members := []archivetest.RARFile{
		{Name: "Show 1x01 First (GRP).mkv", Data: mkvBytes(81, 700_000)},
		{Name: "Show 1x02 Second (GRP).mkv", Data: mkvBytes(82, 900_000)},
		{Name: "Show 1x03 Third (GRP).mkv", Data: mkvBytes(83, 800_000)},
	}
	vols := buildRAR(t, members, archivetest.RAROptions{Version: 5, VolumeSize: 300_000})
	var files []postFile
	for k, v := range vols {
		files = append(files, postFile{name: fmt.Sprintf("q9Zr7Tb2Wc8Hn4Jd.part%03d.rar", k+1), data: v})
	}
	pack := post(t, newFakeFetcher(), 50_000, files...) // names only; articles are posted per case below

	for _, tc := range []struct {
		episode int
		want    int // index into members, -1 for none
	}{{1, 0}, {2, 1}, {3, 2}, {9, -1}} {
		t.Run(fmt.Sprintf("episode %d", tc.episode), func(t *testing.T) {
			f := newFakeFetcher()
			post(t, f, 50_000, files...)
			doc, err := debrid.EpisodeNZB(pack, &core.ReleaseTarget{Season: 1, Episode: tc.episode})
			if err != nil {
				t.Fatalf("EpisodeNZB: %v", err)
			}
			e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
			if tc.want < 0 {
				if _, err := e.Open(context.Background(), "season", doc); !errors.Is(err, ErrNoPlayableFile) {
					t.Fatalf("Open = %v, want ErrNoPlayableFile (never another episode)", err)
				}
				return
			}
			src := openSource(t, e, "season", doc)
			want := members[tc.want]
			if src.Name() != want.Name || src.Size() != int64(len(want.Data)) {
				t.Fatalf("played %q (%d bytes), want %q (%d bytes)", src.Name(), src.Size(), want.Name, len(want.Data))
			}
			got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
			if err != nil || !bytes.Equal(got, want.Data) {
				t.Fatalf("content differs from the member: %v", err)
			}
		})
	}
}
