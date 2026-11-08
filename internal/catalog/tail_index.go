package catalog

import (
	"context"
	"errors"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// The long-tail tier: every movie and series id in TMDB's daily export,
// findable by search and openable, without a catalogue row. Titles a
// viewer opens are promoted into the library (catalog_titles) by the
// first-sight detail fetch in Get; nothing else of the tail is ever stored
// in the database.
//
// The tier is one search index (the same titleIndex layout and scorer the
// library uses) plus, per entry, what a search result shows: the title,
// the original name, the release date when known and the poster path when
// known. It is built from the export (and the names file, tail_names.go)
// once a day, written to a versioned file and memory-mapped
// (tail_file.go); a search reads the pages it touches and nothing else.
// The index holds every export id, library titles too: a search merges
// both and the library row wins, so a title is findable whatever its tier.

// tailIndex is one built (or loaded) tail tier.
type tailIndex struct {
	ix  *titleIndex
	day time.Time // export day

	// id -> entry: sorted[i] is a key, sortAt[i] its entry index.
	sorted []int64
	sortAt []uint32

	// Per entry, in index (popularity) order.
	titleAt    []uint32 // display title: titleBlob[titleAt[i]:titleAt[i+1]]
	titleBlob  string
	origAt     []uint32 // original name when it differs, else empty
	origBlob   string
	posterAt   []uint32
	posterBlob string
	released   []int32 // dayNumber (tail_names.go); 0 = not known
	flags      []uint8
}

const (
	tailAdult      uint8 = 1 << iota
	tailVideo            // a video release (excluded from browse, findable)
	tailPosterNone       // TMDB has no poster for it (seen by the names walk)
	tailUndated          // TMDB has no date for it (seen by the names walk)
)

// tailEntry is what the tail knows about one title.
type tailEntry struct {
	// Title is a summary: Key, Type, IDs.TMDB, Title, OriginalTitle,
	// Popularity, and Year, Released and PosterPath when known.
	Title core.Title
	// PosterNone: TMDB has no poster for this title; do not look it up.
	PosterNone bool
	// DateUnknown: the export lists the title but no date has been read
	// yet. Search lets it through; once promoted, the row's own date rules.
	DateUnknown bool
	// Video: TMDB flags it a video release.
	Video bool
}

func (t *tailIndex) len() int { return len(t.flags) }

// find returns key's entry index.
func (t *tailIndex) find(key core.RatingKey) (int, bool) {
	i, ok := slices.BinarySearch(t.sorted, int64(key))
	if !ok {
		return 0, false
	}
	return int(t.sortAt[i]), true
}

func blobAt(blob string, at []uint32, i int) string { return blob[at[i]:at[i+1]] }

// entry builds entry i's summary.
func (t *tailIndex) entry(i int) tailEntry {
	key := core.RatingKey(t.ix.keys[i])
	typ := core.TypeMovie
	if t.ix.kinds[i] == titleKindShow {
		typ = core.TypeShow
	}
	// Copies: the blobs may be a memory-mapped file that is unmapped once
	// this version is retired, and a summary outlives the search.
	title := strings.Clone(blobAt(t.titleBlob, t.titleAt, i))
	orig := strings.Clone(blobAt(t.origBlob, t.origAt, i))
	if orig == "" {
		orig = title
	}
	e := tailEntry{Title: core.Title{
		Key: key, Type: typ, IDs: core.IDs{TMDB: key.TMDB()},
		Title: title, OriginalTitle: orig, SortTitle: sortTitle(title),
		Popularity: float64(t.ix.pops[i]), PosterPath: strings.Clone(blobAt(t.posterBlob, t.posterAt, i)),
		Year: int(t.ix.years[i]),
	}}
	f := t.flags[i]
	e.Title.Adult = f&tailAdult != 0
	e.Video = f&tailVideo != 0
	e.PosterNone = f&tailPosterNone != 0
	if d := t.released[i]; d > 0 {
		e.Title.Released = dayTime(d)
		if e.Title.Year == 0 {
			e.Title.Year = e.Title.Released.Year()
		}
	} else {
		e.DateUnknown = f&tailUndated == 0
	}
	return e
}

// tailRow is one export row collected for the build.
type tailRow struct {
	key   int64
	kind  uint8
	pop   float32
	flags uint8
	name  string // the export's original name
}

// tailBuilder collects export rows, then builds the index.
type tailBuilder struct {
	rows []tailRow
	day  time.Time
}

func (b *tailBuilder) add(tv bool, row tmdb.ExportRow, name string) {
	r := tailRow{key: int64(titleKey(tv, row.ID)), pop: float32(row.Popularity), name: name}
	if tv {
		r.kind = titleKindShow
	}
	if row.Adult {
		r.flags |= tailAdult
	}
	if row.Video {
		r.flags |= tailVideo
	}
	b.rows = append(b.rows, r)
}

// build builds the index in memory (tests, and the reader-based
// buildTailIndex): the same sections the file writer streams.
func (b *tailBuilder) build(names *tailNames) *tailIndex {
	m := &memSections{}
	n, err := b.write(names, m)
	if err != nil {
		panic(err) // memory sections do not fail
	}
	t, err := m.index(b.day, n)
	if err != nil {
		panic(err)
	}
	return t
}

// write orders the rows most popular first, joins the names file and
// writes every section of the index to out as soon as it is complete,
// dropping it from the heap: the build's transient memory is the rows,
// the names and one section at a time, not the whole index (H measured
// the whole-index build at ~600 MiB above the app's heap).
func (b *tailBuilder) write(names *tailNames, out sectionSink) (int, error) {
	rows := b.rows
	b.rows = nil
	// One entry per key (an id can be listed in both the plain and the
	// adult file): keep the first.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	rows = slices.CompactFunc(rows, func(a, b tailRow) bool { return a.key == b.key })
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].pop != rows[j].pop {
			return rows[i].pop > rows[j].pop
		}
		return rows[i].key < rows[j].key
	})
	n := len(rows)
	put := func(sec int, data []byte) error { return out.put(sec, data) }

	// The id -> entry table.
	order := make([]uint32, n)
	for i := range order {
		order[i] = uint32(i)
	}
	sort.Slice(order, func(a, b int) bool { return rows[order[a]].key < rows[order[b]].key })
	sorted := make([]int64, n)
	for i, at := range order {
		sorted[i] = rows[at].key
	}
	if err := put(secSorted, sliceBytes(sorted)); err != nil {
		return 0, err
	}
	sorted = nil
	if err := put(secSortAt, sliceBytes(order)); err != nil {
		return 0, err
	}
	order = nil

	keys := make([]int64, n)
	kinds := make([]uint8, n)
	pops := make([]float32, n)
	for i, r := range rows {
		keys[i], kinds[i], pops[i] = r.key, r.kind, r.pop
	}
	for _, s := range []struct {
		sec  int
		data []byte
	}{{secKeys, sliceBytes(keys)}, {secKinds, kinds}, {secPops, sliceBytes(pops)}, {secVotes, make([]byte, 4*n)}} {
		if err := put(s.sec, s.data); err != nil {
			return 0, err
		}
	}
	keys, kinds, pops = nil, nil, nil

	years := make([]uint16, n)
	released := make([]int32, n)
	flags := make([]uint8, n)
	var titles, origs, posters blobBuilder
	titleAt := make([]uint32, 0, n+1)
	origAt := make([]uint32, 0, n+1)
	posterAt := make([]uint32, 0, n+1)
	for i := range rows {
		r := &rows[i]
		flags[i] = r.flags
		title, orig, poster := r.name, "", ""
		if nm, ok := names.lookup(core.RatingKey(r.key)); ok {
			if nm.title != "" && nm.title != r.name {
				title, orig = nm.title, r.name
			}
			poster = nm.poster
			released[i] = nm.released
			if nm.year > 0 {
				years[i] = uint16(nm.year)
			}
			flags[i] |= nm.flags & (tailPosterNone | tailUndated)
		}
		titleAt = append(titleAt, titles.add(title))
		origAt = append(origAt, origs.add(orig))
		posterAt = append(posterAt, posters.add(poster))
		r.name = ""
	}
	rows = nil
	titleAt = append(titleAt, titles.end())
	origAt = append(origAt, origs.end())
	posterAt = append(posterAt, posters.end())
	titleBlob, origBlob := titles.String(), origs.String()
	titles, origs = blobBuilder{}, blobBuilder{}
	for _, s := range []struct {
		sec  int
		data []byte
	}{{secYears, sliceBytes(years)}, {secReleased, sliceBytes(released)}, {secFlags, flags},
		{secPosterAt, sliceBytes(posterAt)}, {secPosterBlob, posters.b},
		{secTitleAt, sliceBytes(titleAt)}, {secTitleBlob, stringBytes(titleBlob)}, {secOrigAt, sliceBytes(origAt)}, {secOrigBlob, stringBytes(origBlob)}} {
		if err := put(s.sec, s.data); err != nil {
			return 0, err
		}
	}
	years, released, flags, posterAt, posters = nil, nil, nil, nil, blobBuilder{}

	var both [2]string
	err := writeLeanTitleIndex(n, func(i int) []string {
		both[0] = blobAt(titleBlob, titleAt, i)
		if o := blobAt(origBlob, origAt, i); o != "" {
			both[1] = o
			return both[:2]
		}
		return both[:1]
	}, out)
	return n, err
}

