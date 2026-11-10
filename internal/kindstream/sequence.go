package kindstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Leaf describes one part/page/chapter in a multi-part Sequence (spec §4.4, §5.1).
type Leaf struct {
	Name string
	MIME string
	Size int64
	Open func(ctx context.Context) (ByteSource, error)
}

type openLeafEntry struct {
	index int
	src   ByteSource
}

// Sequence presents an ordered list of Leaf sources both as discrete addressable
// leaves (Leaf(i)) and as a single concatenated ByteSource (ReadAt) when every
// leaf has a known positive Size (spec §4.4, §5.1).
//
// Sequence opens leaves lazily on demand, holds at most 2 leaves open at a time
// (current + next), and prefetches the next leaf when a read crosses 90% of the
// current leaf.
type Sequence struct {
	leaves    []Leaf
	starts    []int64 // cumulative start offsets (len == len(leaves)+1)
	totalSize int64
	concatOK  bool
	concatErr error
	name      string
	mime      string

	mu     sync.Mutex
	closed bool
	open   []openLeafEntry // at most 2 open leaves, ordered by recency (open[len-1] is newest)
}

// NewSequence constructs a Sequence over leaves.
func NewSequence(leaves []Leaf) *Sequence {
	cp := make([]Leaf, len(leaves))
	copy(cp, leaves)

	starts := make([]int64, len(cp)+1)
	concatOK := len(cp) > 0
	var concatErr error
	var total int64
	for i, l := range cp {
		if l.Size <= 0 {
			concatOK = false
			if concatErr == nil {
				concatErr = fmt.Errorf("kindstream: sequence leaf %d (%q) has unknown size %d; concatenated ReadAt requires known sizes", i, l.Name, l.Size)
			}
		} else {
			total += l.Size
		}
		starts[i+1] = total
	}
	if len(cp) == 0 {
		concatErr = errors.New("kindstream: empty sequence")
	}

	name := "sequence"
	mime := "application/octet-stream"
	if len(cp) > 0 {
		if cp[0].Name != "" {
			name = cp[0].Name
		}
		if cp[0].MIME != "" {
			mime = cp[0].MIME
		}
	}

	return &Sequence{
		leaves:    cp,
		starts:    starts,
		totalSize: total,
		concatOK:  concatOK,
		concatErr: concatErr,
		name:      name,
		mime:      mime,
	}
}

// Len returns the number of leaves in the sequence.
func (s *Sequence) Len() int { return len(s.leaves) }

// Leaf opens (or returns the already-open) ByteSource for leaf i (0-based).
// The returned ByteSource shares the sequence's open-leaf pool; calling Close
// on the returned leaf handle is a no-op (Sequence.Close closes all open leaves).
func (s *Sequence) Leaf(i int) (ByteSource, error) {
	return s.LeafContext(context.Background(), i)
}

// LeafContext is Leaf with an explicit context for opening the leaf on cache miss.
func (s *Sequence) LeafContext(ctx context.Context, i int) (ByteSource, error) {
	if i < 0 || i >= len(s.leaves) {
		return nil, fmt.Errorf("kindstream: leaf index %d out of range [0,%d)", i, len(s.leaves))
	}
	src, err := s.acquireLeaf(ctx, i, -1)
	if err != nil {
		return nil, err
	}
	return &pinnedLeafSource{ByteSource: src}, nil
}

