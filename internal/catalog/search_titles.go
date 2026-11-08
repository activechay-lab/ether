package catalog

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"math/bits"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Title search answers from an in-memory index of every movie and show name
// (title, original title and alternative titles), so a search never ranks
// the catalogue in SQL. The index finds candidates by word, by whole-title
// prefix and by spelling correction, and scores each one by relevance:
//
//	1.00  the exact title ("Up", "1917", "Spiderman" for Spider-Man)
//	0.96  the title give or take a leading article ("Office" -> The Office)
//	0.86  a title the query begins ("The Shawsh" -> The Shawshank Redemption)
//	0.80  the query's words at the start of the title, in order
//	0.76  every word in order, 0.72 in any order
//	0.50  the query inside a word (four letters or more)
//
// Each spelling correction costs 0.12 ("The Guilded Age" scores 0.88). A
// popularity prior worth up to 0.10 separates titles that match equally
// well, and lets a famous near-exact match pass an obscure exact one only
// when the two are close. Word order, punctuation, accents and spacing never
// matter ("spider man", "Spider-Man" and "spiderman" are one title).
//
// A search reads at most searchMaxCandidates titles and a bounded slice of
// the vocabulary, whatever the catalogue's size. The index is rebuilt in the
// background after catalogue writes; until the first build finishes, search
// falls back to the literal SQL match.

const (
	// searchIndexRefresh is the least time between rebuilds after the
	// catalogue changes; searchIndexMaxAge bounds staleness from writes this
	// node cannot see (another cluster node's sync).
	searchIndexRefresh = 10 * time.Minute
	searchIndexMaxAge  = time.Hour
	// searchIndexRetry spaces failed builds.
	searchIndexRetry = time.Minute
	// searchIndexFirstWait is how long a search waits for the first build.
	searchIndexFirstWait    = 250 * time.Millisecond
	searchIndexBuildTimeout = 5 * time.Minute

	// searchMaxCandidates bounds the titles one search scores.
	searchMaxCandidates = 800
	// searchPrefixScan bounds the names one whole-title prefix lookup reads.
	searchPrefixScan = 2048
	// searchPrefixWords bounds the vocabulary words a last-word prefix
	// expands to ("s" while typing).
	searchPrefixWords = 4096
	// searchNearPerWord bounds the spelling corrections tried per word.
	searchNearPerWord = 6
	// searchMaxNames bounds the names scored per title.
	searchMaxNames = 24
	// searchMaxQueryWords bounds the words of a query that are matched.
	searchMaxQueryWords = 12
	// searchFreshMax bounds the titles written since the last build that a
	// search scores one by one; older ones wait for the next build.
	searchFreshMax = 4096

	// searchConfident is a score that answers the search: the exact title,
	// or it give or take an article or one correction.
	searchConfident = 0.88
	// searchRemoteOnly scores a provider result whose names the catalogue
	// does not hold (TMDB matched an alias it alone knows).
	searchRemoteOnly = 0.3
)

// searchCorrectionCost is what one spelling correction costs a match.
const searchCorrectionCost = 0.12

// searchAffinityBoost lifts a title the viewer has watched, started or
// rated (core.SearchAffinity) above equally good matches they do not know,
// as a PMS library holds the viewer's own titles; searchServerBoost, less,
// a title only someone else on this server knows (PMS's library is the
// household's). Both are less than the gap between the exact title and a
// title it begins, so they never bury the title the viewer typed.
const (
	searchAffinityBoost = 0.06
	searchServerBoost   = 0.04
)

// applySearchAffinity raises the scores of the titles in items the
// searching viewer knows.
func applySearchAffinity(ctx context.Context, items []core.Title) {
	fn := core.SearchAffinity(ctx)
	if fn == nil || len(items) == 0 {
		return
	}
	keys := make([]core.RatingKey, 0, len(items))
	for _, t := range items {
		if t.Type == core.TypeMovie || t.Type == core.TypeShow {
			keys = append(keys, t.Key)
		}
	}
	known := fn(ctx, keys)
	for i := range items {
		if items[i].SearchScore <= 0 {
			continue
		}
		switch known[items[i].Key] {
		case core.AffinityViewer:
			items[i].SearchScore += searchAffinityBoost
		case core.AffinityServer:
			items[i].SearchScore += searchServerBoost
		}
	}
}