type blobBuilder struct {
	b []byte
}

func (b *blobBuilder) add(s string) uint32 {
	at := uint32(len(b.b))
	b.b = append(b.b, s...)
	return at
}
func (b *blobBuilder) end() uint32    { return uint32(len(b.b)) }
func (b *blobBuilder) String() string { return string(b.b) }

// exportName decodes the name fields ExportRow leaves out.
type exportName struct {
	OriginalTitle string `json:"original_title"`
	OriginalName  string `json:"original_name"`
}

// buildTailIndex builds a tail index from export readers (gzip JSON lines,
// as TMDB publishes them; several days or the adult files may be
// concatenated gzip members). names may be nil. exclude, if set, leaves
// keys out (tests; the server indexes every id).
func buildTailIndex(movies, tv io.Reader, names *tailNames, exclude func(core.RatingKey) bool) (*tailIndex, error) {
	b := &tailBuilder{}
	for _, src := range []struct {
		r  io.Reader
		tv bool
	}{{movies, false}, {tv, true}} {
		if src.r == nil {
			continue
		}
		if _, _, err := tmdb.ParseExportNamed(src.r, func(row tmdb.ExportRow, name string) error {
			if exclude == nil || !exclude(titleKey(src.tv, row.ID)) {
				b.add(src.tv, row, name)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return b.build(names), nil
}

// tailState holds the current tail index and its lifecycle.
type tailState struct {
	cur      atomic.Pointer[tailHandle]
	building atomic.Bool
	enabled  atomic.Bool // RunTail runs on this node
	dir      string
	free     func(string) (int64, error)

	mu      sync.Mutex
	builtAt time.Time
	lastErr string
	path    string   // the current version's file
	stale   []string // retired files whose deletion failed
}

// tailHandle refcounts one index version, so the version a search is
// reading is unmapped (and its file deleted) only when the last reader
// is done, never under it. On Windows a mapped file cannot be removed.
type tailHandle struct {
	t       *tailIndex
	refs    atomic.Int64
	retired atomic.Bool
	once    sync.Once
	close   func() // unmaps and deletes the file; nil for an in-memory index
}

func (h *tailHandle) release() {
	if h.refs.Add(-1) == 0 && h.retired.Load() {
		h.finish()
	}
}

func (h *tailHandle) finish() {
	h.once.Do(func() {
		if h.close != nil {
			h.close()
		}
	})
}

// acquireTail returns the current tail index and a release function, or
// nil when there is none yet.
func (c *Catalog) acquireTail() (*tailIndex, func()) {
	for {
		h := c.tail.cur.Load()
		if h == nil {
			return nil, func() {}
		}
		h.refs.Add(1)
		if c.tail.cur.Load() == h {
			return h.t, h.release
		}
		h.release() // swapped meanwhile: take the new one
	}
}

// installTail makes h current and retires the previous version.
func (c *Catalog) installTail(h *tailHandle) {
	old := c.tail.cur.Swap(h)
	if old != nil {
		old.retired.Store(true)
		if old.refs.Load() == 0 {
			old.finish()
		}
	}
}

// setTailIndex makes t the tail index (tests, and in-memory builds).
func (c *Catalog) setTailIndex(t *tailIndex) {
	if t == nil {
		c.installTail(nil)
		return
	}
	c.installTail(&tailHandle{t: t})
}

// tailEntry is the tail's summary for key.
func (c *Catalog) tailEntry(key core.RatingKey) (tailEntry, bool) {
	t, release := c.acquireTail()
	defer release()
	if t == nil {
		return tailEntry{}, false
	}
	i, ok := t.find(key)
	if !ok {
		return tailEntry{}, false
	}
	return t.entry(i), true
}

// Findable reports whether key is a stored title or a long-tail entry:
// one a search can return and Get can open.
func (c *Catalog) Findable(ctx context.Context, key core.RatingKey) (bool, error) {
	if _, ok := c.tailEntry(key); ok {
		return true, nil
	}
	if _, err := c.loadByKey(ctx, key); err == nil {
		return true, nil
	} else if errors.Is(err, core.ErrNotFound) {
		return false, nil
	} else {
		return false, err
	}
}

// searchTail is the tail's best n matches for q, as summaries, with their
// scores. Adult titles are left out.
func (c *Catalog) searchTail(q searchQuery, wantMovie, wantShow bool, n int) ([]tailEntry, map[core.RatingKey]float64) {
	t, release := c.acquireTail()
	defer release()
	if t == nil {
		return nil, nil
	}
	hits := t.ix.search(q, wantMovie, wantShow, n)
	out := make([]tailEntry, 0, len(hits))
	scores := make(map[core.RatingKey]float64, len(hits))
	for _, h := range hits {
		e := t.entry(int(h.title))
		if e.Title.Adult {
			continue
		}
		scores[e.Title.Key] = h.score
		out = append(out, e)
	}
	return out, scores
}

// TailStatus describes the long-tail search index.
type TailStatus struct {
	// Enabled: this node keeps a tail index (nodes that serve search).
	Enabled   bool      `json:"enabled"`
	Ready     bool      `json:"ready"`
	Building  bool      `json:"building"`
	Day       time.Time `json:"day,omitempty"`
	Titles    int       `json:"titles"`
	Bytes     int64     `json:"bytes"`
	BuiltAt   time.Time `json:"builtAt,omitempty"`
	LastError string    `json:"lastError,omitempty"`
}

// TailStatus reports the long-tail index state.
func (c *Catalog) TailStatus() TailStatus {
	st := TailStatus{Enabled: c.tail.enabled.Load(), Building: c.tail.building.Load()}
	c.tail.mu.Lock()
	st.BuiltAt, st.LastError = c.tail.builtAt, c.tail.lastErr
	c.tail.mu.Unlock()
	t, release := c.acquireTail()
	defer release()
	if t != nil {
		st.Ready, st.Day, st.Titles = true, t.day, t.len()
		st.Bytes = t.bytes()
	}
	return st
}
