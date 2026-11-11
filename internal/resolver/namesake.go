package resolver

import (
	"context"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// namesakeIndex answers the two namesake questions a playback decision asks
// (playback.NamesakeAware): whether another catalogue title shares this
// one's normalized title and near year, and whether a release's reported
// external identifiers name a different title. The index is built once, on
// first use, from the indexed catalogue title column and refreshed lazily;
// a lookup is a map read, never a per-request catalogue scan, and answers
// are cached per title.

// namesakeEntry is one catalogue title in the index.
type namesakeEntry struct {
	key  int64
	kind core.KeyKind
	year int
}

// namesakeState guards the index and the per-title answer cache.
type namesakeState struct {
	mu      sync.Mutex
	built   bool
	entries map[string][]namesakeEntry // normalized title -> titles
	loaded  time.Time
	answers map[core.RatingKey]bool
}

// namesakeStaleAfter bounds how often the index reloads from the database.
const namesakeStaleAfter = 10 * time.Minute

// NamesakeTitles reports a same-media-type catalogue title whose normalized
// title equals t's and whose year lies within one year of t's (never t
// itself). The catalogue is read through the resolver's database, which holds
// go of the same catalogue rows; a lookup is a map read over the normalized
// title index, so it does not scale with library size per request.
func (s *Service) NamesakeTitles(ctx context.Context, t *core.Title) bool {
	if t == nil || s.db == nil {
		return false
	}
	norm := releaseparse.NormalizeTitle(lookupName(t))
	if norm == "" {
		return false
	}
	s.namesake.mu.Lock()
	if ans, ok := s.namesake.answers[t.Key]; ok && !s.namesakeStale() {
		s.namesake.mu.Unlock()
		return ans
	}
	s.namesake.mu.Unlock()
	s.buildNamesakeIndex(ctx)
	s.namesake.mu.Lock()
	defer s.namesake.mu.Unlock()
	if s.namesake.answers == nil {
		s.namesake.answers = map[core.RatingKey]bool{}
	}
	ans := false
	wantKind := t.Key.Kind()
	for _, e := range s.namesake.entries[norm] {
		if e.key == int64(t.Key) || e.kind != wantKind {
			continue
		}
		if t.Year > 0 && e.year > 0 && absInt(e.year-t.Year) > 1 {
			continue
		}
		ans = true
		break
	}
	s.namesake.answers[t.Key] = ans
	return ans
}

// NamesakeConflict reports a release whose reported identifiers name a
// different catalogue title than t. Missing identifiers carry no evidence.
func (s *Service) NamesakeConflict(ctx context.Context, t *core.Title, r core.Release) bool {
	if t == nil || s.db == nil {
		return false
	}
	if t.IDs == (core.IDs{}) || r.TitleIDs == (core.IDs{}) {
		return false
	}
	if _, conflict := releaseparse.CompareTitleIDs(r.TitleIDs, t.IDs); conflict {
		return true
	}
	return false
}

func (s *Service) namesakeStale() bool {
	return s.namesake.built && s.now().Sub(s.namesake.loaded) < namesakeStaleAfter
}

// buildNamesakeIndex loads the movie/show titles once. A single pass reads
// key, type, title and year from the catalogue table; the result is held for
// namesakeStaleAfter and rebuilt lazily.
func (s *Service) buildNamesakeIndex(ctx context.Context) {
	s.namesake.mu.Lock()
	defer s.namesake.mu.Unlock()
	if s.namesake.built && s.now().Sub(s.namesake.loaded) < namesakeStaleAfter {
		return
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key, type, title, year FROM catalog_titles WHERE type IN ('movie','show') AND title<>''`)
	if err != nil {
		s.log.Debug("namesake index load failed", "err", err)
		return
	}
	defer rows.Close()
	entries := map[string][]namesakeEntry{}
	for rows.Next() {
		var key int64
		var typ, title string
		var year int
		if err := rows.Scan(&key, &typ, &title, &year); err != nil {
			s.log.Debug("namesake index scan failed", "err", err)
			return
		}
		norm := releaseparse.NormalizeTitle(title)
		if norm == "" {
			continue
		}
		kind := core.KindMovie
		if typ == string(core.TypeShow) {
			kind = core.KindShow
		}
		entries[norm] = append(entries[norm], namesakeEntry{key: key, kind: kind, year: year})
	}
	if err := rows.Err(); err != nil {
		s.log.Debug("namesake index rows failed", "err", err)
		return
	}
	s.namesake.entries = entries
	s.namesake.answers = map[core.RatingKey]bool{}
	s.namesake.built = true
	s.namesake.loaded = s.now()
}

// lookupName is the title text to normalize for t; the index is keyed by the
// catalogue's own title, and a request's OriginalTitle or Title may differ.
func lookupName(t *core.Title) string {
	if t.Title != "" {
		return t.Title
	}
	return t.OriginalTitle
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