type searchIndexState struct {
	ix atomic.Pointer[titleIndex]
	// fresh holds movies and shows written since the index's build began,
	// newest last, so a title is searchable as soon as it is stored.
	freshMu  sync.Mutex
	fresh    []freshTitle
	freshSeq uint64
	mu       sync.Mutex
	gen      uint64    // catalogue write generation the index reflects
	at       time.Time // when the last build finished
	next     time.Time // earliest next build after a failure
	building chan struct{}
}

// WarmSearch starts building the title search index in the background so
// the first search does not wait for it.
func (c *Catalog) WarmSearch(ctx context.Context) {
	_ = c.searchIndex(ctx, false)
}

// searchIndex returns the current index, starting a background rebuild when
// it is missing or stale. Only the very first build is waited for, and only
// briefly; a stale index is served while its successor builds.
func (c *Catalog) searchIndex(ctx context.Context, wait bool) *titleIndex {
	st := &c.searchIx
	ix := st.ix.Load()
	now := c.now()
	gen := c.writeGen.Load()
	st.mu.Lock()
	stale := ix == nil || (gen != st.gen && now.Sub(st.at) >= searchIndexRefresh) || now.Sub(st.at) >= searchIndexMaxAge
	if stale && st.building == nil && !now.Before(st.next) {
		done := make(chan struct{})
		st.building = done
		go c.rebuildSearchIndex(gen, done)
	}
	building := st.building
	st.mu.Unlock()
	if ix != nil || building == nil || !wait {
		return ix
	}
	timer := time.NewTimer(searchIndexFirstWait)
	defer timer.Stop()
	select {
	case <-building:
	case <-timer.C:
	case <-ctx.Done():
	}
	return st.ix.Load()
}

func (c *Catalog) rebuildSearchIndex(gen uint64, done chan struct{}) {
	st := &c.searchIx
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), searchIndexBuildTimeout)
	defer cancel()
	started := time.Now()
	st.freshMu.Lock()
	from := st.freshSeq
	st.freshMu.Unlock()
	ix, err := c.loadTitleIndex(ctx)
	st.mu.Lock()
	defer st.mu.Unlock()
	st.building = nil
	if err != nil {
		st.next = c.now().Add(searchIndexRetry)
		c.log.Warn("catalog: build title search index", "err", err)
		return
	}
	st.ix.Store(ix)
	// Titles written before the build began are in it now.
	st.freshMu.Lock()
	st.fresh = slices.DeleteFunc(st.fresh, func(f freshTitle) bool { return f.seq <= from })
	st.freshMu.Unlock()
	st.gen, st.at = gen, c.now()
	c.log.Debug("catalog: title search index built", "titles", len(ix.keys), "words", ix.lx.size(), "took", time.Since(started))
}

// freshTitle is a movie or show written since the index's build began.
type freshTitle struct {
	seq   uint64
	key   int64
	kind  uint8
	pop   float64
	votes int
	year  int
	names [][]string // each name's word folds
}

// noteSearchWrites makes stored movies and shows searchable before the
// index's next build.
func (c *Catalog) noteSearchWrites(titles []core.Title) {
	st := &c.searchIx
	st.freshMu.Lock()
	defer st.freshMu.Unlock()
	for i := range titles {
		t := &titles[i]
		if (t.Type != core.TypeMovie && t.Type != core.TypeShow) || t.Adult {
			continue
		}
		st.freshSeq++
		f := freshTitle{seq: st.freshSeq, key: int64(t.Key), pop: t.Popularity, votes: t.VoteCount, year: t.Year}
		if t.Type == core.TypeShow {
			f.kind = titleKindShow
		}
		for _, name := range append([]string{t.Title, t.OriginalTitle}, t.AlternativeTitles...) {
			if w := searchWords(name); len(w) > 0 && len(f.names) < searchMaxNames {
				f.names = append(f.names, w)
			}
		}
		st.fresh = append(st.fresh, f)
	}
	if over := len(st.fresh) - searchFreshMax; over > 0 {
		st.fresh = slices.Delete(st.fresh, 0, over)
	}
}

