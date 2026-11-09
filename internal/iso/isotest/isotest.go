// Package isotest builds ISO 9660 images and Blu-ray playlists for tests.
package isotest

import (
	"encoding/binary"
	"path"
	"sort"
	"strings"
)

const sector = 2048

// File is one file of the image, with '/'-separated directories in Path.
type File struct {
	Path string
	Data []byte
}

type node struct {
	name     string
	dir      bool
	children []*node
	data     []byte
	lba      uint32
	size     uint32
}

// Build returns an ISO 9660 image holding files, with volume identifier
// label. Every file is one contiguous extent; names are stored as given.
func Build(label string, files []File) []byte {
	root := &node{dir: true}
	for _, f := range files {
		cur := root
		parts := strings.Split(f.Path, "/")
		for i, p := range parts {
			last := i == len(parts)-1
			var next *node
			for _, c := range cur.children {
				if c.name == p && c.dir == !last {
					next = c
				}
			}
			if next == nil {
				next = &node{name: p, dir: !last}
				if last {
					next.data = f.Data
				}
				cur.children = append(cur.children, next)
			}
			cur = next
		}
	}
	var dirs, regular []*node
	var walk func(n *node)
	walk = func(n *node) {
		sort.SliceStable(n.children, func(i, j int) bool { return n.children[i].name < n.children[j].name })
		dirs = append(dirs, n)
		for _, c := range n.children {
			if c.dir {
				walk(c)
			} else {
				regular = append(regular, c)
			}
		}
	}
	walk(root)

	next := uint32(18)
	for _, d := range dirs {
		d.lba = next
		d.size = uint32(len(dirRecords(d, d, d)))
		next += d.size / sector
	}
	for _, f := range regular {
		f.lba = next
		f.size = uint32(len(f.data))
		next += (f.size + sector - 1) / sector
	}
	img := make([]byte, int(next)*sector)

	pvd := img[16*sector:]
	pvd[0] = 1
	copy(pvd[1:6], "CD001")
	pvd[6] = 1
	copy(pvd[40:72], []byte(label + strings.Repeat(" ", 32))[:32])
	both32(pvd[80:88], next)
	both16(pvd[120:124], 1)
	both16(pvd[124:128], 1)
	both16(pvd[128:132], sector)
	copy(pvd[156:190], record(root, "\x00", root.lba, root.size, true))
	term := img[17*sector:]
	term[0] = 255
	copy(term[1:6], "CD001")
	term[6] = 1

	parent := map[*node]*node{root: root}
	for _, d := range dirs {
		for _, c := range d.children {
			if c.dir {
				parent[c] = d
			}
		}
	}
	for _, d := range dirs {
		copy(img[int(d.lba)*sector:], dirRecords(d, d, parent[d]))
	}
	for _, f := range regular {
		copy(img[int(f.lba)*sector:], f.data)
	}
	return img
}

func both32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b[0:4], v)
	binary.BigEndian.PutUint32(b[4:8], v)
}

func both16(b []byte, v uint16) {
	binary.LittleEndian.PutUint16(b[0:2], v)
	binary.BigEndian.PutUint16(b[2:4], v)
}

// record encodes one directory record.
func record(n *node, name string, lba, size uint32, dir bool) []byte {
	n2 := len(name)
	l := 33 + n2
	if l%2 == 1 {
		l++
	}
	r := make([]byte, l)
	r[0] = byte(l)
	both32(r[2:10], lba)
	both32(r[10:18], size)
	if dir {
		r[25] = 2
	}
	both16(r[28:32], 1)
	r[32] = byte(n2)
	copy(r[33:], name)
	return r
}

// dirRecords lays out a directory's records, never crossing a sector, and
// pads the result to whole sectors.
func dirRecords(d, self, parent *node) []byte {
	var out []byte
	add := func(r []byte) {
		if len(out)%sector+len(r) > sector {
			out = append(out, make([]byte, sector-len(out)%sector)...)
		}
		out = append(out, r...)
	}
	add(record(self, "\x00", self.lba, self.size, true))
	add(record(parent, "\x01", parent.lba, parent.size, true))
	for _, c := range d.children {
		name := c.name
		if !c.dir {
			name += ";1"
		}
		add(record(c, name, c.lba, c.size, c.dir))
	}
	if len(out)%sector != 0 || len(out) == 0 {
		out = append(out, make([]byte, sector-len(out)%sector)...)
	}
	return out
}

// PlayItem is one clip of a playlist, with IN and OUT times at 45 kHz.
type PlayItem struct {
	Clip    string // five digits, e.g. "00001"
	In, Out uint32
}

// MPLS returns a Blu-ray playlist file listing items.
func MPLS(items []PlayItem) []byte {
	var body []byte
	for _, it := range items {
		b := make([]byte, 20)
		copy(b[0:5], it.Clip)
		copy(b[5:9], "M2TS")
		binary.BigEndian.PutUint32(b[12:16], it.In)
		binary.BigEndian.PutUint32(b[16:20], it.Out)
		l := make([]byte, 2)
		binary.BigEndian.PutUint16(l, uint16(len(b)))
		body = append(body, l...)
		body = append(body, b...)
	}
	pl := make([]byte, 10)
	binary.BigEndian.PutUint32(pl[0:4], uint32(6+len(body)))
	binary.BigEndian.PutUint16(pl[6:8], uint16(len(items)))
	pl = append(pl, body...)
	hdr := make([]byte, 40)
	copy(hdr[0:4], "MPLS")
	copy(hdr[4:8], "0200")
	binary.BigEndian.PutUint32(hdr[8:12], 40)
	return append(hdr, pl...)
}

// Base returns the file name of p.
func Base(p string) string { return path.Base(p) }
