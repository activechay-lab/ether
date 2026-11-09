package stream

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
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

// archiveSeason builds a pack NZB from archives of the given member names and
// returns the episode NZB cut for season 3, episode ep, with its fetcher.
func archiveSeason(t *testing.T, names []string, ep int, perEpisodeSets []string, sevenZip bool) (*fakeFetcher, []byte, [][]byte) {
	t.Helper()
	f := newFakeFetcher()
	data := make([][]byte, len(names))
	for i := range names {
		data[i] = mkvBytes(uint64(90+i), 600_000+i*50_000)
	}
	var files []postFile
	switch {
	case sevenZip:
		var ms []archivetest.SevenZipFile
		for i, n := range names {
			ms = append(ms, archivetest.SevenZipFile{Name: n, Data: data[i]})
		}
		for k, v := range cut(archivetest.Build7zCopy(ms, false), 250_000) {
			files = append(files, postFile{name: fmt.Sprintf("q9Zr7Tb2Wc8Hn4Jd.7z.%03d", k+1), data: v})
		}
	case len(perEpisodeSets) > 0:
		for i, n := range names {
			vols := buildRAR(t, []archivetest.RARFile{{Name: n, Data: data[i]}}, archivetest.RAROptions{Version: 5, VolumeSize: 250_000})
			for k, v := range vols {
				files = append(files, postFile{name: fmt.Sprintf("%s.part%03d.rar", perEpisodeSets[i], k+1), data: v})
			}
		}
	default:
		var ms []archivetest.RARFile
		for i, n := range names {
			ms = append(ms, archivetest.RARFile{Name: n, Data: data[i]})
		}
		for k, v := range buildRAR(t, ms, archivetest.RAROptions{Version: 5, VolumeSize: 250_000}) {
			files = append(files, postFile{name: fmt.Sprintf("q9Zr7Tb2Wc8Hn4Jd.part%03d.rar", k+1), data: v})
		}
	}
	pack := post(t, f, 50_000, files...)
	doc, err := debrid.EpisodeNZB(pack, &core.ReleaseTarget{Season: 3, Episode: ep})
	if err != nil {
		t.Fatalf("EpisodeNZB: %v", err)
	}
	return f, doc, data
}

// The member naming styles a season held in archives uses, and the layouts
// around them: each must play the requested episode's own member.
func TestSeasonArchiveScenarios(t *testing.T) {
	for _, tc := range []struct {
		name     string
		members  []string
		sets     []string // one obfuscated set per episode
		sevenZip bool
	}{
		{name: "one set, 3x02 names", members: []string{"Show 3x01 A.mkv", "Show 3x02 B.mkv", "Show 3x03 C.mkv"}},
		{name: "one set, SxxExx in a folder", members: []string{"Show.S03/Show.S03E01.mkv", "Show.S03/Show.S03E02.mkv", "Show.S03/Show.S03E03.mkv"}},
		{name: "one set, bare E02 in a season folder", members: []string{"Temporada 3/E01.mkv", "Temporada 3/E02.mkv", "Temporada 3/E03.mkv"}},
		{name: "one set, classic numbering in a season folder", members: []string{"Season 3/01 - A.mkv", "Season 3/02 - B.mkv", "Season 3/03 - C.mkv"}},
		{name: "one obfuscated set per episode", members: []string{"Show 3x01 A.mkv", "Show 3x02 B.mkv", "Show 3x03 C.mkv"}, sets: []string{"aZ1xQ9wE", "bK2yR8vF", "cL3zS7uG"}},
		{name: "7z season", members: []string{"Show 3x01 A.mkv", "Show 3x02 B.mkv", "Show 3x03 C.mkv"}, sevenZip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, doc, data := archiveSeason(t, tc.members, 2, tc.sets, tc.sevenZip)
			e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
			src := openSource(t, e, "season", doc)
			if src.Name() != path.Base(tc.members[1]) || src.Size() != int64(len(data[1])) {
				t.Fatalf("played %q (%d bytes), want %q (%d bytes)", src.Name(), src.Size(), path.Base(tc.members[1]), len(data[1]))
			}
			if got := readAll(t, src, 32<<10); !bytes.Equal(got, data[1]) {
				t.Fatal("content is not episode 2's")
			}
		})
	}

	// Numbering the members do not state is never guessed: no wrong episode.
	t.Run("unnumbered members are rejected, not guessed", func(t *testing.T) {
		f, doc, _ := archiveSeason(t, []string{"Show 301.mkv", "Show 302.mkv", "Show 303.mkv"}, 2, nil, false)
		e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
		if _, err := e.Open(context.Background(), "season", doc); !errors.Is(err, ErrNoPlayableFile) {
			t.Fatalf("Open = %v, want ErrNoPlayableFile", err)
		}
	})
}