// searchKeys is the index's (and the fresh titles') best n matches for q.
func (c *Catalog) searchKeys(ix *titleIndex, q searchQuery, wantMovie, wantShow bool, n int) ([]core.RatingKey, map[core.RatingKey]float64) {
	scores := map[core.RatingKey]float64{}
	var keys []core.RatingKey
	for _, h := range ix.search(q, wantMovie, wantShow, n) {
		k := core.RatingKey(ix.keys[h.title])
		scores[k] = h.score
		keys = append(keys, k)
	}
	st := &c.searchIx
	st.freshMu.Lock()
	fresh := st.fresh
	st.freshMu.Unlock()
	var scratch []int
	for i := range fresh {
		f := &fresh[i]
		if (f.kind == titleKindMovie && !wantMovie) || (f.kind == titleKindShow && !wantShow) {
			continue
		}
		best := 0.0
		for j, words := range f.names {
			sc := scoreName(q, words, &scratch)
			if q.yearless != nil {
				if ys := scoreName(*q.yearless, words, &scratch); ys > 0 {
					if f.year == q.year {
						ys += 0.03
					} else {
						ys -= 0.15
					}
					sc = max(sc, ys)
				}
			}
			if j > 0 {
				sc = altNameScore(sc)
			}
			best = max(best, sc)
		}
		if best <= 0 {
			continue
		}
		k := core.RatingKey(f.key)
		if _, ok := scores[k]; !ok {
			keys = append(keys, k)
		}
		scores[k] = best + popularityPrior(f.pop, f.votes)
	}
	sort.SliceStable(keys, func(i, j int) bool { return scores[keys[i]] > scores[keys[j]] })
	if len(keys) > n {
		for _, k := range keys[n:] {
			delete(scores, k)
		}
		keys = keys[:n]
	}
	return keys, scores
}

// titleIndex is the in-memory title search index. Titles are numbered most
// popular first, so every posting list, read in order, yields the most
// popular matches first.
type titleIndex struct {
	lx    *searchLexicon
	keys  []int64
	kinds []uint8 // titleKindMovie or titleKindShow
	pops  []float32
	votes []uint32
	years []uint16
	// Title i's names are names nameAt[i]..nameAt[i+1]-1; name n is
	// nameBlob[nameOff[n]:nameOff[n+1]], its words' folds joined by spaces.
	// The first name is the title itself.
	nameAt   []uint32
	nameOff  []uint32
	nameBlob string
	// postings: the titles holding lexicon word w are posts[postAt[w]:postAt[w+1]].
	postAt []uint32
	posts  []uint32
	// Every name's folds run together, and again without a leading article,
	// sorted, for exact and whole-title prefix lookups: entry e is
	// joinBlob[joinOff[e]:joinOff[e+1]] and belongs to title joinTitle[e].
	joinOff   []uint32
	joinTitle []uint32
	joinBlob  string
}

const (
	titleKindMovie uint8 = iota
	titleKindShow
)

func (ix *titleIndex) name(n uint32) string { return ix.nameBlob[ix.nameOff[n]:ix.nameOff[n+1]] }

func (ix *titleIndex) join(e int) string { return ix.joinBlob[ix.joinOff[e]:ix.joinOff[e+1]] }

