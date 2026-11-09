package iso

import (
	"errors"
	"io"
)

// Bounds for parsing an untrusted image. Every length, count and location in
// a disc image is attacker-controlled, and the reads go through Usenet, so
// nothing sized by the image may be trusted without a ceiling.
const (
	// maxDirBytes caps one directory's bytes. A Blu-ray BDMV/STREAM/ holds
	// ~26 KiB; 8 MiB is orders of magnitude of headroom.
	maxDirBytes = 8 << 20

	// maxWalkDepth, maxWalkDirs and maxWalkFiles bound one filesystem walk.
	maxWalkDepth = 32
	maxWalkDirs  = 16384
	maxWalkFiles = 65536

	// maxFileExtents bounds the allocation descriptors collected for one file.
	maxFileExtents = 65536

	// maxVDSSectors bounds the Volume Descriptor Sequence scan.
	maxVDSSectors = 256

	// listReadBudget is the total number of bytes one Analyze may read: the
	// filesystem walk, the volume label and the playlists together. It also
	// bounds Allocation Extent Descriptor chains that loop.
	listReadBudget = 64 << 20

	// maxPlaylistBytes skips a playlist larger than this; real MPLS files are
	// a few KiB. maxPlaylistTotalBytes caps all playlist reads of one disc.
	maxPlaylistBytes      = 1 << 20
	maxPlaylistTotalBytes = 16 << 20
)

// errReadBudget is returned once an Analyze has read its whole budget.
var errReadBudget = errors.New("iso: read budget exceeded")

// budgetReader fails every read after left bytes have been served.
type budgetReader struct {
	rs   io.ReadSeeker
	left int64
}

func newBudgetReader(rs io.ReadSeeker, budget int64) *budgetReader {
	return &budgetReader{rs: rs, left: budget}
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errReadBudget
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rs.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *budgetReader) Seek(off int64, whence int) (int64, error) {
	return b.rs.Seek(off, whence)
}

// walker tracks one directory walk: the directories already entered (keyed by
// on-disc location) and the running directory and file counts.
type walker struct {
	seen  map[uint64]struct{}
	dirs  int
	files int
}

func newWalker() *walker { return &walker{seen: make(map[uint64]struct{})} }

// enter reports whether the directory at key may be walked at depth. It is
// false for a directory already visited (a loop or a shared directory), too
// deep, or past the directory cap.
func (w *walker) enter(key uint64, depth int) bool {
	if depth > maxWalkDepth || w.dirs >= maxWalkDirs {
		return false
	}
	if _, dup := w.seen[key]; dup {
		return false
	}
	w.seen[key] = struct{}{}
	w.dirs++
	return true
}

// addFile reports whether another file fits under the file cap.
func (w *walker) addFile() bool {
	if w.files >= maxWalkFiles {
		return false
	}
	w.files++
	return true
}
