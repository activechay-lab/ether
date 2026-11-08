package debrid

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// packUnit is one episode-sized thing in a Usenet season pack: a plain
// video file or one archive set. Names are what the episode is read from.
type packUnit struct {
	name  string
	bytes int64
	files map[*nzb.File]bool
}

// PackNamer writes real file names (read from PAR2 or the first articles)
// into an obfuscated NZB's subjects. Implemented by *stream.Engine.
type PackNamer interface {
	NameFiles(ctx context.Context, releaseID string, nzbData []byte) ([]byte, error)
}

// minUnnamedBytes is the size below which an unidentified file (an nfo, a
// sample part) is not worth probing.
const minUnnamedBytes = 1 << 20

// episodeNZB is EpisodeNZB for a release whose files may be obfuscated:
// unidentifiable big files are named first when a namer is set.
func (x *Indexer) episodeNZB(ctx context.Context, r core.Release, data []byte) ([]byte, error) {
	if x.packNamer != nil && hasUnnamedFiles(data) {
		named, err := x.packNamer.NameFiles(ctx, r.ID, data)
		if err != nil {
			return nil, err
		}
		data = named
	}
	return EpisodeNZB(data, r.Target)
}

func hasUnnamedFiles(data []byte) bool {
	doc, err := nzb.Parse(data)
	if err != nil {
		return false
	}
	for _, f := range doc.Files {
		if f.Info.Kind == nzb.KindUnknown && f.Bytes() >= minUnnamedBytes {
			return true
		}
	}
	return false
}

// EpisodeNZB narrows the NZB of a Usenet season pack to the files of the
// wanted episode (its video, or its archive set, plus its PAR2 index).
// The stream engine then plays that episode like any single-episode
// release. A pack with no file for the episode wraps core.ErrNotAvailable.
func EpisodeNZB(data []byte, t *core.ReleaseTarget) ([]byte, error) {
	if t == nil || t.Episode <= 0 {
		return data, nil
	}
	doc, err := nzb.Parse(data)
	if err != nil {
		return nil, err
	}
	chosen := pickPackUnit(packUnits(doc), t)
	if chosen == nil {
		return nil, fmt.Errorf("usenet season pack has no file for S%02dE%02d: %w", t.Season, t.Episode, core.ErrNotAvailable)
	}
	stem := strings.ToLower(chosen.name)
	// PAR2 files named after the episode, and the pack's index when the
	// episode has none of its own. The index also lists the other episodes;
	// the engine's integrity check looks up the stream's file by name and
	// never consults those entries. Recovery volumes are never kept: they
	// span the whole pack and nothing here repairs.
	own := map[*nzb.File]bool{}
	ownIndex := false
	for _, f := range doc.Files {
		if f.Info.Kind == nzb.KindPar2 && !nzb.IsPar2Volume(f.Name) && strings.HasPrefix(strings.ToLower(f.Name), stem) {
			own[f] = true
			ownIndex = true
		}
	}
	packIndex := nzb.Par2Index(doc.Files)
	out, ok := doc.Subset(func(f *nzb.File) bool {
		return chosen.files[f] || own[f] || (!ownIndex && f == packIndex)
	})
	if !ok {
		return nil, fmt.Errorf("usenet season pack has no file for S%02dE%02d: %w", t.Season, t.Episode, core.ErrNotAvailable)
	}
	return out, nil
}

func packUnits(doc *nzb.NZB) []packUnit {
	var units []packUnit
	var vols []*nzb.File
	for _, f := range doc.Files {
		switch {
		case f.Info.Kind == nzb.KindVideo && !f.Info.Sample && !f.Info.Extra:
			name := strings.TrimSuffix(f.Name, path.Ext(f.Name))
			units = append(units, packUnit{name: name, bytes: f.Bytes(), files: map[*nzb.File]bool{f: true}})
		case f.Info.Kind.IsArchive():
			vols = append(vols, f)
		}
	}
	for _, s := range nzb.GroupSets(vols) {
		if len(s.Volumes) == 0 || s.Volumes[0].Info.Sample {
			continue
		}
		u := packUnit{name: s.Name, bytes: s.Bytes(), files: map[*nzb.File]bool{}}
		for _, v := range s.Volumes {
			u.files[v] = true
		}
		units = append(units, u)
	}
	return units
}

// pickPackUnit returns the largest unit whose name carries the target
// episode, or nil. The release itself already matched the series, so the
// check is the episode number alone.
func pickPackUnit(units []packUnit, t *core.ReleaseTarget) *packUnit {
	var best *packUnit
	for i := range units {
		u := &units[i]
		if !fileCourMatchesTarget(u.name+".mkv", t) {
			continue
		}
		info := releaseparse.Parse(u.name)
		ok := releaseparse.EpisodeMatches(info, t.Season, t.Episode) ||
			(t.AbsoluteEpisode > 0 && releaseparse.EpisodeMatchesAbsolute(info, t.AbsoluteEpisode)) ||
			(len(info.Episodes) == 0 && classicPackEpisodeMatches(u.name+".mkv", t.Season, t.Episode))
		if info.Cour > 0 && t.Cour == info.Cour && t.CourEpisode > 0 {
			ok = releaseparse.EpisodeMatches(info, t.Season, t.CourEpisode) ||
				releaseparse.EpisodeMatchesAbsolute(info, t.CourEpisode)
		}
		if ok && (best == nil || u.bytes > best.bytes) {
			best = u
		}
	}
	return best
}