// loadTitleIndex reads every movie and show name. It reads key ranges in
// short statements (mirrorSnapshotChunk), never one whole-catalogue
// statement whose read snapshot would hold the WAL back, and orders the
// titles by popularity in Go.
func (c *Catalog) loadTitleIndex(ctx context.Context) (*titleIndex, error) {
	type entry struct {
		key   int64
		kind  uint8
		pop   float64
		votes int
		year  int
		names []string
	}
	var titles []*entry
	at := map[int64]*entry{}
	for lo, done := int64(-1<<63), false; !done; {
		var hi int64
		err := c.db.QueryRowContext(ctx, `SELECT key FROM catalog_titles WHERE key>? ORDER BY key LIMIT 1 OFFSET ?`, lo, mirrorSnapshotChunk-1).Scan(&hi)
		if errors.Is(err, sql.ErrNoRows) {
			hi, done = 1<<63-1, true
		} else if err != nil {
			return nil, err
		}
		rows, err := c.db.QueryContext(ctx, `SELECT key, type, title, original_title, popularity, vote_count, year FROM catalog_titles
			WHERE type IN ('movie','show') AND adult=0 AND key>? AND key<=? ORDER BY key`, lo, hi)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			e := &entry{}
			var typ, title, original string
			if err := rows.Scan(&e.key, &typ, &title, &original, &e.pop, &e.votes, &e.year); err != nil {
				rows.Close()
				return nil, err
			}
			if typ == string(core.TypeShow) {
				e.kind = titleKindShow
			}
			e.names = []string{title}
			if original != "" && original != title {
				e.names = append(e.names, original)
			}
			at[e.key] = e
			titles = append(titles, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		// Names keep the order they were stored in (SQLite's rowid), as the
		// whole-table scan read them: the per-title name cap keeps the first.
		altOrder := ""
		if store.DialectOf(c.db) != store.Postgres {
			altOrder = " ORDER BY rowid"
		}
		alts, err := c.db.QueryContext(ctx, `SELECT key, title FROM catalog_alt_titles WHERE key>? AND key<=?`+altOrder, lo, hi)
		if err != nil {
			return nil, err
		}
		for alts.Next() {
			var key int64
			var title string
			if err := alts.Scan(&key, &title); err != nil {
				alts.Close()
				return nil, err
			}
			if e, ok := at[key]; ok && len(e.names) < searchMaxNames {
				e.names = append(e.names, title)
			}
		}
		err = alts.Err()
		alts.Close()
		if err != nil {
			return nil, err
		}
		lo = hi
	}
	sort.Slice(titles, func(i, j int) bool {
		if titles[i].pop != titles[j].pop {
			return titles[i].pop > titles[j].pop
		}
		return titles[i].key < titles[j].key
	})
	ix := &titleIndex{}
	for _, e := range titles {
		ix.keys = append(ix.keys, e.key)
		ix.kinds = append(ix.kinds, e.kind)
		ix.pops = append(ix.pops, float32(e.pop))
		ix.votes = append(ix.votes, uint32(max(0, e.votes)))
		ix.years = append(ix.years, uint16(max(0, min(e.year, math.MaxUint16))))
	}
	b := newTitleIndexBuilder()
	for _, e := range titles {
		b.add(e.names)
	}
	b.finish(ix)
	return ix, nil
}

// titleIndexBuilder accumulates names in title order.
type titleIndexBuilder struct {
	lex      *lexiconBuilder
	words    [][]string // per title: every word key (with joined words' parts)
	nameAt   []uint32
	nameOff  []uint32
	nameBlob strings.Builder
	joins    []joinEntry
}

type joinEntry struct {
	s     string
	title uint32
}

func newTitleIndexBuilder() *titleIndexBuilder {
	return &titleIndexBuilder{lex: newLexiconBuilder()}
}

// add adds the next title's names, the title itself first.
func (b *titleIndexBuilder) add(names []string) {
	title := uint32(len(b.words))
	b.nameAt = append(b.nameAt, uint32(len(b.nameOff)))
	b.lex.addTitle(names...)
	var keys []string
	seenName := map[string]bool{}
	for _, name := range names {
		for _, w := range lexiconWords(name) {
			keys = append(keys, w.key)
		}
		words := searchWords(name)
		if len(words) == 0 {
			continue
		}
		folded := strings.Join(words, " ")
		if seenName[folded] {
			continue
		}
		seenName[folded] = true
		b.nameOff = append(b.nameOff, uint32(b.nameBlob.Len()))
		b.nameBlob.WriteString(folded)
		joined := strings.Join(words, "")
		b.joins = append(b.joins, joinEntry{joined, title})
		if len(words) > 1 && isSearchArticle(words[0]) {
			b.joins = append(b.joins, joinEntry{strings.Join(words[1:], ""), title})
		}
	}
	b.words = append(b.words, keys)
}

func (b *titleIndexBuilder) finish(ix *titleIndex) {
	ix.lx = b.lex.build()
	ix.nameAt = append(b.nameAt, uint32(len(b.nameOff)))
	ix.nameOff = append(b.nameOff, uint32(b.nameBlob.Len()))
	ix.nameBlob = b.nameBlob.String()
	// Postings, counted then filled, each list in title order.
	counts := make([]uint32, ix.lx.size()+1)
	ids := make([][]int32, len(b.words))
	for t, keys := range b.words {
		seen := make(map[int32]bool, len(keys))
		for _, k := range keys {
			w := int32(ix.lx.find(k))
			if w < 0 || seen[w] {
				continue
			}
			seen[w] = true
			ids[t] = append(ids[t], w)
			counts[w+1]++
		}
	}
	for i := 1; i < len(counts); i++ {
		counts[i] += counts[i-1]
	}
	ix.postAt = counts
	ix.posts = make([]uint32, counts[len(counts)-1])
	fill := make([]uint32, len(counts))
	copy(fill, counts)
	for t, ws := range ids {
		for _, w := range ws {
			ix.posts[fill[w]] = uint32(t)
			fill[w]++
		}
	}
	sort.Slice(b.joins, func(i, j int) bool {
		if b.joins[i].s != b.joins[j].s {
			return b.joins[i].s < b.joins[j].s
		}
		return b.joins[i].title < b.joins[j].title
	})
	var jb strings.Builder
	ix.joinOff = make([]uint32, 0, len(b.joins)+1)
	ix.joinTitle = make([]uint32, 0, len(b.joins))
	for _, e := range b.joins {
		ix.joinOff = append(ix.joinOff, uint32(jb.Len()))
		jb.WriteString(e.s)
		ix.joinTitle = append(ix.joinTitle, e.title)
	}
	ix.joinOff = append(ix.joinOff, uint32(jb.Len()))
	ix.joinBlob = jb.String()
}

// searchWords is a name's or query's words as folds, joined words whole
// ("Spider-Man: No Way Home" -> spiderman no way home).
func searchWords(s string) []string {
	var out []string
	for _, field := range strings.FieldsFunc(s, isWordBreak) {
		field = strings.TrimFunc(field, func(r rune) bool { return !isWordRune(r) })
		if k := foldKey(field); k != "" {
			out = append(out, k)
		}
	}
	return out
}

func isSearchArticle(w string) bool { return w == "the" || w == "a" || w == "an" }

// isSearchFiller is a word a title may leave out or spell differently
// ("Fast and Furious" is Fast & Furious).
func isSearchFiller(w string) bool {
	return isSearchArticle(w) || w == "and" || w == "of" || w == "in"
}

// spellDistance is how many edits a word may be corrected by: none for
// short words and numbers (so "It", "Up" and "1917" stay as typed), one for
// words of four to six letters, two for longer ones.
func spellDistance(key string) int {
	n := utf8.RuneCountInString(key)
	if n < 4 || strings.IndexFunc(key, func(r rune) bool { return !unicode.IsDigit(r) }) < 0 {
		return 0
	}
	if n <= 6 {
		return 1
	}
	return 2
}

// searchQuery is a parsed search.
type searchQuery struct {
	words     []string
	runes     []rune // join as runes
	join      string // the words run together
	noArticle string // join without a leading article, or ""
	// A final four-digit year ("Dune 2021") is also tried as a filter.
	year     int
	yearless *searchQuery
}

func parseSearchQuery(query string) searchQuery {
	q := newSearchQuery(searchWords(query))
	if n := len(q.words); n > 1 {
		if y := searchYear(q.words[n-1]); y > 0 {
			yl := newSearchQuery(q.words[:n-1])
			q.year, q.yearless = y, &yl
		}
	}
	return q
}

func newSearchQuery(words []string) searchQuery {
	if len(words) > searchMaxQueryWords {
		words = words[:searchMaxQueryWords]
	}
	q := searchQuery{words: words, join: strings.Join(words, "")}
	q.runes = []rune(q.join)
	if len(words) > 1 && isSearchArticle(words[0]) {
		q.noArticle = strings.Join(words[1:], "")
	}
	return q
}

func searchYear(w string) int {
	if len(w) != 4 {
		return 0
	}
	y := 0
	for _, r := range w {
		if r < '0' || r > '9' {
			return 0
		}
		y = y*10 + int(r-'0')
	}
	if y < 1900 || y > 2099 {
		return 0
	}
	return y
}

// indexHit is a scored title.
type indexHit struct {
	title uint32
	score float64
}

// search returns up to n titles of the wanted kinds, best match first.
func (ix *titleIndex) search(q searchQuery, wantMovie, wantShow bool, n int) []indexHit {
	if len(q.words) == 0 || n <= 0 || (!wantMovie && !wantShow) {
		return nil
	}
	cands := ix.candidates(q, nil)
	if q.yearless != nil {
		cands = ix.candidates(*q.yearless, cands)
	}
	var scratch []int
	hits := make([]indexHit, 0, len(cands))
	for t := range cands {
		if k := ix.kinds[t]; (k == titleKindMovie && !wantMovie) || (k == titleKindShow && !wantShow) {
			continue
		}
		s := ix.scoreTitle(q, t, &scratch)
		if q.yearless != nil {
			ys := ix.scoreTitle(*q.yearless, t, &scratch)
			if ys > 0 {
				if int(ix.years[t]) == q.year {
					ys += 0.03
				} else {
					ys -= 0.15
				}
				s = max(s, ys)
			}
		}
		if s <= 0 {
			continue
		}
		hits = append(hits, indexHit{title: t, score: s + popularityPrior(float64(ix.pops[t]), int(ix.votes[t]))})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].title < hits[j].title
	})
	if len(hits) > n {
		hits = hits[:n]
	}
	return hits
}

