package catalog

import (
	"strings"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// One sort-name order serves every protocol's alphabet jump: Plex's
// firstCharacter buckets and Jellyfin and Emby's NameStartsWith,
// NameStartsWithOrGreater and NameLessThan. Each is a half-open range of
// the stored sort title (SortTitle: lower case, leading English article
// dropped, Latin accents folded), compared byte-wise (catalog_titles'
// sort_title is COLLATE "C" on PostgreSQL), so every bound is a seek on
// the sort_title indexes and never a scan.

// sortNameRange is the range [lo, hi) of sort titles a filter's name bounds
// admit; hi "" is unbounded above.
type sortNameRange struct {
	lo, hi  string
	bounded bool // some bound is set
	none    bool // the bounds admit no title
}

// sortNameKey folds a client's bound into the sort-title collation. A bound
// is not a title: a leading article and surrounding spaces stay, as
// Jellyfin compares it ("batman " is the word, not every "batman…").
func sortNameKey(s string) string {
	word := strings.TrimSpace(s)
	if word == "" {
		return ""
	}
	at := strings.Index(s, word)
	return s[:at] + foldSortTitle(word) + s[at+len(word):]
}

func sortNameRangeOf(f core.Filter) sortNameRange {
	var r sortNameRange
	atLeast := func(lo string) {
		r.bounded = true
		if lo > r.lo {
			r.lo = lo
		}
	}
	below := func(hi string) {
		r.bounded = true
		if r.hi == "" || hi < r.hi {
			r.hi = hi
		}
	}
	prefix := func(p string) {
		atLeast(p)
		if hi, ok := prefixEnd(p); ok {
			below(hi)
		}
	}
	// Plex's buckets: '#' sorts before 'a', '~' from '{' (past 'z'), and a
	// letter is its own prefix, exactly as jumpBucketSQL counts them.
	if fc := strings.TrimSpace(f.FirstCharacter); fc != "" {
		switch {
		case fc == "#" || fc == "0":
			below("a")
		case fc == "~" || fc == "…":
			atLeast("{")
		case len(fc) == 1 && ((fc[0] >= 'a' && fc[0] <= 'z') || (fc[0] >= 'A' && fc[0] <= 'Z')):
			prefix(strings.ToLower(fc))
		}
	}
	// Jellyfin skips blank bounds; NameLessThan=A is its "#" bucket.
	if p := sortNameKey(f.NameStartsWith); p != "" {
		prefix(p)
	}
	if v := sortNameKey(f.NameStartsWithOrGreater); v != "" {
		atLeast(v)
	}
	if v := sortNameKey(f.NameLessThan); v != "" {
		below(v)
	}
	r.none = r.hi != "" && r.lo >= r.hi
	return r
}

// prefixEnd is the least string above every string that starts with p: p
// with its last code point raised by one. UTF-8 keeps code point order
// byte-wise, so the result stays valid text either engine can bind.
func prefixEnd(p string) (string, bool) {
	rs := []rune(p)
	for i := len(rs) - 1; i >= 0; i-- {
		r := rs[i] + 1
		if r == 0xD800 {
			r = 0xE000 // past the surrogates, which are not text
		}
		if r <= unicode.MaxRune {
			rs[i] = r
			return string(rs[:i+1]), true
		}
	}
	return "", false
}

// sql is r's predicate on col (a sort_title column).
func (r sortNameRange) sql(col string) (string, []any) {
	switch {
	case r.none:
		return "0=1", nil
	case r.hi == "" && r.lo == "":
		return "", nil
	case r.hi == "":
		return col + " >= ?", []any{r.lo}
	case r.lo == "":
		return col + " < ?", []any{r.hi}
	}
	return col + " >= ? AND " + col + " < ?", []any{r.lo, r.hi}
}

// childSortNameCond is r's predicate on a season or episode table, whose
// sort_title keeps the database's default collation on PostgreSQL. An
// empty sort title (a row with no name) never matches a bound.
func (c *Catalog) childSortNameCond(alias string, r sortNameRange) (string, []any) {
	col := alias + ".sort_title"
	if store.DialectOf(c.db) == store.Postgres {
		col += ` COLLATE "C"`
	}
	cond, args := r.sql(col)
	if cond == "" || r.none {
		return cond, args
	}
	return alias + ".sort_title<>'' AND " + cond, args
}

// jumpBuckets are the jump-bar buckets (catalog_facet_tally's 'first'
// values, jumpBucketSQL) that together are exactly r, when r starts and
// ends on bucket edges: "" (the start of '#'), a letter, or '{' (the start
// of '~'). A total over them is then a sum of tally rows.
func jumpBuckets(r sortNameRange) ([]any, bool) {
	edge := func(s string, end bool) (int, bool) {
		switch {
		case s == "" && end:
			return 28, true
		case s == "":
			return 0, true
		case s == "{":
			return 27, true
		case len(s) == 1 && s[0] >= 'a' && s[0] <= 'z':
			return int(s[0]-'a') + 1, true
		}
		return 0, false
	}
	lo, okLo := edge(r.lo, false)
	hi, okHi := edge(r.hi, true)
	if !r.bounded || r.none || !okLo || !okHi || lo >= hi {
		return nil, false
	}
	out := make([]any, 0, hi-lo)
	for i := lo; i < hi; i++ {
		switch i {
		case 0:
			out = append(out, "#")
		case 27:
			out = append(out, "~")
		default:
			out = append(out, string(rune('A'+i-1)))
		}
	}
	return out, true
}
