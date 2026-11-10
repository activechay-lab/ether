package stream

import (
	"context"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

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
// the other discs of a release, and how many further discs are analysed.
const maxDiscSiblings = 8

// isoAnalyzeTimeout bounds reading one disc image's structure, and separately
// finding and reading all of the release's further discs, so a degraded
// provider cannot stall starting playback for minutes.
const isoAnalyzeTimeout = 90 * time.Second

// analyzeDisc reads the structure of the disc image of p. The image's layout
// must be exact for the remap that follows, so every volume is verified first.
func (b *builder) analyzeDisc(ctx context.Context, p *plan) (*disc, error) {
	all := make([]int, len(p.vols))
	for k := range all {
		all[k] = k
	}
	if err := p.ensure(ctx, verifier{e: b.e, prio: b.prio, fo: b.fo}, all); err != nil {
		return nil, err
	}
	actx, cancel := context.WithTimeout(ctx, isoAnalyzeTimeout)
	defer cancel()
	rd := planReader{ctx: actx, e: b.e, rel: b.rel, p: p, prio: b.prio, fo: b.fo}
	an, err := iso.Analyze(actx, rd, p.size, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if actx.Err() != nil {
			return nil, fmt.Errorf("stream: reading disc image %s timed out: %w", p.name, actx.Err())
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
func (b *builder) isoSiblings(cands []candidate, self int) func(context.Context) []*plan {
	members := b.memberISOs
	return func(ctx context.Context) []*plan {
		out := append([]*plan(nil), members...)
		tried := 0
		for j, c := range cands {
			if len(out) >= maxDiscSiblings || tried >= maxDiscSiblings || ctx.Err() != nil {
				break
			}
			if j == self || c.kind == nzb.KindVideo {
				continue // a plain video is never a further disc
			}
			tried++
			b.memberISOs = nil
			p, err := b.planFor(ctx, c)
			if err != nil || !isISOName(p.name) {
				continue
			}
			out = append(out, p)
			out = append(out, b.memberISOs...)
		}
		b.memberISOs = nil
		return out[:min(len(out), maxDiscSiblings)]
	}
}

// expandISO turns the plan of a disc image into the plan of what plays from
// it: the Blu-ray main feature (its clips back to back, across every disc of
// the title), or else the largest video file. The images are never extracted.
// Their structure is read through their own plans, and the chosen files'
// extents are remapped straight onto the archive volumes, so reads, readahead
// and PAR2 checks work as for any other plan.
func (b *builder) expandISO(ctx context.Context, first *plan, siblings func(context.Context) []*plan) (*plan, error) {
	d0, err := b.analyzeDisc(ctx, first)
	if err != nil {
		return nil, err
	}
	group := []*disc{d0}
	if len(d0.an.MainFeature) > 0 && siblings != nil {
		// One budget for finding and reading every further disc: past it,
		// the discs read so far play.
		sctx, cancel := context.WithTimeout(ctx, isoAnalyzeTimeout)
		defer cancel()
		for _, sp := range siblings(sctx) {
			ds, err := b.analyzeDisc(sctx, sp)
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
		group = joinableDiscs(group)
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

// minDiscFeaturePercent is how large, as a percentage of the first disc's
// feature, a further disc's feature must be to join it. A bonus disc's longest
// playlist (a "play all" of extras) is the main-feature candidate of its disc
// too, and must not be appended to the film. A second disc of a split
// feature is usually 70-100% of the first (a 1 h 57 m disc plus 1 h 23 m is
// 71%), while a bonus disc's "play all" is often 50-75% of a 2 h film. 65%
// keeps the splits and drops most bonus discs; tune it with real releases.
const minDiscFeaturePercent = 65

// joinableDiscs keeps the discs, in order, whose feature is a real part of the
// first disc's feature, so a bonus disc is not appended to the film.
func joinableDiscs(group []*disc) []*disc {
	if len(group) < 2 {
		return group
	}
	out := group[:1:1]
	for _, d := range group[1:] {
		if sameFeature(group[0], d) {
			out = append(out, d)
		}
	}
	return out
}

// sameFeature reports whether d's main feature is at least minDiscFeaturePercent
// of ref's, by authored duration or, when a disc has none, by clip bytes.
func sameFeature(ref, d *disc) bool {
	r, x := ref.an.DurationTicks, d.an.DurationTicks
	if r <= 0 || x <= 0 {
		r, x = featureBytes(ref.an), featureBytes(d.an)
	}
	return r > 0 && x*100 >= r*minDiscFeaturePercent
}

func featureBytes(an *iso.Analysis) int64 {
	var n int64
	for _, c := range an.MainFeature {
		n += c.Size
	}
	return n
}

// isoFile is one file to play, with the disc it is on and its playlist timing.
// Limitation: a clip plays whole. A PlayItem whose IN/OUT covers only part of
// its clip still contributes every packet of it, and inTicks only sets the
// delta, so packets outside IN/OUT get overlapping timestamps (rare for main
// features).
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
	if vobs := dvdFeature(an.Files); len(vobs) > 0 {
		return vobs, strings.TrimSuffix(isoName, path.Ext(isoName)) + ".vob"
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

// reDVDTitleVOB matches the feature parts of a DVD title set,
// VIDEO_TS/VTS_nn_1.VOB to VTS_nn_9.VOB (VTS_nn_0.VOB is its menu).
var reDVDTitleVOB = regexp.MustCompile(`(?i)(?:^|/)VIDEO_TS/VTS_(\d{2})_([1-9])\.VOB$`)

// dvdFeature returns the VOBs of a DVD's largest title set in order. They are
// one program stream cut into files of at most 1 GiB, so the feature is all
// of them back to back; the largest single VOB would be a fragment of it.
func dvdFeature(files []iso.File) []iso.File {
	sets := map[string]map[int]iso.File{}
	for _, f := range files {
		m := reDVDTitleVOB.FindStringSubmatch(f.Path)
		if m == nil || f.Size <= 0 {
			continue
		}
		if sets[m[1]] == nil {
			sets[m[1]] = map[int]iso.File{}
		}
		sets[m[1]][int(m[2][0]-'0')] = f
	}
	var best []iso.File
	var bestBytes int64
	for _, vts := range slices.Sorted(maps.Keys(sets)) {
		parts := sets[vts]
		var run []iso.File
		var n int64
		for k := 1; ; k++ { // parts 1, 2, ... without a gap
			f, ok := parts[k]
			if !ok {
				break
			}
			run = append(run, f)
			n += f.Size
		}
		if n > bestBytes {
			best, bestBytes = run, n
		}
	}
	return best
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
	var sub []subRange
	encrypted := false
	for _, d := range group {
		encrypted = encrypted || d.p.encrypted != nil
	}
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
			if encrypted {
				// The bytes must be decrypted by their own image's plan.
				sub = append(sub, subRange{start: inner - covered, size: covered, src: d.p, srcOff: x.Offset})
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
	np.sub = sub
	// The continuous-timeline table needs MPLS timing for every clip and clip
	// boundaries that add up to the whole file.
	if timed && !mismatch && len(clips) == len(files) {
		np.clips = clips
		np.spans = clipSpans(clips)
	}
	return np, nil
}

// subRange is a stretch of a composed plan that is read through another plan:
// the disc image it comes from, which does its own decryption.
type subRange struct {
	start, size int64 // in the composed plan
	src         *plan
	srcOff      int64 // in src
}

// readSub reads a composed plan through the plans of the images it is made of.
func readSub(ctx context.Context, e *Engine, rel *release, p *plan, buf []byte, off int64, prio core.Priority, fo fetchOpts) (int, error) {
	i := sort.Search(len(p.sub), func(i int) bool { return p.sub[i].start+p.sub[i].size > off })
	var done int
	for ; i < len(p.sub) && done < len(buf); i++ {
		r := p.sub[i]
		at := off + int64(done)
		if at < r.start {
			return done, fmt.Errorf("stream: %s has a gap at %d: %w", p.name, at, ErrCorrupt)
		}
		take := int(min(int64(len(buf)-done), r.start+r.size-at))
		n, err := readPlan(ctx, e, rel, r.src, buf[done:done+take], r.srcOff+(at-r.start), prio, fo)
		done += n
		if err != nil {
			return done, err
		}
		if n < take {
			return done, errEOF
		}
	}
	return done, nil
}