// popularityPrior is up to 0.10 for how well known a title is: mostly its
// TMDB vote count (lasting standing: "Noah" is the 2014 film, not a
// forgotten series of the same name), partly its current popularity
// (what people are watching now). Both are log scales, topping out at
// about 20,000 votes and a popularity of 300.
func popularityPrior(pop float64, votes int) float64 {
	p := 0.0
	if pop > 0 {
		p += 0.04 * min(1, math.Log10(1+pop)/2.5)
	}
	if votes > 0 {
		p += 0.06 * min(1, math.Log10(1+float64(votes))/4.3)
	}
	return p
}

// candidates collects the titles a query could mean into into (allocated
// when nil): titles whose whole name the query is or begins, then titles
// holding every word (the last as a prefix), then the same with spelling
// corrections, most popular first, up to searchMaxCandidates.
func (ix *titleIndex) candidates(q searchQuery, into map[uint32]struct{}) map[uint32]struct{} {
	if into == nil {
		into = make(map[uint32]struct{}, 64)
	}
	if len(q.words) == 0 {
		return into
	}
	ix.joinPrefix(q.join, into)
	if q.noArticle != "" {
		ix.joinPrefix(q.noArticle, into)
	}
	if len(into) >= searchMaxCandidates {
		return into
	}
	nw := (len(ix.keys) + 63) / 64
	literal := make([][]uint64, 0, len(q.words))
	fuzzy := make([][]uint64, 0, len(q.words))
	var optionalLit, optionalFuzzy [][]uint64
	anyFuzzy := false
	for i, w := range q.words {
		last := i == len(q.words)-1
		lit := make([]uint64, nw)
		ix.orWord(lit, w, last)
		fz := lit
		if d := spellDistance(w); d > 0 {
			fz = make([]uint64, nw)
			copy(fz, lit)
			for _, m := range ix.lx.near(w, d, searchNearPerWord) {
				ix.orPostings(fz, m.idx)
				anyFuzzy = true
			}
		}
		if isSearchFiller(w) && len(q.words) > 1 {
			optionalLit, optionalFuzzy = append(optionalLit, lit), append(optionalFuzzy, fz)
			continue
		}
		literal, fuzzy = append(literal, lit), append(fuzzy, fz)
	}
	if len(literal) == 0 {
		literal, fuzzy = optionalLit, optionalFuzzy
	}
	collect := func(sets [][]uint64) {
		if len(sets) == 0 {
			return
		}
		for wi := range nw {
			word := sets[0][wi]
			for _, s := range sets[1:] {
				word &= s[wi]
			}
			for word != 0 {
				if len(into) >= searchMaxCandidates {
					return
				}
				b := bits.TrailingZeros64(word)
				word &^= 1 << b
				into[uint32(wi*64+b)] = struct{}{}
			}
		}
	}
	collect(literal)
	if anyFuzzy {
		collect(fuzzy)
	}
	return into
}

