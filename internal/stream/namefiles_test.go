package stream

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/par2/par2test"
)

func TestNameFilesWritesPar2NamesOfObfuscatedPack(t *testing.T) {
	f := newFakeFetcher()
	ep1 := mkvBytes(51, 400_000)
	ep2 := mkvBytes(52, 450_000)
	idx := par2Index(16_384,
		par2test.File{Name: "Show.S01E01.1080p.mkv", Data: ep1},
		par2test.File{Name: "Show.S01E02.1080p.mkv", Data: ep2},
	)
	doc := post(t, f, 100_000,
		postFile{name: obfName(1), data: ep1},
		postFile{name: obfName(2), data: ep2},
		postFile{name: "d41d8cd98f00b204e9800998ecf8427e.par2", data: idx},
	)
	e := newTestEngine(t, f, nil, Options{})
	out, err := e.NameFiles(context.Background(), "pack", doc)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := nzb.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, file := range parsed.Files {
		got[file.Name] = true
	}
	if !got["Show.S01E01.1080p.mkv"] || !got["Show.S01E02.1080p.mkv"] {
		t.Fatalf("names = %v", got)
	}
}
