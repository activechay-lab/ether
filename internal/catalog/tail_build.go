package catalog

import (
	"bytes"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// writeLeanTitleIndex writes the sections of the titleIndex
// titleIndexBuilder would build, for 1.5M titles in bounded memory:
// titleIndexBuilder keeps every title's word strings, every join string
// and a spelling map per word until it finishes (1.6 GB of heap for the
// whole export). This builds the lexicon first, keeps word ids and offsets
// only, and hands each section to out as soon as it is complete. The
// lexicon's surface spellings, which no search reads, are not kept.
// names(i) is title i's names, the title first, in index (popularity)
// order; it is called twice per title.
func writeLeanTitleIndex(n int, names func(i int) []string, out sectionSink) error {
	// Pass 1: the vocabulary and in how many titles each key appears.
	docs := map[string]uint32{}
	var seen []string
	for i := range n {
		seen = seen[:0]
		for _, name := range names(i) {
			for _, w := range lexiconWords(name) {
				if !slices.Contains(seen, w.key) {
					seen = append(seen, w.key)
					docs[strings.Clone(w.key)]++
				}
			}
		}
	}
	lx := buildLexiconFrom(docs)
	docs = nil
	for _, s := range []struct {
		sec  int
		data []byte
	}{{secLxBlob, stringBytes(lx.blob)}, {secLxOffs, sliceBytes(lx.offs)}, {secLxRunes, lx.runes}, {secLxDocs, sliceBytes(lx.docs)},
		{secLxGramKeys, sliceBytes(lx.gramKeys)}, {secLxGramAt, sliceBytes(lx.gramAt)}, {secLxPosts, sliceBytes(lx.posts)}} {
		if err := out.put(s.sec, s.data); err != nil {
			return err
		}
	}
	// Only the key table is needed from here (lx.find; size reads docs).
	lx.gramKeys, lx.gramAt, lx.posts, lx.runes = nil, nil, nil, nil

	// Pass 2: names, word ids and join entries.
	var nameBlob, joinBlob []byte
	nameAt := make([]uint32, 0, n+1)
	nameOff := make([]uint32, 0, n+n/4)
	wordAt := make([]uint32, 0, n+1)
	var wordIDs []uint32
	type join struct{ off, len, title uint32 }
	joins := make([]join, 0, n+n/4)
	var folded []string
	for i := range n {
		nameAt = append(nameAt, uint32(len(nameOff)))
		wordAt = append(wordAt, uint32(len(wordIDs)))
		start := len(wordIDs)
		folded = folded[:0]
		for _, name := range names(i) {
			for _, w := range lexiconWords(name) {
				if id := lx.find(w.key); id >= 0 && !slices.Contains(wordIDs[start:], uint32(id)) {
					wordIDs = append(wordIDs, uint32(id))
				}
			}
			words := searchWords(name)
			if len(words) == 0 {
				continue
			}
			f := strings.Join(words, " ")
			if slices.Contains(folded, f) {
				continue
			}
			folded = append(folded, f)
			nameOff = append(nameOff, uint32(len(nameBlob)))
			nameBlob = append(nameBlob, f...)
			at := len(joinBlob)
			for _, w := range words {
				joinBlob = append(joinBlob, w...)
			}
			joins = append(joins, join{uint32(at), uint32(len(joinBlob) - at), uint32(i)})
			if len(words) > 1 && isSearchArticle(words[0]) {
				at := len(joinBlob)
				for _, w := range words[1:] {
					joinBlob = append(joinBlob, w...)
				}
				joins = append(joins, join{uint32(at), uint32(len(joinBlob) - at), uint32(i)})
			}
		}
	}
	nWords := lx.size()
	lx = nil
	nameAt = append(nameAt, uint32(len(nameOff)))
	nameOff = append(nameOff, uint32(len(nameBlob)))
	for _, s := range []struct {
		sec  int
		data []byte
	}{{secNameAt, sliceBytes(nameAt)}, {secNameOff, sliceBytes(nameOff)}, {secNameBlob, nameBlob}} {
		if err := out.put(s.sec, s.data); err != nil {
			return err
		}
	}
	nameAt, nameOff, nameBlob = nil, nil, nil
	wordAt = append(wordAt, uint32(len(wordIDs)))

	// Postings: each word's titles, in title order.
	counts := make([]uint32, nWords+1)
	for _, w := range wordIDs {
		counts[w+1]++
	}
	for i := 1; i < len(counts); i++ {
		counts[i] += counts[i-1]
	}
	posts := make([]uint32, counts[len(counts)-1])
	fill := slices.Clone(counts)
	for t := range n {
		for _, w := range wordIDs[wordAt[t]:wordAt[t+1]] {
			posts[fill[w]] = uint32(t)
			fill[w]++
		}
	}
	wordIDs, wordAt, fill = nil, nil, nil
	if err := out.put(secPostAt, sliceBytes(counts)); err != nil {
		return err
	}
	if err := out.put(secPosts, sliceBytes(posts)); err != nil {
		return err
	}
	counts, posts = nil, nil

	// Joins, sorted by string then title.
	sort.Slice(joins, func(a, b int) bool {
		sa, sb := joinBlob[joins[a].off:joins[a].off+joins[a].len], joinBlob[joins[b].off:joins[b].off+joins[b].len]
		if c := bytes.Compare(sa, sb); c != 0 {
			return c < 0
		}
		return joins[a].title < joins[b].title
	})
	sortedBlob := make([]byte, 0, len(joinBlob))
	joinOff := make([]uint32, 0, len(joins)+1)
	joinTitle := make([]uint32, 0, len(joins))
	for _, j := range joins {
		joinOff = append(joinOff, uint32(len(sortedBlob)))
		sortedBlob = append(sortedBlob, joinBlob[j.off:j.off+j.len]...)
		joinTitle = append(joinTitle, j.title)
	}
	joinOff = append(joinOff, uint32(len(sortedBlob)))
	joins, joinBlob = nil, nil
	for _, s := range []struct {
		sec  int
		data []byte
	}{{secJoinOff, sliceBytes(joinOff)}, {secJoinTitle, sliceBytes(joinTitle)}, {secJoinBlob, sortedBlob}} {
		if err := out.put(s.sec, s.data); err != nil {
			return err
		}
	}
	return nil
}

// buildLeanTitleIndex is writeLeanTitleIndex into a titleIndex in memory.
func buildLeanTitleIndex(ix *titleIndex, n int, names func(i int) []string) {
	m := &memSections{}
	if err := writeLeanTitleIndex(n, names, m); err != nil {
		panic(err)
	}
	t := &tailIndex{ix: &titleIndex{lx: &searchLexicon{}}}
	if err := m.apply(t); err != nil {
		panic(err)
	}
	*ix = *t.ix
}

// buildLexiconFrom is lexiconBuilder.build from key counts alone (no
// surface spellings).
func buildLexiconFrom(docs map[string]uint32) *searchLexicon {
	keys := make([]string, 0, len(docs))
	size := 0
	for k := range docs {
		keys = append(keys, k)
		size += len(k)
	}
	slices.Sort(keys)
	lx := &searchLexicon{offs: make([]uint32, len(keys)+1), runes: make([]uint8, len(keys)), docs: make([]uint32, len(keys))}
	var sb strings.Builder
	sb.Grow(size)
	gramCount := map[uint64]uint32{}
	for i, k := range keys {
		lx.offs[i] = uint32(sb.Len())
		sb.WriteString(k)
		lx.runes[i] = uint8(min(utf8.RuneCountInString(k), 255))
		lx.docs[i] = docs[k]
		forEachGram(k, func(g uint64) { gramCount[g]++ })
	}
	lx.offs[len(keys)] = uint32(sb.Len())
	lx.blob = sb.String()
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
