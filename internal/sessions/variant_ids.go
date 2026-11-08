package sessions

import (
	"context"
	"database/sql"
	"hash/fnv"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("variants", 1, `
CREATE TABLE variants_ids (
  media_id INTEGER PRIMARY KEY,
  rating_key INTEGER NOT NULL,
  release_id TEXT NOT NULL
);
CREATE UNIQUE INDEX variants_ids_pair ON variants_ids(rating_key, release_id);`)
}

const (
	variantIDBase  = 9_000_000_000_000_000
	variantIDSpace = 7_199_254_740_990 // ids stay below 2^53

	maxVariantIDs = 200_000
)

// ---------------------------------------------------------------------------
// Variant Media/Part ids

type VariantRef struct {
	Key       core.RatingKey
	ReleaseID string
}

// variantEntry is a registry id's mapping and its last_seen as last read
// or touched (Unix seconds; see variants_retention.go).
type variantEntry struct {
	ref  VariantRef
	seen int64
}

type VariantIDs struct {
	mu     sync.Mutex
	db     *sql.DB
	byID   map[int64]variantEntry
	byPair map[VariantRef]int64
	loaded bool
	// pending holds ids whose last_seen write is queued for the flusher.
	pending map[int64]struct{}
	now     func() time.Time
}

func NewVariantIDs(db *sql.DB, now func() time.Time) *VariantIDs {
	if now == nil {
		now = time.Now
	}
	return &VariantIDs{db: db, byID: map[int64]variantEntry{}, byPair: map[VariantRef]int64{}, pending: map[int64]struct{}{}, now: now}
}

// loadLocked reads persisted mappings once; the caller holds mu.
func (r *VariantIDs) loadLocked() {
	if r.loaded {
		return
	}
	r.loaded = true
	if r.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `SELECT media_id, rating_key, release_id, last_seen FROM variants_ids`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, rk, seen int64
		var rel string
		if rows.Scan(&id, &rk, &rel, &seen) == nil {
			ref := VariantRef{Key: core.RatingKey(rk), ReleaseID: rel}
			r.byID[id] = variantEntry{ref: ref, seen: seen}
			r.byPair[ref] = id
		}
	}
	if err := rows.Err(); err != nil {
		slog.Default().Warn("plex: variant ids not fully loaded", "err", err)
	}
}

func VariantHash(key core.RatingKey, releaseID string) int64 {
	h := fnv.New64a()
	h.Write([]byte(strconv.FormatInt(int64(key), 10) + "/" + releaseID))
	return variantIDBase + int64(h.Sum64()%variantIDSpace) + 1
}

// idFor returns the stable Media/Part id of a non-Auto variant.
func (r *VariantIDs) IDFor(key core.RatingKey, releaseID string) int64 {
	ref := VariantRef{Key: key, ReleaseID: releaseID}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loadLocked()
	if id, ok := r.byPair[ref]; ok {
		r.touchLocked(id)
		return id
	}
	if len(r.byID) >= maxVariantIDs && r.db == nil {
		r.byID = map[int64]variantEntry{}
		r.byPair = map[VariantRef]int64{}
	}
	id := VariantHash(key, releaseID)
	for {
		existing, ok := r.byID[id]
		if !ok || existing.ref == ref {
			break
		}
		id++ // collision: probe (the space is ~10^15 wide, so this terminates)
		if id >= variantIDBase+variantIDSpace {
			id = variantIDBase + 1
		}
	}
	seen := r.now().Unix()
	r.byID[id] = variantEntry{ref: ref, seen: seen}
	r.byPair[ref] = id
	if r.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = r.db.ExecContext(ctx,
			`INSERT INTO variants_ids(media_id, rating_key, release_id, last_seen) VALUES (?,?,?,?) ON CONFLICT DO NOTHING`,
			id, int64(key), releaseID, seen)
		cancel()
	}
	return id
}

func (r *VariantIDs) Lookup(id int64) (VariantRef, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loadLocked()
	e, ok := r.byID[id]
	if ok {
		r.touchLocked(id)
	}
	return e.ref, ok
}
