package catalog

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// TMDB's popularity, vote count and rating move a little on nearly every
// sync, and 12 indexes on catalog_titles hold them. Writing every wobble
// rewrote those index entries for the whole catalogue on each pass (on
// PostgreSQL, a non-HOT update that adds an entry to every index). A sync
// therefore writes them only when the change matters:
//
//   - always on a new title, a detail fetch and a material upsert;
//   - when a value moves far enough to change an ordering by it
//     (popularity 10% and at least 0.5, votes 5% and at least one,
//     rating 0.1). TMDB's long tail (popularity under 1) moves more than
//     10% almost every day: on two consecutive real exports a relative
//     rule alone still wrote 74% of 1.27M movies;
//   - when a value crosses a threshold something decides by: the browse
//     bar, the new-release bar, and every numeric bound of an owner or
//     viewer catalogue rule, so no rule reads a value on the wrong side;
//   - and exactly once every ratingReconcileDays for every title the sync
//     sees (by key, a 1/ratingReconcileDays share each day, about 50k a
//     day at 1.5M titles), so no stored value is older than that.
const (
	popularityDrift     = 0.10
	popularityFloor     = 0.5
	votesDrift          = 0.05
	ratingDrift         = 0.1
	ratingReconcileDays = 30
)

// ratingCuts are the thresholds a stored rating, vote count or popularity
// must never sit on the wrong side of.
type ratingCuts struct {
	popularity, votes, rating []float64
}

// ratingCuts reads the installed config and every viewer's own rules.
func (c *Catalog) ratingCuts() ratingCuts {
	cfg := c.config()
	var cuts ratingCuts
	if cfg.MinPopularity > 0 {
		cuts.popularity = append(cuts.popularity, cfg.MinPopularity, max(2.5, cfg.MinPopularity*0.6))
	} else {
		cuts.popularity = append(cuts.popularity, 2.5)
	}
	if cfg.MinVotes > 0 {
		cuts.votes = append(cuts.votes, float64(cfg.MinVotes), float64(max(10, cfg.MinVotes/5)))
	} else {
		cuts.votes = append(cuts.votes, 10)
	}
	add := func(specs []core.LibraryExclusion) {
		for _, spec := range specs {
			for _, r := range spec.Rules {
				var dst *[]float64
				switch r.Field {
				case "popularity":
					dst = &cuts.popularity
				case "votes":
					dst = &cuts.votes
				case "rating":
					dst = &cuts.rating
				default:
					continue
				}
				var one float64
				var many []float64
				if json.Unmarshal(r.Value, &one) == nil {
					*dst = append(*dst, one)
				} else if json.Unmarshal(r.Value, &many) == nil {
					*dst = append(*dst, many...)
				}
				// "is" compares exactly: any write of that field matters.
				if r.Op == "is" {
					*dst = append(*dst, -1)
				}
			}
		}
	}
	pol := c.LibraryPolicies()
	add(pol.MovieRules)
	add(pol.ShowRules)
	c.viewerRules.RLock()
	for _, v := range c.viewerRules.entries {
		add(v.document.Exclusions)
	}
	c.viewerRules.RUnlock()
	return cuts
}

// ratingExact reports whether this sync writes key's values exactly
// regardless of drift: its day in the reconcile cycle.
func ratingExact(key core.RatingKey, now time.Time) bool {
	day := now.Unix() / 86400
	k := int64(key) % ratingReconcileDays
	if k < 0 {
		k += ratingReconcileDays
	}
	return k == day%ratingReconcileDays
}

// ratingDriftSQL is the narrow update's condition beyond "the values
// differ", and its arguments. Values are bound as DOUBLE PRECISION and
// booleans compared as 0/1 so the same SQL runs on SQLite and PostgreSQL.
func ratingDriftSQL(cuts ratingCuts, pop float64, votes int, rating float64) (string, []any) {
	const f = `CAST(? AS DOUBLE PRECISION)`
	var b strings.Builder
	fmt.Fprintf(&b, `((ABS(popularity-%[1]s)>=%[2]g*popularity AND ABS(popularity-%[1]s)>=%[5]g) OR ABS(vote_count-%[1]s)>=CASE WHEN %[3]g*vote_count>1 THEN %[3]g*vote_count ELSE 1 END OR ABS(rating-%[1]s)>=%[4]g`, f, popularityDrift, votesDrift, ratingDrift, popularityFloor)
	args := []any{pop, pop, float64(votes), rating}
	for _, group := range []struct {
		col  string
		cuts []float64
		v    float64
	}{{"popularity", cuts.popularity, pop}, {"vote_count", cuts.votes, float64(votes)}, {"rating", cuts.rating, rating}} {
		for _, cut := range group.cuts {
			if cut < 0 {
				// An exact-value rule ("is"): every change matters.
				b.WriteString(` OR 1=1`)
				continue
			}
			below := 0
			if group.v < cut {
				below = 1
			}
			b.WriteString(` OR (CASE WHEN ` + group.col + `<` + f + ` THEN 1 ELSE 0 END)<>?`)
			args = append(args, cut, below)
		}
	}
	b.WriteString(`)`)
	return b.String(), args
}

// popularityMatters is ratingDriftSQL's popularity rule in Go, for the
// export pass: it holds every stored popularity in memory, so it decides
// before issuing an UPDATE rather than in the UPDATE's WHERE clause.
// exact is ratingExact for the title's key.
func popularityMatters(cuts ratingCuts, old, cur float64, exact bool) bool {
	if old == cur {
		return false
	}
	if exact {
		return true
	}
	if d := math.Abs(old - cur); d >= popularityDrift*old && d >= popularityFloor {
		return true
	}
	for _, cut := range cuts.popularity {
		if cut < 0 || (old < cut) != (cur < cut) {
			return true
		}
	}
	return false
}