// joinPrefix adds the titles of names that run together to begin with
// prefix. When more names begin with it than one lookup reads, only those
// equal to it are added (popular prefix matches come from the word index).
func (ix *titleIndex) joinPrefix(prefix string, into map[uint32]struct{}) {
	if prefix == "" {
		return
	}
	n := len(ix.joinTitle)
	lo := sort.Search(n, func(e int) bool { return ix.join(e) >= prefix })
	hi := lo
	for hi < n && hi-lo < searchPrefixScan && strings.HasPrefix(ix.join(hi), prefix) {
		hi++
	}
	if hi-lo == searchPrefixScan {
		for e := lo; e < n && ix.join(e) == prefix; e++ {
			into[ix.joinTitle[e]] = struct{}{}
		}
		return
	}
	for e := lo; e < hi; e++ {
		into[ix.joinTitle[e]] = struct{}{}
	}
}

// orWord sets the titles holding word w (as a prefix too, for the query's
// last word) in set.
func (ix *titleIndex) orWord(set []uint64, w string, prefix bool) {
	if !prefix {
		if i := ix.lx.find(w); i >= 0 {
			ix.orPostings(set, i)
		}
		return
	}
	n := ix.lx.size()
	i := sort.Search(n, func(i int) bool { return ix.lx.key(i) >= w })
	for read := 0; i < n && read < searchPrefixWords && strings.HasPrefix(ix.lx.key(i), w); i, read = i+1, read+1 {
		ix.orPostings(set, i)
	}
}

