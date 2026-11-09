package iso

import (
	"context"
	"fmt"
	"io"
	"path"
)

// Extent is one contiguous run of ISO image bytes.
type Extent struct {
	Offset, Length int64
}

// File is one non-directory file of the image. Its bytes are the Extents in
// order (Blu-ray feature clips routinely use hundreds of them).
type File struct {
	Path    string
	Size    int64
	Extents []Extent
}

// Base is the file's name without its directories.
func (f File) Base() string { return path.Base(f.Path) }

// Clip is a main-feature file with the playlist timing the Blu-ray gives it,
// in 45 kHz ticks. InTicks is where the clip's own timeline starts.
type Clip struct {
	File
	InTicks, DurationTicks int64
}

// Analysis is what an ISO image holds.
type Analysis struct {
	// Label is the volume identifier, which groups the discs of one title.
	Label string
	// Files is every non-empty file.
	Files []File
	// MainFeature is the ordered clips of the Blu-ray main feature playlist,
	// nil for a disc without one. Playing them back to back is the movie.
	MainFeature []Clip
	// DurationTicks is the main feature's authored length at 45 kHz.
	DurationTicks int64
}

func fileOf(e isoFileEntry) File {
	f := File{Path: e.path, Size: int64(e.size), Extents: make([]Extent, 0, len(e.extents))}
	for _, x := range e.extents {
		f.Extents = append(f.Extents, Extent{Offset: int64(x.lba) * iso9660SectorSize, Length: int64(x.length)})
	}
	return f
}

// Analyze reads the filesystem of the ISO image r (size bytes), looks for a
// Blu-ray main feature and returns what it found. progress, when set, reports
// the playlists examined. It only reads structure: a few small ranges of the
// image, never its media.
func Analyze(ctx context.Context, r io.ReaderAt, size int64, progress func(done, total int)) (*Analysis, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// One read budget covers the whole analysis (walk, label, playlists), so a
	// hostile image cannot make us read without bound through Usenet.
	rs := newBudgetReader(io.NewSectionReader(r, 0, size), listReadBudget)
	entries, err := ListISOFiles(ctx, rs)
	if err != nil {
		return nil, fmt.Errorf("iso: listing files: %w", err)
	}
	out := &Analysis{Label: ReadVolumeLabel(rs)}
	for _, e := range entries {
		if e.size == 0 {
			continue
		}
		out.Files = append(out.Files, fileOf(e))
	}
	if mf := ResolveMainFeature(ctx, rs, entries, progress); mf != nil {
		out.DurationTicks = mf.DurationTicks
		for i, e := range mf.Streams {
			c := Clip{File: fileOf(e)}
			if i < len(mf.ClipInTimes) {
				c.InTicks, c.DurationTicks = mf.ClipInTimes[i], mf.ClipDurations[i]
			}
			out.MainFeature = append(out.MainFeature, c)
		}
	}
	return out, nil
}
