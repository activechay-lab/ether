// Package archivex lists and reads members of zip-shaped containers
// (zip, cbz, epub) with the standard library only, so hosted builds
// stay possible. RAR/7z listing is Ether's side of the container
// pattern; plugins declare members, Ether extracts bytes.
package archivex

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Member is one non-directory entry in archive order.
type Member struct {
	Index int // position among non-directory members, archive order
	Name  string
	Size  int64
}

// List returns the non-directory members of a zip-shaped archive.
func List(data []byte) ([]Member, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var out []Member
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			continue
		}
		out = append(out, Member{Index: len(out), Name: f.Name, Size: int64(f.UncompressedSize64)})
	}
	return out, nil
}

// ListNatural is List sorted the way readers order pages (1, 2, 10).
func ListNatural(data []byte) ([]Member, error) {
	ms, err := List(data)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(ms, func(i, j int) bool { return NaturalLess(ms[i].Name, ms[j].Name) })
	for i := range ms {
		ms[i].Index = i
	}
	return ms, nil
}

// ReadMember returns one member's bytes by exact name.
func ReadMember(data []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("archivex: member %q not found", name)
}

// ReadMemberAt returns the nth non-directory member's bytes (0-based).
func ReadMemberAt(data []byte, index int) ([]byte, Member, error) {
	ms, err := List(data)
	if err != nil {
		return nil, Member{}, err
	}
	if index < 0 || index >= len(ms) {
		return nil, Member{}, fmt.Errorf("archivex: member index %d out of range (%d members)", index, len(ms))
	}
	b, err := ReadMember(data, ms[index].Name)
	return b, ms[index], err
}

// NaturalLess compares names with embedded numbers compared numerically.
func NaturalLess(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		ac, bc := a[ai], b[bi]
		if isDigit(ac) && isDigit(bc) {
			an, bn := 0, 0
			for ai < len(a) && isDigit(a[ai]) {
				an = an*10 + int(a[ai]-'0')
				ai++
			}
			for bi < len(b) && isDigit(b[bi]) {
				bn = bn*10 + int(b[bi]-'0')
				bi++
			}
			if an != bn {
				return an < bn
			}
			continue
		}
		if ac != bc {
			return ac < bc
		}
		ai++
		bi++
	}
	return len(a)-ai < len(b)-bi
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
