package catalog

import (
	"bytes"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// buildLeanTitleIndex builds the same titleIndex titleIndexBuilder does,
// for 1.5M titles in bounded memory: titleIndexBuilder keeps every title's
// word strings, every join string and a spelling map per word until it
// finishes (1.6 GB of heap for the whole export); this builds the lexicon
// first and then keeps word ids and offsets only. The lexicon's surface
// spellings, which no search reads, are not kept. names(i) is title i's
// names, the title first; titles are in index (popularity) order.
func buildLeanTitleIndex(ix *titleIndex, n int, names func(i int) []string) {
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
	ix.lx = lx

	// Pass 2: names, word ids and join entries.
	var nameBlob, joinBlob []byte
	ix.nameAt = make([]uint32, 0, n+1)
	ix.nameOff = make([]uint32, 0, n+n/4)
	wordAt := make([]uint32, 0, n+1)
	var wordIDs []uint32
	type join struct{ off, len, title uint32 }
	joins := make([]join, 0, n+n/4)
	var folded []string
	for i := range n {
		ix.nameAt = append(ix.nameAt, uint32(len(ix.nameOff)))
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
			ix.nameOff = append(ix.nameOff, uint32(len(nameBlob)))
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
	ix.nameAt = append(ix.nameAt, uint32(len(ix.nameOff)))
	ix.nameOff = append(ix.nameOff, uint32(len(nameBlob)))
	ix.nameBlob = string(nameBlob)
	nameBlob = nil
	wordAt = append(wordAt, uint32(len(wordIDs)))

	// Postings: each word's titles, in title order.
	counts := make([]uint32, lx.size()+1)
	for _, w := range wordIDs {
		counts[w+1]++
	}
	for i := 1; i < len(counts); i++ {
		counts[i] += counts[i-1]
	}
	ix.postAt = counts
	ix.posts = make([]uint32, counts[len(counts)-1])
	fill := slices.Clone(counts)
	for t := range n {
		for _, w := range wordIDs[wordAt[t]:wordAt[t+1]] {
			ix.posts[fill[w]] = uint32(t)
			fill[w]++
		}
	}
	wordIDs, wordAt, fill = nil, nil, nil

	// Joins, sorted by string then title.
	js := func(j join) string { return string(joinBlob[j.off : j.off+j.len]) }
	sort.Slice(joins, func(a, b int) bool {
		sa, sb := joinBlob[joins[a].off:joins[a].off+joins[a].len], joinBlob[joins[b].off:joins[b].off+joins[b].len]
		if c := bytes.Compare(sa, sb); c != 0 {
			return c < 0
		}
		return joins[a].title < joins[b].title
	})
	var jb strings.Builder
	jb.Grow(len(joinBlob))
	ix.joinOff = make([]uint32, 0, len(joins)+1)
	ix.joinTitle = make([]uint32, 0, len(joins))
	for _, j := range joins {
		ix.joinOff = append(ix.joinOff, uint32(jb.Len()))
		jb.WriteString(js(j))
		ix.joinTitle = append(ix.joinTitle, j.title)
	}
	ix.joinOff = append(ix.joinOff, uint32(jb.Len()))
	ix.joinBlob = jb.String()
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
