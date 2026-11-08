package experience

import (
	"strconv"
	"sync"
)

// A person's facet identifier is a hash of their TMDB id, so a person page
// cannot name the person without inverting it. Every identifier Ether hands
// out is minted by personFacetID, which remembers the inverse; a person page
// then reads that person's own credits by index instead of walking every
// credited title in the catalogue. The memo is bounded: past personIDsCap
// entries the older half is dropped, and a page for a forgotten identifier
// falls back to the walk.
const personIDsCap = 1 << 17

var personIDs = struct {
	sync.Mutex
	cur, old map[string]int64
}{cur: map[string]int64{}}

func personFacetID(tmdb int64) string {
	id := facetID("person", strconv.FormatInt(tmdb, 10))
	personIDs.Lock()
	if _, ok := personIDs.cur[id]; !ok {
		if len(personIDs.cur) >= personIDsCap/2 {
			personIDs.old, personIDs.cur = personIDs.cur, make(map[string]int64, len(personIDs.cur))
		}
		personIDs.cur[id] = tmdb
	}
	personIDs.Unlock()
	return id
}

// personTMDB inverts an identifier personFacetID handed out.
func personTMDB(id string) (int64, bool) {
	personIDs.Lock()
	defer personIDs.Unlock()
	if tmdb, ok := personIDs.cur[id]; ok {
		return tmdb, true
	}
	tmdb, ok := personIDs.old[id]
	return tmdb, ok
}
