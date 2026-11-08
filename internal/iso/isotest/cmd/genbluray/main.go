// Command genbluray writes the small Blu-ray disc image used by the encrypted
// disc fixtures (see internal/archive/testdata/gen-iso7z.sh) and the main
// feature's bytes next to it. Its output is deterministic.
package main

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"

	"github.com/braedonsaunders/ether/internal/iso/isotest"
)

func randBytes(seed uint64, n int) []byte {
	r := rand.New(rand.NewPCG(seed, 99))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: genbluray <dir>")
		os.Exit(2)
	}
	c1, c2 := randBytes(1, 90_000), randBytes(2, 60_000)
	image := isotest.Build("ENCRYPTED_DISC", []isotest.File{
		{Path: "BDMV/PLAYLIST/00000.mpls", Data: isotest.MPLS([]isotest.PlayItem{{Clip: "00001", In: 0, Out: 450000}, {Clip: "00002", In: 0, Out: 270000}})},
		{Path: "BDMV/STREAM/00001.m2ts", Data: c1},
		{Path: "BDMV/STREAM/00002.m2ts", Data: c2},
	})
	for name, data := range map[string][]byte{"disc.iso": image, "feature.bin": append(append([]byte{}, c1...), c2...)} {
		if err := os.WriteFile(filepath.Join(os.Args[1], name), data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
