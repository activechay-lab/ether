package stream

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/iso"
	"github.com/braedonsaunders/ether/internal/nzb"
)

// isISOName reports whether a plan's file is a disc image.
func isISOName(name string) bool { return strings.EqualFold(path.Ext(name), ".iso") }

// planReader reads a plan's bytes as an io.ReaderAt, for parsing the
// structure of a disc image held in an archive without extracting it.
type planReader struct {
	ctx  context.Context
	e    *Engine
	rel  *release
	p    *plan
	prio core.Priority
	fo   fetchOpts
}

func (r planReader) ReadAt(buf []byte, off int64) (int, error) {
	if off >= r.p.size {
		return 0, errEOF
	}
	n, err := readPlan(r.ctx, r.e, r.rel, r.p, buf, off, r.prio, r.fo)
	if err == nil && n < len(buf) && off+int64(n) >= r.p.size {
		err = errEOF
	}
	return n, err
}

// expandISO turns the plan of a disc image into the plan of what plays from
// it: the Blu-ray main feature (its clips back to back), or else the largest
// video file. The image is never extracted. Its structure is read through the
// image's own plan, and the chosen files' extents are remapped straight onto
// the archive's volumes, so reads, readahead and PAR2 checks work as for any
// other plan.
func (b *builder) expandISO(ctx context.Context, p *plan) (*plan, error) {
	if p.encrypted != nil {
		return nil, fmt.Errorf("stream: encrypted disc image %s: %w", p.name, ErrUnsupported)
	}
	// The remap below uses the image's layout, so it must be exact.
	all := make([]int, len(p.vols))
	for k := range all {
		all[k] = k
	}
	if err := p.ensure(ctx, verifier{e: b.e, prio: b.prio, fo: b.fo}, all); err != nil {
		return nil, err
	}
	rd := planReader{ctx: ctx, e: b.e, rel: b.rel, p: p, prio: b.prio, fo: b.fo}
	an, err := iso.Analyze(ctx, rd, p.size, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if IsRejection(err) || isMissing(err) {
			return nil, err
		}
		// Not a readable disc image: nothing plays from it, and a smaller
		// candidate in the release still may.
		return nil, fmt.Errorf("%w: %s is not a readable disc image: %v", ErrNoPlayableFile, p.name, err)
	}
	files, name := isoFeature(an, p.name)
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: disc image %s holds no video (%d files)", ErrNoPlayableFile, p.name, len(an.Files))
	}
	return b.isoPlan(p, name, files)
}

// isoFeature picks what plays from an analysed image: its Blu-ray main
// feature, or the largest video file. name is the played file's name.
func isoFeature(an *iso.Analysis, isoName string) ([]iso.File, string) {
	if len(an.MainFeature) > 0 {
		files := make([]iso.File, len(an.MainFeature))
		for i, c := range an.MainFeature {
			files[i] = c.File
		}
		return files, strings.TrimSuffix(isoName, path.Ext(isoName)) + ".m2ts"
	}
	var best *iso.File
	for i := range an.Files {
		f := &an.Files[i]
		info := nzb.Classify(f.Path)
		if info.Kind != nzb.KindVideo || info.Sample || info.Extra {
			continue
		}
		if best == nil || f.Size > best.Size {
			best = f
		}
	}
	if best == nil {
		return nil, ""
	}
	return []iso.File{*best}, best.Base()
}

// isoPlan maps files (in order) onto the volumes that hold the image p.
func (b *builder) isoPlan(p *plan, name string, files []iso.File) (*plan, error) {
	src := p.layout()
	lay := &layout{version: 1}
	var inner int64
	add := func(vol int, off, n int64) {
		if k := len(lay.ext) - 1; k >= 0 {
			if last := &lay.ext[k]; last.vol == vol && last.off+last.size == off {
				last.size += n
				inner += n
				return
			}
		}
		lay.ext = append(lay.ext, extent{inner: inner, size: n, vol: vol, off: off})
		inner += n
	}
	for _, f := range files {
		var got int64
		for _, x := range f.Extents {
			if x.Length <= 0 {
				continue
			}
			if x.Offset < 0 || x.Offset+x.Length > p.size {
				return nil, fmt.Errorf("stream: %s extent outside the disc image: %w", f.Path, ErrCorrupt)
			}
			var covered int64
			for _, pc := range src.pieces(x.Offset, x.Length) {
				add(pc.vol, pc.off, pc.n)
				covered += pc.n
			}
			if covered != x.Length {
				return nil, fmt.Errorf("stream: %s extent is not mapped by the image: %w", f.Path, ErrCorrupt)
			}
			got += x.Length
		}
		if got != f.Size {
			return nil, fmt.Errorf("stream: %s has %d bytes in extents, %d in its entry: %w", f.Path, got, f.Size, ErrCorrupt)
		}
	}
	lay.size = inner
	verified := make([]bool, len(p.vols))
	for k := range verified {
		verified[k] = true
	}
	np := newPlan(b.rel, p.kind, name, inner, p.vols, p.info, verified)
	np.member = p.member
	np.lay = lay
	return np, nil
}
