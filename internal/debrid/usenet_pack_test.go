package debrid

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
)

func packNZB(subjects ...string) []byte {
	var b strings.Builder
	b.WriteString(`<nzb><head><meta type="password">pw</meta></head>`)
	for i, s := range subjects {
		fmt.Fprintf(&b, `<file poster="p" date="1" subject="%s"><groups><group>a.b</group></groups><segments><segment bytes="100" number="1">id%d@x</segment></segments></file>`, s, i)
	}
	b.WriteString(`</nzb>`)
	return []byte(b.String())
}

func TestEpisodeNZBKeepsOnlyWantedEpisode(t *testing.T) {
	data := packNZB(
		`&quot;Ted.Lasso.S03E01.1080p.WEB-DL.x264.mkv&quot; yEnc (1/1)`,
		`&quot;Ted.Lasso.S03E02.1080p.WEB-DL.x264.mkv&quot; yEnc (1/1)`,
		`&quot;Ted.Lasso.S03E02.1080p.WEB-DL.x264.par2&quot; yEnc (1/1)`,
		`&quot;Ted.Lasso.S03E01.1080p.WEB-DL.x264.par2&quot; yEnc (1/1)`,
	)
	out, err := EpisodeNZB(data, &core.ReleaseTarget{Season: 3, Episode: 2})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := nzb.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Files) != 2 || !strings.Contains(doc.Files[0].Name, "S03E02") || !strings.Contains(doc.Files[1].Name, "S03E02") {
		t.Fatalf("files = %v", doc.Files)
	}
	if doc.Password() != "pw" {
		t.Fatalf("password lost: %q", doc.Password())
	}
}

func TestEpisodeNZBKeepsArchiveSet(t *testing.T) {
	data := packNZB(
		`&quot;ted.lasso.s03e01.part1.rar&quot; yEnc (1/1)`,
		`&quot;ted.lasso.s03e01.part2.rar&quot; yEnc (1/1)`,
		`&quot;ted.lasso.s03e02.part1.rar&quot; yEnc (1/1)`,
	)
	out, err := EpisodeNZB(data, &core.ReleaseTarget{Season: 3, Episode: 1})
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := nzb.Parse(out)
	if len(doc.Files) != 2 {
		t.Fatalf("want 2 volumes, got %d", len(doc.Files))
	}
}

func TestEpisodeNZBMissingEpisode(t *testing.T) {
	data := packNZB(`&quot;Ted.Lasso.S03E01.1080p.mkv&quot; yEnc (1/1)`)
	if _, err := EpisodeNZB(data, &core.ReleaseTarget{Season: 3, Episode: 5}); err == nil {
		t.Fatal("expected error")
	}
}

type fakeNamer struct{ out []byte }

func (n fakeNamer) NameFiles(context.Context, string, []byte) ([]byte, error) { return n.out, nil }

func TestEpisodeNZBNamesObfuscatedPackFirst(t *testing.T) {
	big := func(subject string, id int) string {
		return fmt.Sprintf(`<file poster="p" date="1" subject="%s"><groups><group>a.b</group></groups><segments><segment bytes="2000000" number="1">id%d@x</segment></segments></file>`, subject, id)
	}
	obf := []byte("<nzb>" + big("&quot;q01X9k3Pq9LmZr7Tb2Wc8Hn4Jd&quot; yEnc (1/1)", 1) + big("&quot;q02X9k3Pq9LmZr7Tb2Wc8Hn4Jd&quot; yEnc (1/1)", 2) + "</nzb>")
	named := []byte("<nzb>" + big("&quot;Show.S01E01.mkv&quot; yEnc (1/1)", 1) + big("&quot;Show.S01E02.mkv&quot; yEnc (1/1)", 2) + "</nzb>")
	x := &Indexer{packNamer: fakeNamer{named}}
	out, err := x.episodeNZB(context.Background(), core.Release{ID: "r", Target: &core.ReleaseTarget{Season: 1, Episode: 2}}, obf)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := nzb.Parse(out)
	if len(doc.Files) != 1 || doc.Files[0].Name != "Show.S01E02.mkv" {
		t.Fatalf("files = %+v", doc.Files)
	}
	// Without a namer the obfuscated pack cannot be narrowed.
	if _, err := (&Indexer{}).episodeNZB(context.Background(), core.Release{Target: &core.ReleaseTarget{Season: 1, Episode: 2}}, obf); err == nil {
		t.Fatal("expected error")
	}
}
