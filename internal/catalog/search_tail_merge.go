package catalog

import (
	"sort"

	"github.com/braedonsaunders/ether/internal/core"
)

// Search covers two tiers: the library (every stored movie and show, with
// TMDB vote counts) and the long tail (every other id in TMDB's daily
// export, known by its original name and the day's popularity alone).
// Titles from both are scored on the same relevance scale with the same
// popularity prior (search_titles.go), so a library title and a tail title
// with the same name and standing would tie.
//
// They are not alike: the tail is about 1.5M titles, nearly all without an
// audience, and each common word ("home", "love", "her") names hundreds of
// them. So a title from outside the library (a tail hit, or a TMDB
// live-search hit the library does not hold) has searchOutsideMargin taken
// off its score: it ranks above a library title only when it matches
// clearly better. With the tail's prior at most 0.006 (popularity 5, the
// Some bar; 0.0095 at 15, the Most bar), a tail title that is exactly the
// query scores at most 0.79, so it ranks
//
//   - below every library title the query begins (0.84 to 0.86 plus a
//     prior of at least 0.006): "harry potter" is the Philosopher's Stone,
//     never a tail documentary called Harry Potter; "jurassic" is Jurassic
//     Park, not one of four tail films called Jurassic;
//   - below a library title found through one correction (0.88), the title
//     give or take an article (0.96) or the exact title (1.00);
//   - below a known library title that holds the query mid-title (0.73 to
//     0.76 plus its prior: 0.80 at about 5,000 votes): "nemo" is Finding
//     Nemo, "endgame" Avengers: Endgame, not the tail's Nemo or Endgame;
//   - above an obscure library title that only holds the query's words,
//     so a viewer who types a tail title exactly still finds it first.
//
// The gate (search_gate_test.go) measured 0.15 (the first bound above
// alone) letting tail namesakes push Finding Nemo, Avengers: Endgame and
// Marvel's The Punisher out of the top five; 0.20 and up met the baseline
// on every class; 0.22 keeps the Most bar's tail inside the third bound.
//
// A TMDB live-search hit outside the library keeps its vote count in the
// prior, so a well-known title the library lacks (released since the last
// sync) still ranks by its standing.
const searchOutsideMargin = 0.22

// searchTailConfident is the relevance (before the margin and the prior) at
// which a tail hit answers a search without waiting for TMDB: the exact
// title, or the exact title give or take an article.
const searchTailConfident = 0.96

// outsideScore is the score of a title from outside the library whose
// index score (relevance plus prior) is s.
func outsideScore(s float64) float64 { return s - searchOutsideMargin }

// tailRelevance recovers a tail hit's relevance from its index score.
func tailRelevance(t *core.Title, score float64) float64 {
	return score - popularityPrior(t.Popularity, t.VoteCount)
}

// inLibrary reports whether the library search index (or the titles
// written since its build) holds key: the tail lists every export id,
// library titles too, and a library title is the library's to rank.
func (c *Catalog) inLibrary(key core.RatingKey) bool {
	if ix := c.searchIx.ix.Load(); ix != nil && ix.holds(int64(key)) {
		return true
	}
	st := &c.searchIx
	st.freshMu.Lock()
	defer st.freshMu.Unlock()
	for i := range st.fresh {
		if st.fresh[i].key == int64(key) {
			return true
		}
	}
	return false
}

// mergeTailHits adds the tail hits to the library's, each tail hit scored
// with the outside margin, dropping tail hits the library already holds,
// best first. It keeps up to max(limit*3, 30) titles: visibility filters
// run after it.
func mergeTailHits(lib []core.Title, tail []core.Title, tailScore map[core.RatingKey]float64, limit int) []core.Title {
	out := make([]core.Title, 0, len(lib)+len(tail))
	seen := make(map[core.RatingKey]bool, len(lib)+len(tail))
	for _, t := range lib {
		seen[t.Key] = true
		out = append(out, t)
	}
	for _, t := range tail {
		if seen[t.Key] {
			continue
		}
		seen[t.Key] = true
		t.SearchScore = outsideScore(tailScore[t.Key])
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SearchScore != out[j].SearchScore {
			return out[i].SearchScore > out[j].SearchScore
		}
		return out[i].Popularity > out[j].Popularity
	})
	if keep := max(limit*3, 30); len(out) > keep {
		out = out[:keep]
	}
	return out
}

// tailAnswers reports whether a tail hit is good enough to answer the
// search without waiting for TMDB's live search.
func tailAnswers(tail []core.Title, tailScore map[core.RatingKey]float64) bool {
	for i := range tail {
		if tailRelevance(&tail[i], tailScore[tail[i].Key]) >= searchTailConfident-1e-9 {
			return true
		}
	}
	return false
}