// acquireLeaf returns the open ByteSource for leaf idx, opening it if needed,
// while preserving keepIdx in the 2-slot open pool when possible.
func (s *Sequence) acquireLeaf(ctx context.Context, idx int, keepIdx int) (ByteSource, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("kindstream: sequence closed")
	}
	for pos, e := range s.open {
		if e.index == idx {
			// Move to most-recently-used position.
			if pos == 0 && len(s.open) == 2 {
				s.open[0], s.open[1] = s.open[1], s.open[0]
			}
			src := e.src
			s.mu.Unlock()
			return src, nil
		}
	}
	openFn := s.leaves[idx].Open
	s.mu.Unlock()

	if openFn == nil {
		return nil, fmt.Errorf("kindstream: sequence leaf %d has nil Open callback", idx)
	}
	opened, err := openFn(ctx)
	if err != nil {
		return nil, err
	}

	var toClose []ByteSource
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = opened.Close()
		return nil, errors.New("kindstream: sequence closed")
	}
	// Check if another goroutine opened the same leaf concurrently.
	for pos, e := range s.open {
		if e.index == idx {
			if pos == 0 && len(s.open) == 2 {
				s.open[0], s.open[1] = s.open[1], s.open[0]
			}
			existing := e.src
			s.mu.Unlock()
			_ = opened.Close()
			return existing, nil
		}
	}

	for len(s.open) >= 2 {
		evictPos := 0
		if s.open[0].index == keepIdx && len(s.open) > 1 {
			evictPos = 1
		}
		toClose = append(toClose, s.open[evictPos].src)
		s.open = append(s.open[:evictPos], s.open[evictPos+1:]...)
	}
	s.open = append(s.open, openLeafEntry{index: idx, src: opened})
	s.mu.Unlock()

	for _, old := range toClose {
		_ = old.Close()
	}
	return opened, nil
}

// ReadAt reads len(p) bytes at offset off across the concatenated leaves.
func (s *Sequence) ReadAt(p []byte, off int64) (int, error) {
	if !s.concatOK {
		return 0, s.concatErr
	}
	if off < 0 {
		return 0, fmt.Errorf("kindstream: negative sequence read offset %d", off)
	}
	if off >= s.totalSize {
		if len(p) == 0 {
			return 0, nil
		}
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}

	want := len(p)
	hitEOF := false
	if rem := s.totalSize - off; int64(want) > rem {
		want = int(rem)
		hitEOF = true
	}

	// Find first leaf whose end offset > off.
	leafIdx := sort.Search(len(s.leaves), func(i int) bool {
		return s.starts[i+1] > off
	})

	totalRead := 0
	for totalRead < want && leafIdx < len(s.leaves) {
		leafStart := s.starts[leafIdx]
		leafSize := s.leaves[leafIdx].Size
		relOff := (off + int64(totalRead)) - leafStart
		avail := leafSize - relOff
		chunk := want - totalRead
		if int64(chunk) > avail {
			chunk = int(avail)
		}

		src, err := s.acquireLeaf(context.Background(), leafIdx, leafIdx+1)
		if err != nil {
			return totalRead, err
		}

		n, rerr := src.ReadAt(p[totalRead:totalRead+chunk], relOff)
		totalRead += n

		// Prefetch leafIdx+1 when read crosses 90% of leafIdx.
		endInLeaf := relOff + int64(n)
		threshold := (leafSize * 9) / 10
		if endInLeaf >= threshold && leafIdx+1 < len(s.leaves) {
			_, _ = s.acquireLeaf(context.Background(), leafIdx+1, leafIdx)
		}

		if n < chunk {
			if rerr == nil || errors.Is(rerr, io.EOF) {
				rerr = io.ErrUnexpectedEOF
			}
			return totalRead, rerr
		}
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return totalRead, rerr
		}
		leafIdx++
	}

	if hitEOF || totalRead < len(p) {
		return totalRead, io.EOF
	}
	return totalRead, nil
}

// Size returns the sum of all leaf sizes when known, or 0 if any leaf has
// unknown size.
func (s *Sequence) Size() int64 {
	if !s.concatOK {
		return 0
	}
	return s.totalSize
}

// Name returns the sequence's display name.
func (s *Sequence) Name() string { return s.name }

// MIME returns the sequence's MIME type.
func (s *Sequence) MIME() string { return s.mime }

// OpenLeafCount returns the number of currently open leaves (at most 2).
func (s *Sequence) OpenLeafCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.open)
}

// IsLeafOpen reports whether leaf i is currently open in the sequence pool.
func (s *Sequence) IsLeafOpen(i int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.open {
		if e.index == i {
			return true
		}
	}
	return false
}

// Close closes any currently open leaves.
func (s *Sequence) Close() error {
	s.mu.Lock()
	s.closed = true
	entries := s.open
	s.open = nil
	s.mu.Unlock()

	var firstErr error
	for _, e := range entries {
		if err := e.src.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type pinnedLeafSource struct {
	ByteSource
}

func (p *pinnedLeafSource) Close() error {
	return nil
}
