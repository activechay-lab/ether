package experience

import (
	"context"
	"strconv"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
)

// A person's facet identifier is a hash of their TMDB id, so a person page
// cannot name the person without inverting it. Every identifier Ether hands
// out is minted by personFacetID, which remembers the inverse; a person page
// then reads that person's own credits by index instead of walking every
// credited title in the catalogue. The memo is bounded: past personIDsCap
// entries the older half is dropped. An identifier it forgot, or one a
// client kept across a restart, is read from the catalogue's person
// vocabulary, which carries every credited person's identifier under an
// index (personFacetTMDB).
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

// personFacetTMDB inverts a person facet identifier: from the memo, else
// from the indexed person vocabulary (Deps.PersonByFacetID), remembering
// the answer. ok is false when neither knows it.
func (s *Service) personFacetTMDB(ctx context.Context, id string) (int64, bool, error) {
	if tmdb, ok := personTMDB(id); ok {
		return tmdb, true, nil
	}
	if s.d.PersonByFacetID == nil {
		return 0, false, nil
	}
	tmdb, ok, err := s.d.PersonByFacetID(core.WithoutCatalogViewer(ctx), id)
	if err != nil || !ok {
		return 0, false, err
	}
	personFacetID(tmdb)
	return tmdb, true, nil
}