// A larger set that belongs to another episode and is incomplete must not
// reject the release: the wanted episode's set plays.
func TestSeasonOtherEpisodesBrokenSetDoesNotBlock(t *testing.T) {
	names := []string{"Show 3x01 A.mkv", "Show 3x02 B.mkv", "Show 3x03 C.mkv"}
	sets := []string{"aZ1xQ9wE", "bK2yR8vF", "cL3zS7uG"}
	data := make([][]byte, len(names))
	var files []postFile
	for i, n := range names {
		data[i] = mkvBytes(uint64(70+i), 600_000+i*50_000)
		vols := buildRAR(t, []archivetest.RARFile{{Name: n, Data: data[i]}}, archivetest.RAROptions{Version: 5, VolumeSize: 250_000})
		for k, v := range vols {
			if i == 2 && k == 1 {
				continue // the largest set (episode 3) lacks volume 2
			}
			files = append(files, postFile{name: fmt.Sprintf("%s.part%03d.rar", sets[i], k+1), data: v})
		}
	}
	f := newFakeFetcher()
	pack := post(t, f, 50_000, files...)
	doc, err := debrid.EpisodeNZB(pack, &core.ReleaseTarget{Season: 3, Episode: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
	src := openSource(t, e, "season", doc)
	if src.Name() != names[0] || !bytes.Equal(readAll(t, src, 32<<10), data[0]) {
		t.Fatalf("played %q, want %q", src.Name(), names[0])
	}
	// With no healthy set for the wanted episode, the real fault is reported.
	doc, err = debrid.EpisodeNZB(pack, &core.ReleaseTarget{Season: 3, Episode: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Open(context.Background(), "season3", doc); err == nil || errors.Is(err, ErrNoPlayableFile) && !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Open = %v, want the incomplete-set fault", err)
	}
}

// One compressed episode in a season archive must not reject the season when
// the wanted episode is stored.
func TestSeasonArchiveWithAnotherCompressedEpisode(t *testing.T) {
	a, b := mkvBytes(61, 600_000), mkvBytes(62, 650_000)
	vols := buildRAR(t, []archivetest.RARFile{
		{Name: "Show 3x01 A.mkv", Data: a, Method: 3},
		{Name: "Show 3x02 B.mkv", Data: b},
	}, archivetest.RAROptions{Version: 5, VolumeSize: 250_000})
	var files []postFile
	for k, v := range vols {
		files = append(files, postFile{name: fmt.Sprintf("q9Zr7Tb2Wc8Hn4Jd.part%03d.rar", k+1), data: v})
	}
	f := newFakeFetcher()
	pack := post(t, f, 50_000, files...)
	doc, err := debrid.EpisodeNZB(pack, &core.ReleaseTarget{Season: 3, Episode: 2})
	if err != nil {
		t.Fatal(err)
	}
	e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
	src := openSource(t, e, "season", doc)
	if src.Name() != "Show 3x02 B.mkv" || !bytes.Equal(readAll(t, src, 32<<10), b) {
		t.Fatalf("played %q", src.Name())
	}
}

// A cour archive whose members carry the franchise-absolute number plays the
// episode the absolute number names; members numbered from the cour's own
// start are rejected rather than guessed.
func TestSeasonArchiveCourMatchesThroughAbsoluteNumber(t *testing.T) {
	play := func(t *testing.T, members []string, target core.ReleaseTarget) (*fakeFetcher, []byte, [][]byte) {
		t.Helper()
		f := newFakeFetcher()
		data := make([][]byte, len(members))
		var ms []archivetest.RARFile
		for i, n := range members {
			data[i] = mkvBytes(uint64(40+i), 600_000+i*50_000)
			ms = append(ms, archivetest.RARFile{Name: n, Data: data[i]})
		}
		var files []postFile
		for k, v := range buildRAR(t, ms, archivetest.RAROptions{Version: 5, VolumeSize: 250_000}) {
			files = append(files, postFile{name: fmt.Sprintf("Show.Part.2.part%03d.rar", k+1), data: v})
		}
		pack := post(t, f, 50_000, files...)
		doc, err := debrid.EpisodeNZB(pack, &target)
		if err != nil {
			t.Fatal(err)
		}
		return f, doc, data
	}
	for _, target := range []core.ReleaseTarget{
		{Season: 2, Episode: 1, AbsoluteEpisode: 28},
		{Season: 1, Episode: 28, AbsoluteEpisode: 28},
	} {
		t.Run(fmt.Sprintf("S%dE%d abs %d", target.Season, target.Episode, target.AbsoluteEpisode), func(t *testing.T) {
			f, doc, data := play(t, []string{"Show - 27.mkv", "Show - 28.mkv", "Show - 29.mkv"}, target)
			e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
			src := openSource(t, e, "cour", doc)
			if src.Name() != "Show - 28.mkv" || !bytes.Equal(readAll(t, src, 32<<10), data[1]) {
				t.Fatalf("played %q, want Show - 28.mkv", src.Name())
			}
		})
	}
	t.Run("cour-relative members are rejected", func(t *testing.T) {
		f, doc, _ := play(t, []string{"Show - 01.mkv", "Show - 02.mkv", "Show - 03.mkv"}, core.ReleaseTarget{Season: 2, Episode: 1, AbsoluteEpisode: 28})
		e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
		if _, err := e.Open(context.Background(), "cour", doc); !errors.Is(err, ErrNoPlayableFile) {
			t.Fatalf("Open = %v, want ErrNoPlayableFile", err)
		}
	})
}

// A season held in one store-mode ZIP plays the requested episode, not the
// largest member.
func TestSeasonInOneZIPPlaysTheRequestedEpisode(t *testing.T) {
	names := []string{"Show 1x01 A.mkv", "Show 1x02 B.mkv", "Show 1x03 C.mkv"}
	data := make([][]byte, len(names))
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, n := range names {
		data[i] = mkvBytes(uint64(30+i), 600_000+i*50_000)
		w, err := zw.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f := newFakeFetcher()
	pack := post(t, f, 50_000, postFile{name: "q9Zr7Tb2Wc8Hn4Jd.zip", data: buf.Bytes()})
	for ep := 1; ep <= 3; ep++ {
		doc, err := debrid.EpisodeNZB(pack, &core.ReleaseTarget{Season: 1, Episode: ep})
		if err != nil {
			t.Fatal(err)
		}
		e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
		src := openSource(t, e, fmt.Sprintf("zip%d", ep), doc)
		if src.Name() != names[ep-1] || !bytes.Equal(readAll(t, src, 32<<10), data[ep-1]) {
			t.Fatalf("episode %d played %q, want %q", ep, src.Name(), names[ep-1])
		}
	}
}