func (ix *titleIndex) orPostings(set []uint64, word int) {
	for _, t := range ix.posts[ix.postAt[word]:ix.postAt[word+1]] {
		set[t/64] |= 1 << (t % 64)
	}
}

// scoreTitle is the best score of q against any of title t's names. The
// title's own name counts in full; other names slightly less, so a title
// found by its alternative name ranks after one found by its own.
func (ix *titleIndex) scoreTitle(q searchQuery, t uint32, scratch *[]int) float64 {
	best := 0.0
	for n := ix.nameAt[t]; n < ix.nameAt[t+1]; n++ {
		s := scoreName(q, strings.Fields(ix.name(n)), scratch)
		if n != ix.nameAt[t] {
			s = altNameScore(s)
		}
		best = max(best, s)
	}
	return best
}

// altNameScore discounts a match on a title's other name (an original or
// alternative title): slightly when the query is that name, more when it
// only begins or resembles it, since alternative names are many and mostly
// foreign.
func altNameScore(s float64) float64 {
	if s >= 0.96 {
		return s * 0.97
	}
	return s * 0.88
}

// scoreSearchText scores a title outside the index (a provider result or
// a season or episode) by its title and original title, with the
// popularity prior; 0 when neither matches.
func scoreSearchText(q searchQuery, t *core.Title) float64 {
	var scratch []int
	s := scoreName(q, searchWords(t.Title), &scratch)
	if t.OriginalTitle != "" && t.OriginalTitle != t.Title {
		s = max(s, altNameScore(scoreName(q, searchWords(t.OriginalTitle), &scratch)))
	}
	if q.yearless != nil {
		ys := scoreName(*q.yearless, searchWords(t.Title), &scratch)
		if ys > 0 {
			if t.Year == q.year {
				ys += 0.03
			} else {
				ys -= 0.15
			}
			s = max(s, ys)
		}
	}
	if s <= 0 {
		return 0
	}
	return s + popularityPrior(t.Popularity, t.VoteCount)
}

// scoreName scores q against one name's word folds (see the table above).
func scoreName(q searchQuery, words []string, scratch *[]int) float64 {
	if len(words) == 0 || len(q.words) == 0 {
		return 0
	}
	join := strings.Join(words, "")
	last := q.words[len(q.words)-1]
	// The same letters are the title, whatever its spacing and punctuation
	// ("spider man" is Spider-Man), unless the query ends in a fragment
	// still being typed: "toy s" is not Toys, "the r" is not R.
	whole := len(q.words) == 1 || len(last) >= 3
	if join == q.join && (whole || slices.Equal(words, q.words)) {
		return 1
	}
	best := 0.0
	noArticle := ""
	if len(words) > 1 && isSearchArticle(words[0]) {
		noArticle = strings.Join(words[1:], "")
	}
	if whole && ((q.noArticle != "" && (q.noArticle == join || q.noArticle == noArticle)) || (noArticle != "" && noArticle == q.join)) {
		return 0.96
	}
	switch {
	case strings.HasPrefix(join, q.join) && join != q.join:
		best = prefixScore(words, len(q.join)-len(last), last, 0.86, len(q.runes))
	case q.noArticle != "" && strings.HasPrefix(join, q.noArticle):
		// The viewer typed an article the title does not have.
		best = prefixScore(words, len(q.noArticle)-len(last), last, 0.78, len(q.runes))
	case noArticle != "" && strings.HasPrefix(noArticle, q.join):
		best = prefixScore(words[1:], len(q.join)-len(last), last, 0.84, len(q.runes))
	}
	// Misspelt whole titles and title beginnings.
	if d := spellDistance(q.join); d > 0 {
		jr := []rune(join)
		for _, cand := range []struct {
			r    []rune
			base float64
		}{{jr, 1}, {[]rune(noArticle), 0.96}} {
			if len(cand.r) == 0 {
				continue
			}
			if e := editDistance(q.runes, cand.r, d, scratch); e > 0 && e <= d {
				best = max(best, cand.base-searchCorrectionCost*float64(e))
			}
			// A misspelt beginning: within the title's first word for a
			// one-word query (so "event" does not begin "even though"),
			// anywhere for a longer one.
			first := len([]rune(words[0]))
			if cand.base < 1 && len(words) > 1 {
				first = len([]rune(words[1]))
			}
			if len(q.runes) >= 5 {
				for l := len(q.runes) - d; l <= len(q.runes)+d; l++ {
					if l <= 0 || l >= len(cand.r) || (len(q.words) == 1 && l > first) {
						continue
					}
					if e := editDistance(q.runes, cand.r[:l], d, scratch); e > 0 && e <= d {
						best = max(best, 0.86*cand.base-searchCorrectionCost*float64(e))
					}
				}
			}
		}
	}
	if best >= 0.86 {
		return best
	}
	best = max(best, scoreWords(q.words, words, scratch))
	if best == 0 && len(q.runes) >= 4 && strings.Contains(join, q.join) {
		best = 0.5
	}
	return best
}

