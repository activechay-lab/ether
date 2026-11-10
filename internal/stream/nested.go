package stream

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/nzb"
)

// maxNestedSets bounds how many inner RAR sets of one archive are tried.
const maxNestedSets = 4

// tryNested plans a video held in a RAR set packed inside this archive, when
// that is what the archive holds: m, the video chosen from members, is nil, or
// is an anonymous file (an obfuscated name) that turns out to start with a RAR
// signature. It returns (nil, nil) when the archive holds no nested set, so the
// caller carries on as before; other errors are real faults of a nested set.
func (b *builder) tryNested(ctx context.Context, vols []*postedFile, members []archive.Member, m *archive.Member, concat bool) (*plan, error) {
	if m != nil && nzb.Classify(m.Name).Kind != nzb.KindUnknown {
		return nil, nil
	}
	p, err := b.planNested(ctx, vols, members, m, concat)
	if errors.Is(err, ErrNoPlayableFile) {
		return nil, nil
	}
	return p, err
}

// planNested plans a video held in a RAR set that is itself stored inside the
// outer archive (a RAR, 7z or ZIP): release.rar → movie.part01.rar … →
// movie.mkv. Nothing is extracted: each inner volume is read through the
// outer members' byte ranges to parse its headers, and the video's extents
// inside the inner volumes are then remapped straight onto the outer volumes,
// so the result is an ordinary plan over the posted files. Inner volumes are
// found by name, or, when their names are obfuscated, by their RAR signature.
// One level of nesting is followed.
//
// concat is true for 7z and ZIP, whose member extents address the
// concatenation of the volumes.
//
// It returns ErrNoPlayableFile when the archive holds no inner RAR set with a
// video. anon, when not nil, is the only member sniffed for a signature.
func (b *builder) planNested(ctx context.Context, vols []*postedFile, outer []archive.Member, anon *archive.Member, concat bool) (*plan, error) {
	if b.e.opts.DisableNestedArchives {
		return nil, ErrNoPlayableFile
	}
	if err := b.heads(ctx, vols); err != nil {
		return nil, err
	}
	if concat {
		var err error
		if outer, err = resolveConcat(outer, vols); err != nil {
			return nil, err
		}
		if anon != nil {
			for i := range outer {
				if outer[i].Name == anon.Name {
					anon = &outer[i]
				}
			}
		}
	}
	outerVols := make([]archive.Volume, len(vols))
	for i, pf := range vols {
		outerVols[i] = archive.Volume{R: b.reader(ctx, pf), Size: pf.Size()}
	}
	if anon != nil {
		// Cheap first: one read says whether the chosen anonymous file is a
		// RAR volume at all.
		if got, err := sniffRARMembers(ctx, b, outerVols, []archive.Member{*anon}); err != nil {
			return nil, err
		} else if len(got) == 0 {
			return nil, ErrNoPlayableFile
		}
	}
	sets, gap := innerRARSets(outer)
	var unknown []archive.Member
	for _, m := range outer {
		if !m.Dir && m.Size >= archive.SignatureLen && nzb.Classify(m.Name).Kind == nzb.KindUnknown {
			unknown = append(unknown, m)
		}
	}
	if len(unknown) > 0 && len(unknown) <= maxSniffedMembers {
		// Obfuscated inner volumes: every anonymous member that is a RAR
		// volume, in archive order, is one set.
		hidden, err := sniffRARMembers(ctx, b, outerVols, unknown)
		if err != nil {
			return nil, err
		}
		if len(hidden) > 0 {
			sets = append(sets, hidden)
		}
	}
	if len(sets) == 0 {
		if gap != nil {
			return nil, gap
		}
		return nil, ErrNoPlayableFile
	}
	var first error
	for _, set := range sets[:min(len(sets), maxNestedSets)] {
		p, err := b.nestedSetPlan(ctx, vols, outerVols, set)
		if err == nil {
			return p, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if first == nil || errors.Is(first, ErrNoPlayableFile) {
			first = err
		}
	}
	if gap != nil && errors.Is(first, ErrNoPlayableFile) {
		return nil, gap
	}
	return nil, first
}

// maxSniffedMembers bounds how many anonymous members are read for a RAR
// signature.
const maxSniffedMembers = 256

// sniffRARMembers returns, in archive order, the members that start with a
// RAR signature. A member that cannot be read is not one.
func sniffRARMembers(ctx context.Context, b *builder, outerVols []archive.Volume, ms []archive.Member) ([]archive.Member, error) {
	is := make([]bool, len(ms))
	err := b.parallel(ctx, len(ms), func(ctx context.Context, i int) error {
		if ms[i].Streamable() != nil && ms[i].Encryption == nil {
			return nil
		}
		r, err := archive.NewMemberReader(ms[i], outerVols)
		if err != nil {
			return nil
		}
		head := make([]byte, archive.SignatureLen)
		if n, err := r.ReadAt(head, 0); err != nil && n < len(head) {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			return nil
		}
		f := archive.Detect(head)
		is[i] = f == archive.FormatRAR4 || f == archive.FormatRAR5
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []archive.Member
	for i, ok := range is {
		if ok {
			out = append(out, ms[i])
		}
	}
	return out, nil
}

// resolveConcat rewrites the extents of 7z and ZIP members, which address the
// concatenation of the volumes, as extents of the individual volumes.
func resolveConcat(members []archive.Member, vols []*postedFile) ([]archive.Member, error) {
	raw := &layout{}
	var total int64
	for i, pf := range vols {
		s := pf.Size()
		if s < 0 || s > math.MaxInt64-total {
			return nil, fmt.Errorf("stream: concatenated archive size overflows: %w", ErrCorrupt)
		}
		raw.ext = append(raw.ext, extent{inner: total, size: s, vol: i})
		total += s
	}
	raw.size = total
	out := make([]archive.Member, len(members))
	for i, m := range members {
		out[i] = m
		out[i].Extents = nil
		for _, x := range m.Extents {
			if x.Volume != 0 || x.Offset < 0 || x.Size < 0 || x.Offset > total || x.Size > total-x.Offset {
				return nil, fmt.Errorf("stream: invalid concatenated archive extent: %w", ErrCorrupt)
			}
			var got int64
			for _, pc := range raw.pieces(x.Offset, x.Size) {
				out[i].Extents = append(out[i].Extents, archive.Extent{Volume: pc.vol, Offset: pc.off, Size: pc.n})
				got += pc.n
			}
			if got != x.Size {
				return nil, fmt.Errorf("stream: archive member %s extends past the last volume: %w", m.Name, ErrCorrupt)
			}
		}
	}
	return out, nil
}

// innerRARSets groups the outer members that are RAR volumes into ordered
// sets, largest first. Sets with a gap are dropped and reported as the
// incomplete error to return when no other set plays.
func innerRARSets(outer []archive.Member) ([][]archive.Member, error) {
	var files []*nzb.File
	byFile := map[*nzb.File]archive.Member{}
	for _, m := range outer {
		if m.Dir || m.Size <= 0 {
			continue
		}
		info := nzb.Classify(m.Name)
		if info.Kind != nzb.KindRAR || info.Sample {
			continue
		}
		f := &nzb.File{Name: m.Name, Info: info, Segments: []nzb.Segment{{Number: 1, Bytes: m.Size}}}
		files = append(files, f)
		byFile[f] = m
	}
	var out [][]archive.Member
	var gap error
	for _, s := range nzb.GroupSets(files) {
		if s.Kind != nzb.KindRAR || len(s.Volumes) == 0 {
			continue
		}
		if len(s.Missing) > 0 {
			if gap == nil {
				gap = incompleteError("nested rar set %q lacks volumes %v", s.Name, s.Missing)
			}
			continue
		}
		set := make([]archive.Member, len(s.Volumes))
		for i, v := range s.Volumes {
			set[i] = byFile[v]
		}
		out = append(out, set)
	}
	return out, gap
}

func (b *builder) nestedSetPlan(ctx context.Context, vols []*postedFile, outerVols []archive.Volume, set []archive.Member) (*plan, error) {
	inner := make([]*archive.RARVolume, len(set))
	err := b.parallel(ctx, len(set), func(ctx context.Context, k int) error {
		m := set[k]
		if m.Encrypted && b.rel.doc.Password() == "" {
			return archive.ErrPasswordRequired
		}
		if err := m.Streamable(); err != nil {
			return fmt.Errorf("stream: nested rar %s: %w", m.Name, err)
		}
		r, err := archive.NewMemberReader(m, outerVols)
		if err != nil {
			return fmt.Errorf("stream: nested rar %s: %w", m.Name, err)
		}
		v, err := archive.ReadRARVolume(r, r.Size(), b.rarOptions(ctx, 0))
		if err != nil {
			return fmt.Errorf("stream: nested rar %s: %w", m.Name, err)
		}
		if err := b.checkRARVolume(v); err != nil {
			return err
		}
		inner[k] = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	members, err := archive.ComposeRAR(inner)
	if err != nil {
		return nil, fmt.Errorf("stream: nested rar %s: %w", set[0].Name, err)
	}
	m := b.chooseMember(members)
	if m == nil {
		return nil, fmt.Errorf("%w: nested rar %s holds no video (%d files)", ErrNoPlayableFile, set[0].Name, len(members))
	}
	if m.Encrypted && b.rel.doc.Password() == "" {
		return nil, archive.ErrPasswordRequired
	}
	if err := m.Streamable(); err != nil {
		return nil, fmt.Errorf("stream: nested rar %s: %s: %w", set[0].Name, m.Name, err)
	}
	if m.Encryption != nil {
		return nil, fmt.Errorf("stream: nested rar %s: %s is encrypted inside an archive: %w", set[0].Name, m.Name, ErrUnsupported)
	}
	return b.remapNested(ctx, vols, set, m)
}

// remapNested expresses the extents of inner member m (in inner volumes) as
// extents of the outer volumes, through the outer members that are the inner
// volumes. When the outer archive is encrypted the bytes cannot be read from
// the volumes directly: each stretch is read through the plan of the outer
// member (the inner volume) that decrypts it, as for an encrypted disc image.
func (b *builder) remapNested(ctx context.Context, vols []*postedFile, set []archive.Member, m *archive.Member) (*plan, error) {
	encrypted := set[0].Encryption != nil
	for _, o := range set {
		if (o.Encryption != nil) != encrypted {
			return nil, fmt.Errorf("stream: nested rar %s: outer members differ in encryption: %w", set[0].Name, ErrUnsupported)
		}
	}
	info := make([]volInfo, len(vols))
	verified := make([]bool, len(vols))
	for k, pf := range vols {
		info[k] = volInfo{size: pf.Size()}
		verified[k] = true
	}
	decrypt := map[int]*plan{} // inner volume → plan of the outer member
	lay := &layout{version: 1}
	var sub []subRange
	var inner int64
	for _, x := range m.Extents {
		if x.Volume < 0 || x.Volume >= len(set) {
			return nil, fmt.Errorf("stream: nested rar extent in volume %d of %d: %w", x.Volume, len(set), ErrCorrupt)
		}
		var pieces []archive.Extent
		if encrypted {
			src := decrypt[x.Volume]
			if src == nil {
				var err error
				if src, err = b.encryptedPlan(ctx, kindRAR, &set[x.Volume], vols, info); err != nil {
					return nil, err
				}
				decrypt[x.Volume] = src
			}
			if x.Offset < 0 || x.Size < 0 || x.Offset > src.size || x.Size > src.size-x.Offset {
				return nil, fmt.Errorf("stream: nested rar extent outside %s: %w", set[x.Volume].Name, ErrCorrupt)
			}
			for _, pc := range src.layout().pieces(x.Offset, x.Size) {
				pieces = append(pieces, archive.Extent{Volume: pc.vol, Offset: pc.off, Size: pc.n})
			}
			sub = append(sub, subRange{start: inner, size: x.Size, src: src, srcOff: x.Offset})
		} else {
			var err error
			if pieces, err = sliceExtents(set[x.Volume].Extents, x.Offset, x.Size); err != nil {
				return nil, fmt.Errorf("stream: nested rar %s: %w", set[x.Volume].Name, err)
			}
		}
		var covered int64
		for _, pc := range pieces {
			if pc.Volume < 0 || pc.Volume >= len(vols) {
				return nil, fmt.Errorf("stream: nested rar extent in volume %d of %d: %w", pc.Volume, len(vols), ErrCorrupt)
			}
			if k := len(lay.ext) - 1; k >= 0 && lay.ext[k].vol == pc.Volume && lay.ext[k].off+lay.ext[k].size == pc.Offset {
				lay.ext[k].size += pc.Size
			} else {
				lay.ext = append(lay.ext, extent{inner: inner, size: pc.Size, vol: pc.Volume, off: pc.Offset})
			}
			inner += pc.Size
			covered += pc.Size
		}
		if covered != x.Size {
			return nil, fmt.Errorf("stream: nested rar extent of %s is not mapped: %w", set[x.Volume].Name, ErrCorrupt)
		}
	}
	if inner != m.Size {
		return nil, fmt.Errorf("stream: nested rar member %s covers %d of %d bytes: %w", m.Name, inner, m.Size, ErrCorrupt)
	}
	lay.size = inner
	p := newPlan(b.rel, kindRAR, path.Base(m.Name), m.Size, vols, info, verified)
	p.member = m.Name
	p.lay = lay
	p.sub = sub
	return p, nil
}

// sliceExtents returns the part of the concatenation of extents that covers
// [off, off+n). It fails with ErrCorrupt unless all n bytes are covered.
func sliceExtents(extents []archive.Extent, off, n int64) ([]archive.Extent, error) {
	if off < 0 || n < 0 {
		return nil, fmt.Errorf("stream: negative extent range: %w", ErrCorrupt)
	}
	var out []archive.Extent
	var pos, got int64
	for _, e := range extents {
		if got == n {
			break
		}
		end := pos + e.Size
		if end > off {
			skip := max(off-pos, 0)
			take := min(e.Size-skip, n-got)
			out = append(out, archive.Extent{Volume: e.Volume, Offset: e.Offset + skip, Size: take})
			got += take
			off += take
		}
		pos = end
	}
	if got != n {
		return nil, fmt.Errorf("stream: inner volume holds %d of %d wanted bytes: %w", got, n, ErrCorrupt)
	}
	return out, nil
}

// startsWithRAR reports whether an anonymously named member of the first
// volume starts with a RAR signature, i.e. is a volume of a nested set rather
// than the video. Named members are never read.
func (b *builder) startsWithRAR(ctx context.Context, first *postedFile, f *archive.RARFile) bool {
	if b.e.opts.DisableNestedArchives || f.Encrypted || nzb.Classify(f.Name).Kind != nzb.KindUnknown || f.PackedSize < archive.SignatureLen {
		return false
	}
	head := make([]byte, archive.SignatureLen)
	if n, err := b.reader(ctx, first).ReadAt(head, f.DataOffset); err != nil && n < len(head) {
		return false
	}
	d := archive.Detect(head)
	return d == archive.FormatRAR4 || d == archive.FormatRAR5
}
