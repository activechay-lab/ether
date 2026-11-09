package stream

import (
	"context"
	"fmt"
	"sort"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
)

// maxNamedFiles bounds how many unidentified files NameFiles probes: one
// article each, so a season pack of a few dozen episodes is covered.
const maxNamedFiles = 256

// NameFiles returns the NZB with real file names written into its subjects
// wherever PAR2 (or the first article's yEnc header) names an obfuscated
// file. A season pack can only be narrowed to one episode by name, so
// unlike Open (which only needs the main video) every unidentified file is
// probed. The release's PAR2 load and article cache are shared with Open.
// An NZB with nothing to name is returned unchanged. The result is kept on
// the release, so naming the same NZB again costs nothing.
func (e *Engine) NameFiles(ctx context.Context, releaseID string, nzbData []byte) ([]byte, error) {
	if err := e.ctx.Err(); err != nil {
		return nil, ErrClosed
	}
	rel, err := e.acquire(nzbData)
	if err != nil {
		return nil, err
	}
	defer e.release(rel)
	rel.namedMu.Lock()
	defer rel.namedMu.Unlock()
	if rel.named != nil {
		return rel.named, nil
	}
	b := &builder{e: e, rel: rel, prio: core.PriorityLive, fo: fetchOpts{cacheID: releaseID}}
	unknown := b.unknownFilesExcept(nil, maxNamedFiles)
	if len(unknown) == 0 {
		return nzbData, nil
	}
	sort.SliceStable(unknown, func(i, j int) bool { return unknown[i].idx < unknown[j].idx })
	set := rel.loadPar2(ctx, b.prio, b.fo, rel.par2IndexFile())
	if _, _, err := b.probeObfuscated(ctx, unknown, set); err != nil {
		return nil, fmt.Errorf("naming pack files: %w", err)
	}
	rel.infoMu.Lock()
	defer rel.infoMu.Unlock()
	// An obfuscated PAR2 file found by its head keeps a name that says
	// nothing; give it a .par2 one so a later parse still classifies it.
	for _, f := range rel.doc.Files {
		if f.Info.Kind == nzb.KindPar2 && nzb.Classify(f.Name).Kind != nzb.KindPar2 {
			f.Name = fmt.Sprintf("pack-%d.par2", f.Index)
		}
	}
	out, ok := rel.doc.Subset(func(*nzb.File) bool { return true })
	if !ok {
		return nzbData, nil
	}
	rel.named = out
	return out, nil
}