// prefixScore scores a name the query begins (run together). It is a
// title prefix (score) when the query's words fall on the name's words, the
// last perhaps still being typed ("the shawsh"); a query that runs across
// words ("spidermannoway") scores lower, and a short one ("event" in
// "even tot hier") not at all.
func prefixScore(words []string, before int, last string, score float64, queryRunes int) float64 {
	n := 0
	for i, w := range words {
		if n == before {
			if strings.HasPrefix(w, last) {
				return score
			}
			break
		}
		n += len(w)
		if n > before || i == len(words)-1 {
			break
		}
	}
	if queryRunes >= 6 {
		return score - 0.12
	}
	return 0
}

// scoreWords matches each query word to a different name word: the same
// word, a word it begins (free for the last query word, which may still be
// being typed), or a correction. Filler words may go unmatched.
func scoreWords(qw, words []string, scratch *[]int) float64 {
	if len(words) > 64 {
		words = words[:64]
	}
	var used uint64
	positions := make([]int, 0, len(qw))
	penalty := 0.0
	for i, w := range qw {
		last := i == len(qw)-1
		bestJ, bestC := -1, math.Inf(1)
		d := spellDistance(w)
		wr := []rune(w)
		for j, x := range words {
			if used&(1<<j) != 0 {
				continue
			}
			c := math.Inf(1)
			switch {
			case x == w:
				c = 0
			case strings.HasPrefix(x, w):
				c = 0.03
				if last {
					c = 0
				}
			case d > 0:
				xr := []rune(x)
				if e := editDistance(wr, xr, d, scratch); e <= d {
					c = searchCorrectionCost * float64(e)
				} else if last && len(xr) > len(wr) {
					if e := editDistance(wr, xr[:len(wr)], d, scratch); e <= d {
						c = searchCorrectionCost * float64(e)
					}
				}
			}
			// Prefer the earliest unused word after the last match, so
			// order is kept when a word repeats.
			if c < bestC || (c == bestC && bestJ >= 0 && len(positions) > 0 && bestJ < positions[len(positions)-1] && j > positions[len(positions)-1]) {
				bestJ, bestC = j, c
			}
		}
		if bestJ < 0 {
			if isSearchFiller(w) && len(qw) > 1 {
				penalty += 0.02
				continue
			}
			return 0
		}
		used |= 1 << bestJ
		positions = append(positions, bestJ)
		penalty += bestC
	}
	if len(positions) == 0 {
		return 0
	}
	inOrder, fromStart := true, true
	start := 0
	if isSearchArticle(words[0]) && positions[0] == 1 {
		start = 1
	}
	for i, p := range positions {
		if i > 0 && p <= positions[i-1] {
			inOrder = false
		}
		if p != start+i {
			fromStart = false
		}
	}
	base := 0.72
	switch {
	case inOrder && fromStart:
		base = 0.80
	case inOrder:
		base = 0.76
	}
	base -= min(0.1, 0.015*float64(len(words)-len(positions)))
	return base - penalty
}
