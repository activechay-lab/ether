package catalog

import (
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// The search lexicon is the vocabulary of every movie and show title
// (names, original names and alternative titles), held compactly in memory
// so a search can recover a misspelt word without touching the database:
// "the guilded age" is answered as "the gilded age". Plex does this with
// SQLite's spellfix extension over its title words; Ether's lexicon also
// recovers transpositions, accents ("amelie" finds Amélie), punctuation
// ("spiderman" and "spider man" find Spider-Man) and alternative titles.
//
// A word is keyed by its fold: lower case, accents removed, and only letters
// and digits kept. Each key remembers the most common way titles spell it
// (its surface, which a rewritten query uses so the literal title match
// still applies) and in how many titles it appears. Candidate corrections
// come from a trigram index over the keys, so a lookup reads a few posting
// lists, never the whole vocabulary.
type searchLexicon struct {
	blob    string   // every key, sorted, concatenated
	offs    []uint32 // key i is blob[offs[i]:offs[i+1]]
	runes   []uint8  // key i's length in runes (capped at 255)
	docs    []uint32 // titles containing key i
	surface map[uint32][]string
	// Trigram g's keys are posts[gramAt[i]:gramAt[i+1]] where
	// gramKeys[i] == g; gramKeys is sorted. Flat arrays rather than a map,
	// so a lexicon can be written to a file and memory-mapped (tail index).
	gramKeys []uint64
	gramAt   []uint32
	posts    []uint32
}

// gram returns trigram g's posting list.
func (lx *searchLexicon) gram(g uint64) ([]uint32, bool) {
	i, ok := slices.BinarySearch(lx.gramKeys, g)
	if !ok {
		return nil, false
	}
	return lx.posts[lx.gramAt[i]:lx.gramAt[i+1]], true
}

// lexiconMaxSurfaces bounds the spellings kept per key.
const lexiconMaxSurfaces = 3

// lexiconBuilder accumulates words before the lexicon is compacted.
type lexiconBuilder struct {
	words map[string]*lexiconWord
	seen  map[string]bool // keys already counted for the current title
}

type lexiconWord struct {
	docs     uint32
	surfaces map[string]uint32
}

func newLexiconBuilder() *lexiconBuilder {
	return &lexiconBuilder{words: map[string]*lexiconWord{}, seen: map[string]bool{}}
}

// addTitle adds one title's names (the title, its original name and its
// alternative names); a word counts once per title however often it recurs.
func (b *lexiconBuilder) addTitle(names ...string) {
	clear(b.seen)
	for _, name := range names {
		for _, w := range lexiconWords(name) {
			e := b.words[w.key]
			if e == nil {
				e = &lexiconWord{}
				b.words[w.key] = e
			}
			if !b.seen[w.key] {
				b.seen[w.key] = true
				e.docs++
			}
			if e.surfaces == nil {
				e.surfaces = map[string]uint32{}
			}
			e.surfaces[w.surface]++
		}
	}
}

func (b *lexiconBuilder) build() *searchLexicon {
	keys := make([]string, 0, len(b.words))
	size := 0
	for k := range b.words {
		keys = append(keys, k)
		size += len(k)
	}
	sort.Strings(keys)
	lx := &searchLexicon{offs: make([]uint32, len(keys)+1), runes: make([]uint8, len(keys)), docs: make([]uint32, len(keys)), surface: map[uint32][]string{}}
	var sb strings.Builder
	sb.Grow(size)
	gramCount := map[uint64]uint32{}
	for i, k := range keys {
		lx.offs[i] = uint32(sb.Len())
		sb.WriteString(k)
		lx.runes[i] = uint8(min(utf8.RuneCountInString(k), 255))
		e := b.words[k]
		lx.docs[i] = e.docs
		if s := topSurfaces(k, e.surfaces); len(s) > 0 {
			lx.surface[uint32(i)] = s
		}
		forEachGram(k, func(g uint64) { gramCount[g]++ })
	}
	lx.offs[len(keys)] = uint32(sb.Len())
	lx.blob = sb.String()
	// Lay the posting lists out back to back, each in key order.
	lx.gramKeys = make([]uint64, 0, len(gramCount))
	for g := range gramCount {
		lx.gramKeys = append(lx.gramKeys, g)
	}
	slices.Sort(lx.gramKeys)
	lx.gramAt = make([]uint32, len(lx.gramKeys)+1)
	fill := make([]uint32, len(lx.gramKeys))
	for i, g := range lx.gramKeys {
		lx.gramAt[i+1] = lx.gramAt[i] + gramCount[g]
		fill[i] = lx.gramAt[i]
	}
	lx.posts = make([]uint32, lx.gramAt[len(lx.gramKeys)])
	for i, k := range keys {
		forEachGram(k, func(g uint64) {
			j, _ := slices.BinarySearch(lx.gramKeys, g)
			lx.posts[fill[j]] = uint32(i)
			fill[j]++
		})
	}
	return lx
}

// topSurfaces returns a key's most common spellings, most common first, or
// nil when titles only ever spell it as the key itself (the common case,
// which then costs nothing to store).
func topSurfaces(key string, counts map[string]uint32) []string {
	if len(counts) == 1 {
		if _, ok := counts[key]; ok {
			return nil
		}
	}
	out := make([]string, 0, len(counts))
	for s := range counts {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if counts[out[i]] != counts[out[j]] {
			return counts[out[i]] > counts[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > lexiconMaxSurfaces {
		out = out[:lexiconMaxSurfaces]
	}
	return out
}

func (lx *searchLexicon) size() int { return len(lx.docs) }

func (lx *searchLexicon) key(i int) string { return lx.blob[lx.offs[i]:lx.offs[i+1]] }

// find returns key's index, or -1.
func (lx *searchLexicon) find(key string) int {
	i := sort.Search(lx.size(), func(i int) bool { return lx.key(i) >= key })
	if i < lx.size() && lx.key(i) == key {
		return i
	}
	return -1
}

// approxMatch is a key within the allowed edit distance of a query word.
type approxMatch struct {
	idx  int
	dist int
}

// near returns up to limit keys within maxDist edits (insertions, deletions,
// substitutions and adjacent transpositions) of key, closest and then most
// common first. Only keys sharing enough trigrams with key are compared, so
// the work is bounded by the posting lists of key's own trigrams.
func (lx *searchLexicon) near(key string, maxDist, limit int) []approxMatch {
	if maxDist <= 0 || limit <= 0 || lx.size() == 0 {
		return nil
	}
	var lists [][]uint32
	grams := 0
	forEachGram(key, func(g uint64) {
		grams++
		if l, ok := lx.gram(g); ok {
			lists = append(lists, l)
		}
	})
	// One edit changes at most three of a word's trigrams (four for a
	// transposition), so a key within maxDist shares at least this many.
	need := max(grams-4*maxDist, 1)
	if len(lists) < need {
		return nil
	}
	qr := []rune(key)
	qlen := len(qr)
	var out []approxMatch
	var scratch []int
	// Merge the sorted posting lists, counting how many hold each key.
	heads := make([]int, len(lists))
	for {
		cur := -1
		for li, l := range lists {
			if heads[li] < len(l) && (cur < 0 || int(l[heads[li]]) < cur) {
				cur = int(l[heads[li]])
			}
		}
		if cur < 0 {
			break
		}
		shared := 0
		for li, l := range lists {
			if heads[li] < len(l) && int(l[heads[li]]) == cur {
				shared++
				heads[li]++
			}
		}
		if shared < need {
			continue
		}
		if n := int(lx.runes[cur]); n < qlen-maxDist || n > qlen+maxDist {
			continue
		}
		cand := lx.key(cur)
		if cand == key {
			continue
		}
		if d := editDistance(qr, []rune(cand), maxDist, &scratch); d <= maxDist {
			out = append(out, approxMatch{idx: cur, dist: d})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].dist != out[j].dist {
			return out[i].dist < out[j].dist
		}
		if lx.docs[out[i].idx] != lx.docs[out[j].idx] {
			return lx.docs[out[i].idx] > lx.docs[out[j].idx]
		}
		return out[i].idx < out[j].idx
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// editDistance is the optimal-string-alignment distance between a and b
// (Levenshtein plus adjacent transpositions), or maxDist+1 once it must
// exceed maxDist. scratch is reused between calls.
func editDistance(a, b []rune, maxDist int, scratch *[]int) int {
	if d := len(a) - len(b); d > maxDist || -d > maxDist {
		return maxDist + 1
	}
	n := len(b) + 1
	if cap(*scratch) < 3*n {
		*scratch = make([]int, 3*n)
	}
	buf := (*scratch)[:3*n]
	// Three rows: the transposition looks two rows back.
	prev2, prev, cur := buf[:n], buf[n:2*n], buf[2*n:]
	for j := range n {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		best := i
		for j := 1; j < n; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				v = min(v, prev2[j-2]+1)
			}
			cur[j] = v
			best = min(best, v)
		}
		if best > maxDist {
			return maxDist + 1
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return min(prev[n-1], maxDist+1)
}

// forEachGram calls fn with each trigram of key padded with a boundary mark
// at each end, so a word's first and last letters carry weight too.
func forEachGram(key string, fn func(uint64)) {
	const mark = 0x1FFFFF
	r := make([]rune, 0, len(key)+2)
	r = append(r, mark)
	r = append(r, []rune(key)...)
	r = append(r, mark)
	for i := 0; i+3 <= len(r); i++ {
		fn(uint64(r[i])<<42 | uint64(r[i+1])<<21 | uint64(r[i+2]))
	}
}

// foldedWord is one word of a title or query: its fold and how it is
// written (lower case, accents and inner punctuation kept).
type foldedWord struct {
	key     string
	surface string
}

// lexiconWords splits a title or query into words. Words break at spaces
// and at punctuation which separates words (":", ",", "/", "(" and the
// like); hyphens, apostrophes and inner full stops join, so "Spider-Man",
// "Don't" and "S.W.A.T." are one word each ("spiderman", "dont", "swat").
// A joined word's parts are words too, so "spider" and "man" are found.
func lexiconWords(s string) []foldedWord {
	var out []foldedWord
	for _, field := range strings.FieldsFunc(s, isWordBreak) {
		surface := strings.ToLower(strings.TrimFunc(field, func(r rune) bool { return !isWordRune(r) }))
		if surface == "" {
			continue
		}
		key := foldKey(surface)
		if key == "" {
			continue
		}
		out = append(out, foldedWord{key: key, surface: surface})
		if strings.IndexFunc(surface, isJoiner) < 0 {
			continue
		}
		for _, part := range strings.FieldsFunc(surface, isJoiner) {
			part = strings.TrimFunc(part, func(r rune) bool { return !isWordRune(r) })
			if k := foldKey(part); utf8.RuneCountInString(k) > 1 && k != key {
				out = append(out, foldedWord{key: k, surface: part})
			}
		}
	}
	return out
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func isJoiner(r rune) bool {
	switch r {
	case '-', '\'', '’', '‘', '.', '‐', '‑':
		return true
	}
	return false
}

func isWordBreak(r rune) bool {
	return !isWordRune(r) && !isJoiner(r) && !unicode.Is(unicode.Mn, r)
}

// foldKey lower-cases s, removes accents and keeps only letters and digits.
func foldKey(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		if unicode.Is(unicode.Mn, r) || !isWordRune(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
