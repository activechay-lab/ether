package plex

import (
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plex/model"
	"github.com/braedonsaunders/ether/internal/transcode"
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
	return pr != nil && transcode.HasQuirk(pr.Product, pr.Platform, pr.Device, pr.Model, pr.Version, collectionShelvesQuirk)
}

// defaultPinnedDirectories is the pinned-library list a client that sends
// none would have pinned: every library the viewer can open, in order. A
// client that asks library by library then sees the Home rows once (with
// the first library), as Plex's own Home does, instead of once per library.
func (s *Server) defaultPinnedDirectories(u *core.User) string {
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
// maps each new identifier to the row's own one.
func collectionShelves(rows []model.Hub, section int) (out []model.Hub, renamed map[string]string) {
	var builtIn, shelves, tail []model.Hub
	renamed = map[string]string{}
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
			renamed[row.HubIdentifier] = id
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
