package stream

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
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

// maxDiscSiblings bounds the search for the other discs of a release: how many
// further candidates are planned, how many member ISOs of an archive are
// planned, and how many sibling images are analysed.
const maxDiscSiblings = 8

// isoSiblingsBudget is the time all siblings together may take to analyse. Each
// sibling still has isoAnalyzeTimeout, but a degraded provider cannot stall the
// open for maxDiscSiblings times that.
const isoSiblingsBudget = 90 * time.Second

// minDiscFeaturePercent is how large, as a percentage of the first disc's
// feature, a further disc's feature must be to join it. A bonus disc's longest
// playlist (a "play all" of extras) is the main-feature candidate of its disc too,
// and must not be appended to the film. This threshold is a conservative guess,
// not derived from real releases: see sameFeature.
const minDiscFeaturePercent = 50

// isoAnalyzeTimeout bounds reading one disc image's structure, so a degraded
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
func (b *builder) isoSiblings(ctx context.Context, cands []candidate, self int) func() []*plan {
	members := b.memberISOs
	return func() []*plan {
		out := append([]*plan(nil), members[:min(len(members), maxDiscSiblings)]...)
		attempts := 0
		for j, c := range cands {
			// A plain video file is not a disc image: skip it without planning.
			if j == self || c.kind == nzb.KindVideo {
				continue
			}
			if len(out) >= maxDiscSiblings || attempts >= maxDiscSiblings {
				break
			}
			attempts++
			b.memberISOs = nil
			p, err := b.planFor(ctx, c)
			if err != nil || !isISOName(p.name) {
				continue
			}
			out = append(out, p)
			room := max(0, maxDiscSiblings-len(out))
			out = append(out, b.memberISOs[:min(len(b.memberISOs), room)]...)
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
		sctx, cancel := context.WithTimeout(ctx, isoSiblingsBudget)
		defer cancel()
		for _, sp := range siblings() {
			if sctx.Err() != nil {
				break // the shared budget is spent
			}
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
// of ref's, by authored duration or, when a disc has none, by clip bytes. A film
// that truly spans discs has parts of similar size; a bonus disc's playlist is
// usually shorter. The threshold is not backed by real releases, only by the
// reviewer's guidance; tune it with data.
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
		if isDVDPath(f.Path) {
			continue // a DVD title set is played whole, below
		}
		info := nzb.Classify(f.Path)
		if info.Kind != nzb.KindVideo || info.Sample || info.Extra {
			continue
		}
		if best == nil || f.Size > best.Size {
			best = f
		}
	}
	// A DVD's movie is its largest title set: the VTS_nn_1..k.VOB files, each
	// at most 1 GiB, are one continuous program stream. Playing one of them
	// alone would stop after 20-30 minutes.
	vobs, vobBytes := dvdTitleSet(an.Files)
	if len(vobs) > 0 && (best == nil || vobBytes > best.Size) {
		return vobs, strings.TrimSuffix(isoName, path.Ext(isoName)) + ".vob"
	}
	if best == nil {
		return nil, ""
	}
	return []iso.File{*best}, best.Base()
}

// dvdVOB matches a title set's VOB: VIDEO_TS/VTS_<title set>_<part>.VOB.
// Part 0 is the title set's menu.
var dvdVOB = regexp.MustCompile(`(?i)^VIDEO_TS/VTS_(\d\d)_(\d+)\.VOB$`)

func isDVDPath(p string) bool { return strings.HasPrefix(strings.ToUpper(p), "VIDEO_TS/") }

// dvdTitleSet returns the VOBs of the largest title set, in play order and
// from part 1 on, up to the first missing part, with their total size.
func dvdTitleSet(files []iso.File) ([]iso.File, int64) {
	type part struct {
		n int
		f iso.File
	}
	sets := map[string][]part{}
	for _, f := range files {
		m := dvdVOB.FindStringSubmatch(f.Path)
		if m == nil || f.Size <= 0 {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		if n >= 1 {
			sets[m[1]] = append(sets[m[1]], part{n, f})
		}
	}
	var best []iso.File
	var bestBytes int64
	for _, parts := range sets {
		sort.Slice(parts, func(i, j int) bool { return parts[i].n < parts[j].n })
		var run []iso.File
		var total int64
		for i, pt := range parts {
			if pt.n != i+1 {
				break
			}
			run = append(run, pt.f)
			total += pt.f.Size
		}
		if total > bestBytes {
			best, bestBytes = run, total
		}
	}
	return best, bestBytes
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
		// Limitation: a clip plays whole. A PlayItem whose IN/OUT covers only
		// part of its clip still contributes every packet of the clip, and
		// InTicks only sets the delta, so packets outside IN/OUT get
		// overlapping timestamps. Rare for main features.
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
