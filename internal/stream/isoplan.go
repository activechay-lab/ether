package stream

import (
	"context"
	"fmt"
	"path"
	"sort"
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

// disc is one analysed disc image of a release.
type disc struct {
	p   *plan
	an  *iso.Analysis
	key string
	num int
}

// maxDiscSiblings bounds how many further candidates are planned in search of
// the other discs of a release.
const maxDiscSiblings = 8

// analyzeDisc reads the structure of the disc image of p. The image's layout
// must be exact for the remap that follows, so every volume is verified first.
func (b *builder) analyzeDisc(ctx context.Context, p *plan) (*disc, error) {
	if p.encrypted != nil {
		return nil, fmt.Errorf("stream: encrypted disc image %s: %w", p.name, ErrUnsupported)
	}
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
	key, num := iso.GroupKey(an.Label, p.name)
	return &disc{p: p, an: an, key: key, num: num}, nil
}

// isoSiblings returns a function that plans the release's other disc images:
// the other ISO members of the archive just planned, and the other candidates
// that turn out to be disc images (DISC_1 and DISC_2 posted as separate sets).
func (b *builder) isoSiblings(ctx context.Context, cands []candidate, self int) func() []*plan {
	members := b.memberISOs
	return func() []*plan {
		out := append([]*plan(nil), members...)
		for j, c := range cands {
			if j == self || len(out) >= maxDiscSiblings {
				continue
			}
			b.memberISOs = nil
			p, err := b.planFor(ctx, c)
			if err != nil || !isISOName(p.name) {
				continue
			}
			out = append(out, p)
			out = append(out, b.memberISOs...)
		}
		b.memberISOs = nil
		return out
	}
}

// expandISO turns the plan of a disc image into the plan of what plays from
// it: the Blu-ray main feature (its clips back to back, across every disc of
// the title), or else the largest video file. The images are never extracted.
// Their structure is read through their own plans, and the chosen files'
// extents are remapped straight onto the archive volumes, so reads, readahead
// and PAR2 checks work as for any other plan.
func (b *builder) expandISO(ctx context.Context, first *plan, siblings func() []*plan) (*plan, error) {
	d0, err := b.analyzeDisc(ctx, first)
	if err != nil {
		return nil, err
	}
	group := []*disc{d0}
	if len(d0.an.MainFeature) > 0 && siblings != nil {
		for _, sp := range siblings() {
			ds, err := b.analyzeDisc(ctx, sp)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			if ds.key == d0.key && len(ds.an.MainFeature) > 0 {
				group = append(group, ds)
			}
		}
		sort.SliceStable(group, func(i, j int) bool { return group[i].num < group[j].num })
	}
	if len(group) > 1 {
		b.e.log.Debug("multi-disc main feature", "key", d0.key, "discs", len(group))
		return b.isoPlan(group, strings.ToLower(d0.key)+".m2ts", mainFeature(group))
	}
	files, name := isoFeature(d0.an, first.name)
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: disc image %s holds no video (%d files)", ErrNoPlayableFile, first.name, len(d0.an.Files))
	}
	return b.isoPlan(group, name, discFiles(0, files, d0.an))
}

// isoFile is one file to play, with the disc it is on and its playlist timing.
type isoFile struct {
	iso.File
	disc                   int
	inTicks, durationTicks int64
	timed                  bool
}

func mainFeature(group []*disc) []isoFile {
	var out []isoFile
	for i, d := range group {
		for _, c := range d.an.MainFeature {
			out = append(out, isoFile{File: c.File, disc: i, inTicks: c.InTicks, durationTicks: c.DurationTicks, timed: true})
		}
	}
	return out
}

// discFiles wraps one disc's chosen files, with the Blu-ray timing when they
// are its main feature.
func discFiles(i int, files []iso.File, an *iso.Analysis) []isoFile {
	out := make([]isoFile, len(files))
	for k, f := range files {
		out[k] = isoFile{File: f, disc: i}
		if len(an.MainFeature) == len(files) && an.MainFeature[k].Path == f.Path {
			out[k].inTicks, out[k].durationTicks, out[k].timed = an.MainFeature[k].InTicks, an.MainFeature[k].DurationTicks, true
		}
	}
	return out
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

// isoPlan maps files (in order) onto the volumes that hold the group's
// images: the volumes of every disc are listed one after another.
func (b *builder) isoPlan(group []*disc, name string, files []isoFile) (*plan, error) {
	var vols []*postedFile
	var info []volInfo
	volBase := make([]int, len(group))
	for i, d := range group {
		volBase[i] = len(vols)
		vols = append(vols, d.p.vols...)
		info = append(info, d.p.info...)
	}
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
	var clips []clipBoundary
	var base0, cum int64 = -1, 0
	timed, mismatch := false, false
	for _, f := range files {
		d := group[f.disc]
		src := d.p.layout()
		start := inner
		for _, x := range f.Extents {
			if x.Length <= 0 {
				continue
			}
			if x.Offset < 0 || x.Offset+x.Length > d.p.size {
				return nil, fmt.Errorf("stream: %s extent outside the disc image: %w", f.Path, ErrCorrupt)
			}
			var covered int64
			for _, pc := range src.pieces(x.Offset, x.Length) {
				add(volBase[f.disc]+pc.vol, pc.off, pc.n)
				covered += pc.n
			}
			if covered != x.Length {
				return nil, fmt.Errorf("stream: %s extent is not mapped by the image: %w", f.Path, ErrCorrupt)
			}
		}
		if inner-start != f.Size {
			mismatch = true
			if inner-start == 0 {
				return nil, fmt.Errorf("stream: %s has no bytes in the image: %w", f.Path, ErrCorrupt)
			}
		}
		if f.timed {
			timed = timed || f.inTicks != 0 || f.durationTicks != 0
			in90k := f.inTicks * 2
			if base0 < 0 {
				base0 = in90k
			}
			clips = append(clips, clipBoundary{byteLen: inner - start, delta90k: base0 + cum - in90k})
			cum += f.durationTicks * 2
		}
	}
	lay.size = inner
	verified := make([]bool, len(vols))
	for k := range verified {
		verified[k] = true
	}
	np := newPlan(b.rel, group[0].p.kind, name, inner, vols, info, verified)
	np.member = group[0].p.member
	np.lay = lay
	// The continuous-timeline table needs MPLS timing for every clip and clip
	// boundaries that add up to the whole file.
	if timed && !mismatch && len(clips) == len(files) {
		np.clips = clips
		np.spans = clipSpans(clips)
	}
	return np, nil
}
