package plex

import (
	"hash/fnv"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// collectionShelvesQuirk names clients that lay out a library's promoted
// rows only when they are PMS's own row kinds. Infuse Direct Mode reads
// /hubs/promoted?contentDirectoryID=N once per library and draws a row
// for Recently Added, Recent Playlists and promoted collections
// (custom.collection.*, context hub.custom.collection). It gives other
// hubs no row of its own, and folds their items into the next row it
// does draw. Ether's recommendation rows are server-published shelves,
// which is what PMS's promoted collections are, so these clients get them
// in that form.
const collectionShelvesQuirk = "plex-promoted-rows-as-collections"

func (s *Server) wantsCollectionShelves(pr *Request) bool {
	return pr != nil && frontend.HasQuirk(pr.Product, pr.Platform, pr.Device, pr.Model, pr.Version, collectionShelvesQuirk)
}

// defaultPinnedDirectories is the pinned-library list a client that sends
// none would have pinned: every library the viewer can open, in order. A
// client that asks library by library then sees the Home rows once (with
// the first library), as Plex's own Home does, instead of once per library.
func (s *Server) defaultPinnedDirectories(u *frontend.User) string {
	secs := s.userSections(u)
	ids := make([]string, 0, len(secs))
	for _, sec := range secs {
		ids = append(ids, strconv.Itoa(sec.ID))
	}
	return strings.Join(ids, ",")
}

// collectionShelves reshapes one library's promoted rows for clients with
// collectionShelvesQuirk. PMS's built-in rows (resume, Recently Added) lead
// so no other row's items are folded into them, every Ether row follows as
// a promoted collection shelf (its key, items and order unchanged), and
// rows such a client cannot open (Live TV guide rows) are dropped. renamed
// maps each shelf identifier to the row it stands for.
func collectionShelves(rows []model.Hub, section int) (out []model.Hub, renamed map[string]shelfRef) {
	var builtIn, shelves, tail []model.Hub
	renamed = map[string]shelfRef{}
	for _, row := range rows {
		id := row.HubIdentifier
		switch {
		case pmsBuiltInHub(id):
			builtIn = append(builtIn, row)
		case id == "home.playlists":
			tail = append(tail, row)
		case strings.HasPrefix(id, "livetv.") || len(row.Metadata) == 0:
		default:
			n := collectionShelfNumber(row)
			row.HubIdentifier = "custom.collection." + strconv.Itoa(section) + "." + n + "." + n
			row.Context = "hub.custom.collection"
			renamed[row.HubIdentifier] = shelfRef{hub: id, key: row.Key}
			shelves = append(shelves, row)
		}
	}
	return append(append(builtIn, shelves...), tail...), renamed
}

// pmsBuiltInHub reports PMS's own per-library rows, which such clients
// recognize by identifier.
func pmsBuiltInHub(id string) bool {
	for _, p := range []string{"movie.recentlyadded.", "tv.recentlyadded.", "movie.inprogress.", "tv.inprogress."} {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return id == "home.continue" || id == "home.ondeck" || id == "continueWatching"
}

// collectionShelfNumber is the collection id in a shelf identifier: the
// backing collection's rating key when the row is one, otherwise a stable
// number derived from the row's identifier, so a client that remembers
// shelves by identifier keeps them across refreshes.
func collectionShelfNumber(row model.Hub) string {
	if rest, ok := strings.CutPrefix(row.Key, "/library/collections/"); ok {
		if k, _, ok := strings.Cut(rest, "/"); ok && k != "" {
			return k
		}
	}
	h := fnv.New32a()
	h.Write([]byte(row.HubIdentifier))
	return strconv.FormatUint(uint64(h.Sum32()), 10)
}

// shelfRef is the Ether row behind a collection shelf: its identifier and
// the key it publishes for its items.
type shelfRef struct {
	hub string
	key string
}

// maxShelfRefs bounds the shelf table; it holds one entry per published
// row identifier and library, so the bound is only a backstop.
const maxShelfRefs = 4096

// shelfRebuildEvery limits how often an unknown shelf number triggers a
// rebuild of the viewer's promoted rows (after a restart, before the
// client asked for them again).
const shelfRebuildEvery = 30 * time.Second

// rememberShelves records the rows collectionShelves renamed, so the
// client's /library/collections/{n}/children for a shelf can be answered.
func (s *Server) rememberShelves(renamed map[string]shelfRef) {
	s.shelfRefMu.Lock()
	defer s.shelfRefMu.Unlock()
	if s.shelfRefs == nil || len(s.shelfRefs)+len(renamed) > maxShelfRefs {
		s.shelfRefs = map[string]shelfRef{}
	}
	for id, ref := range renamed {
		parts := strings.Split(id, ".")
		if !strings.HasPrefix(ref.key, "/hubs/items/") {
			// Backed by a real collection: its own route answers it.
			continue
		}
		s.shelfRefs[parts[len(parts)-1]] = ref
	}
}

func (s *Server) shelfFor(n string) (shelfRef, bool) {
	s.shelfRefMu.Lock()
	defer s.shelfRefMu.Unlock()
	ref, ok := s.shelfRefs[n]
	return ref, ok
}

// serveCollectionShelf answers /library/collections/{n}/children for a
// shelf number collectionShelves published: a client with
// collectionShelvesQuirk loads each shelf's cards from the collection
// path its identifier names. The row's items are served exactly as the
// row's own key serves them (same paging).
func (s *Server) serveCollectionShelf(w http.ResponseWriter, r *http.Request) bool {
	pr := FromContext(r.Context())
	if !s.wantsCollectionShelves(pr) || !strings.HasSuffix(r.URL.Path, "/children") {
		return false
	}
	n := chi.URLParam(r, "key")
	if v, err := strconv.ParseUint(n, 10, 32); err != nil || strconv.FormatUint(v, 10) != n {
		return false
	}
	ref, ok := s.shelfFor(n)
	if !ok && s.rebuildShelves(r) {
		ref, ok = s.shelfFor(n)
	}
	if !ok {
		return false
	}
	key, err := url.Parse(ref.key)
	if err != nil {
		return false
	}
	q := r.URL.Query()
	for k, v := range key.Query() {
		q[k] = v
	}
	q.Set("identifier", ref.hub)
	r2 := r.Clone(r.Context())
	u := *r.URL
	u.Path, u.RawPath, u.RawQuery = "/hubs/items", "", q.Encode()
	r2.URL = &u
	s.handleHubItems(w, r2)
	return true
}

// rebuildShelves republishes the viewer's per-library promoted rows (at most
// every shelfRebuildEvery), refilling the shelf table after a restart. A
// client loads all its shelves at once, so misses during a rebuild wait for
// it rather than failing.
func (s *Server) rebuildShelves(r *http.Request) bool {
	s.shelfRefMu.Lock()
	if running := s.shelfRebuild; running != nil {
		s.shelfRefMu.Unlock()
		select {
		case <-running:
			return true
		case <-r.Context().Done():
			return false
		}
	}
	if time.Since(s.shelfRefRebuilt) < shelfRebuildEvery {
		s.shelfRefMu.Unlock()
		return false
	}
	done := make(chan struct{})
	s.shelfRefRebuilt, s.shelfRebuild = time.Now(), done
	s.shelfRefMu.Unlock()
	defer func() {
		s.shelfRefMu.Lock()
		s.shelfRebuild = nil
		s.shelfRefMu.Unlock()
		close(done)
	}()
	pr := FromContext(r.Context())
	for _, sec := range s.userSections(pr.User) {
		r2 := r.Clone(r.Context())
		u := *r.URL
		u.Path, u.RawPath = "/hubs/promoted", ""
		u.RawQuery = url.Values{"contentDirectoryID": {strconv.Itoa(sec.ID)}, "excludeContinueWatching": {"1"}}.Encode()
		r2.URL = &u
		s.buildHomeHubs(r2, true)
	}
	return true
}
