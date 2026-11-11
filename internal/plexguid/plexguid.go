// Package plexguid maps verified external identities through Plex's metadata
// provider. It never invents plex:// IDs or grants Plex account entitlements.
package plexguid

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("plexguid", 1, `CREATE TABLE plexguid_mappings(rating_key INTEGER PRIMARY KEY,guid TEXT NOT NULL,checked_at INTEGER NOT NULL);`)
	store.Register("plexguid", 3, `CREATE TABLE plexguid_verified(rating_key INTEGER PRIMARY KEY, guid TEXT NOT NULL);`)
	// Older child mappings did not retain their parent identity. Keep serving
	// them while the repair worker checks their ancestry.
	store.Register("plexguid", 4, `CREATE TABLE plexguid_parentage(rating_key INTEGER PRIMARY KEY, parent_key INTEGER NOT NULL, parent_guid TEXT NOT NULL);`)
	store.Register("plexguid", 5, `CREATE INDEX plexguid_parentage_parent ON plexguid_parentage(parent_key);`)
	store.Register("plexguid", 2, `CREATE INDEX plexguid_mappings_guid ON plexguid_mappings(guid);`)
	// A newly written mapping records the canonical Plex record's external id
	// (TMDB) and, for a movie or show, its title and year, and is served only
	// while that record matches its guid. Rows written before this table
	// existed have no record: they keep serving as legacy while the repair
	// worker re-verifies each under the stricter rule.
	store.Register("plexguid", 6, `CREATE TABLE plexguid_identity(rating_key INTEGER PRIMARY KEY, guid TEXT NOT NULL, tmdb INTEGER NOT NULL DEFAULT 0, title TEXT NOT NULL DEFAULT '', year INTEGER NOT NULL DEFAULT 0);`)
}

const (
	cacheTTL          = 30 * 24 * time.Hour
	recheckInterval   = 24 * time.Hour
	bgWorkers         = 2
	bgQueueSize       = 256
	bgLookupTimeout   = 3 * time.Second
	childrenPageSize  = 50
	maxChildrenItems  = 2500
	priorityQueueSize = 256
	namesakeCacheMax  = 4096
)

type Options struct {
	HTTP *http.Client
	// ClientIdentifier is the per-install X-Plex-Client-Identifier.
	ClientIdentifier func() string
	BaseURL          string
	Token            func() string
	Background       bool
	// Namesakes reports whether the catalogue holds another title of the same
	// type that shares this key's normalized name and a near year. A legacy
	// mapping (stored before identity records existed) on a title with a
	// namesake cannot be trusted: the guid may name the other work. It is an
	// optional dependency, injected by the app so this package never imports
	// the catalogue. Nil disables the check.
	Namesakes func(context.Context, core.RatingKey) (bool, error)
}

type Service struct {
	db *sql.DB
	o  Options

	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	closeOne sync.Once

	mu      sync.Mutex
	idle    *sync.Cond
	pending map[core.RatingKey]bool
	// force marks queued keys whose cached mapping must be re-verified rather
	// than served from the hot-path cache: the repair worker's rows.
	force map[core.RatingKey]bool
	jobs  chan core.RatingKey
	// priority is a bounded, deduped queue of keys a read path found withheld
	// as a legacy namesake. repairMappings drains it before advancing its scan
	// cursor so those rows are re-verified within seconds; a full queue drops
	// the request and the background scan eventually reaches the row.
	priority   chan core.RatingKey
	prioQueued map[core.RatingKey]bool
	// namesakeCache remembers the (rare, indexed) namesake answer per key so
	// the read path never repeats the lookup. It is only consulted for legacy
	// rows, which repair converts or drops, so it stays small; namesakeCacheMax
	// bounds it regardless.
	namesakeCache map[core.RatingKey]bool
}

func New(db *sql.DB, o Options) *Service {
	if o.HTTP == nil {
		o.HTTP = egress.Client(egress.CapDirect, 5*time.Second)
	}
	if o.BaseURL == "" {
		o.BaseURL = "https://metadata.provider.plex.tv"
	}
	if o.ClientIdentifier == nil {
		// Unconfigured fixtures still get an id of their own, fixed for
		// this Service, never one shared across installs.
		fixed := ClientIdentifier("", "")
		o.ClientIdentifier = func() string { return fixed }
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		db:            db,
		o:             o,
		ctx:           ctx,
		cancel:        cancel,
		pending:       map[core.RatingKey]bool{},
		force:         map[core.RatingKey]bool{},
		prioQueued:    map[core.RatingKey]bool{},
		namesakeCache: map[core.RatingKey]bool{},
	}
	s.idle = sync.NewCond(&s.mu)
	if o.Background {
		s.jobs = make(chan core.RatingKey, bgQueueSize)
		s.priority = make(chan core.RatingKey, priorityQueueSize)
		for i := 0; i < bgWorkers; i++ {
			s.wg.Add(1)
			go s.worker()
		}
		s.wg.Add(1)
		go s.repairMappings()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			<-ctx.Done()
			s.mu.Lock()
			s.idle.Broadcast()
			s.mu.Unlock()
		}()
	}
	return s
}

// ClientIdentifier returns the per-install X-Plex-Client-Identifier used by s.
func (s *Service) ClientIdentifier() string {
	if s == nil {
		return ""
	}
	return s.clientIdentifier()
}

func (s *Service) Close() {
	s.closeOne.Do(func() {
		s.cancel()
		s.wg.Wait()
	})
}

// Wait blocks until all queued background GUID resolutions finish.
func (s *Service) Wait() {
	if s.jobs == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.pending) > 0 && s.ctx.Err() == nil {
		s.idle.Wait()
	}
}

// Schedule queues keys for asynchronous plex:// GUID resolution when
// Background is enabled. Safe to call on hot render paths.
func (s *Service) Schedule(keys ...core.RatingKey) {
	if s == nil || s.jobs == nil || s.ctx.Err() != nil {
		return
	}
	if s.o.Token == nil || s.o.Token() == "" {
		return
	}
	for _, k := range keys {
		if !resolvableKey(k) {
			continue
		}
		s.mu.Lock()
		if s.pending[k] {
			s.mu.Unlock()
			continue
		}
		s.pending[k] = true
		s.mu.Unlock()
		select {
		case s.jobs <- k:
		default:
			s.done(k)
		}
	}
}

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case k := <-s.jobs:
			func() {
				defer s.done(k)
				rctx, cancel := context.WithTimeout(s.ctx, bgLookupTimeout)
				defer cancel()
				s.mu.Lock()
				forced := s.force[k]
				delete(s.force, k)
				s.mu.Unlock()
				if forced {
					_, _ = s.reverify(rctx, k)
				} else {
					_, _ = s.Resolve(rctx, k)
				}
			}()
		}
	}
}

// repairMappings re-verifies stored identities under the current rule and
// revisits children whose parentage was not recorded. It alternates the two
// queues so legacy children get priority without starving other work. A row
// stored before identity records existed (or under a looser rule) carries no
// plexguid_identity row; it keeps serving on the hot path while this pass
// checks its stored guid against the canonical Plex record, granting the
// identity record when it proves out and dropping it otherwise.
func (s *Service) repairMappings() {
	defer s.wg.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var after [2]int64
	var turn int
	queries := [2]string{
		`SELECT m.rating_key FROM plexguid_mappings m WHERE m.rating_key>? AND NOT EXISTS (SELECT 1 FROM plexguid_identity i WHERE i.rating_key=m.rating_key AND i.guid=m.guid) ORDER BY m.rating_key LIMIT 1`,
		`SELECT m.rating_key FROM plexguid_mappings m WHERE m.rating_key>? AND m.rating_key>=3000000000000000 AND m.rating_key<5000000000000000 AND m.guid<>'' AND NOT EXISTS (SELECT 1 FROM plexguid_parentage p WHERE p.rating_key=m.rating_key) ORDER BY m.rating_key LIMIT 1`,
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
		if s.o.Token == nil || s.o.Token() == "" {
			continue
		}
		// Read-path withheld keys are re-verified before the scan advances:
		// a client is actively rendering them wrong right now.
		select {
		case k := <-s.priority:
			s.mu.Lock()
			delete(s.prioQueued, k)
			s.mu.Unlock()
			s.queueRepair(k)
			continue
		default:
		}
		var key int64
		queue := turn
		turn = 1 - turn
		err := s.db.QueryRowContext(s.ctx, queries[queue], after[queue]).Scan(&key)
		if err == sql.ErrNoRows {
			after[queue] = 0
			queue = 1 - queue
			err = s.db.QueryRowContext(s.ctx, queries[queue], after[queue]).Scan(&key)
		}
		if err == sql.ErrNoRows {
			after[queue] = 0
			timer := time.NewTimer(time.Minute)
			select {
			case <-s.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		if err != nil {
			continue
		}
		k := core.RatingKey(key)
		if !resolvableKey(k) {
			after[queue] = key
			continue
		}
		s.mu.Lock()
		if s.pending[k] {
			s.mu.Unlock()
			after[queue] = key
			continue
		}
		s.pending[k] = true
		s.force[k] = true
		s.mu.Unlock()
		select {
		case s.jobs <- k:
			after[queue] = key
		case <-s.ctx.Done():
			s.done(k)
			return
		default:
			s.done(k)
		}
	}
}

func (s *Service) done(k core.RatingKey) {
	s.mu.Lock()
	delete(s.pending, k)
	delete(s.force, k)
	if len(s.pending) == 0 {
		s.idle.Broadcast()
	}
	s.mu.Unlock()
}

var validGUID = regexp.MustCompile(`^plex://(movie|show|season|episode)/[a-f0-9]{24}$`)

func resolvableKey(k core.RatingKey) bool {
	switch k.Kind() {
	case core.KindMovie, core.KindShow:
		return k.TMDB() > 0
	case core.KindSeason:
		return k.TMDB() > 0 && k.Season() >= 0 && k.Season() < 9000
	case core.KindEpisode:
		return k.TMDB() > 0 && k.Season() >= 0 && k.Season() < 9000 && k.Episode() > 0
	default:
		return false
	}
}

// identityVerified is the condition under which a stored mapping may be
// served. A mapping written under this rule carries an identity record for the
// same guid. A mapping stored before identity records existed (a legacy row)
// has no record and keeps serving while repairMappings re-verifies it; once a
// record exists it must match, so a re-verification that disproves the guid
// withholds it. Every mapping must still be verified.
const identityVerified = `(NOT EXISTS (SELECT 1 FROM plexguid_verified v WHERE v.rating_key=m.rating_key) OR EXISTS (SELECT 1 FROM plexguid_verified v WHERE v.rating_key=m.rating_key AND v.guid=m.guid)) AND (NOT EXISTS (SELECT 1 FROM plexguid_identity i WHERE i.rating_key=m.rating_key) OR EXISTS (SELECT 1 FROM plexguid_identity i WHERE i.rating_key=m.rating_key AND i.guid=m.guid))`

// identityExists is true when the mapping row for m carries an identity record
// matching its guid. A false value on a row with a guid is what makes it a
// legacy row: one that identityVerified serves only because no record exists.
const identityExists = `EXISTS (SELECT 1 FROM plexguid_identity i WHERE i.rating_key=m.rating_key AND i.guid=m.guid)`

// hasNamesake reports whether the catalogue holds another title that shares
// this key's name and a near year. The answer is cached per key: it is read on
// the hot path and only ever for legacy rows. A lookup error is not cached and
// is treated as "no namesake" so a transient catalogue error cannot withhold
// every mapping.
func (s *Service) hasNamesake(ctx context.Context, k core.RatingKey) bool {
	if s == nil || s.o.Namesakes == nil {
		return false
	}
	s.mu.Lock()
	v, ok := s.namesakeCache[k]
	s.mu.Unlock()
	if ok {
		return v
	}
	has, err := s.o.Namesakes(ctx, k)
	if err != nil {
		return false
	}
	s.mu.Lock()
	if len(s.namesakeCache) < namesakeCacheMax {
		s.namesakeCache[k] = has
	}
	s.mu.Unlock()
	return has
}

// legacyMapping reports whether k currently has a stored guid served only as a
// legacy row (no identity record for it). One indexed primary-key read.
func (s *Service) legacyMapping(ctx context.Context, k core.RatingKey) bool {
	var legacy bool
	_ = s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM plexguid_mappings m WHERE m.rating_key=? AND m.guid<>'' AND NOT (`+identityExists+`))`, int64(k)).Scan(&legacy)
	return legacy
}

// withheldLegacy reports whether a stored mapping for k must not be served
// because it is a legacy row on a title the catalogue says has a namesake. The
// guid may name the other work, so it is withheld from every read path and the
// key is enqueued for priority re-verification. Non-legacy rows (a verified
// identity record exists) and rows on namesake-free titles keep serving.
func (s *Service) withheldLegacy(ctx context.Context, k core.RatingKey) bool {
	if s == nil || s.o.Namesakes == nil {
		return false
	}
	if !s.legacyMapping(ctx, k) {
		return false
	}
	if !s.hasNamesake(ctx, k) {
		return false
	}
	s.enqueuePriority(k)
	return true
}

// enqueuePriority hands a read-path-withheld key to repairMappings ahead of its
// background scan. The queue is bounded and deduped; when it is full the key is
// dropped (the scan still reaches it eventually). No-op when Background is off.
func (s *Service) enqueuePriority(k core.RatingKey) {
	if s == nil || s.priority == nil || s.ctx.Err() != nil {
		return
	}
	if !resolvableKey(k) {
		return
	}
	s.mu.Lock()
	if s.prioQueued[k] {
		s.mu.Unlock()
		return
	}
	s.prioQueued[k] = true
	s.mu.Unlock()
	select {
	case s.priority <- k:
	default:
		s.mu.Lock()
		delete(s.prioQueued, k)
		s.mu.Unlock()
	}
}

// queueRepair marks k for forced re-verification and sends it to a worker. It
// returns false when the key is already pending or the worker queue is full.
func (s *Service) queueRepair(k core.RatingKey) bool {
	if !resolvableKey(k) {
		return false
	}
	s.mu.Lock()
	if s.pending[k] {
		s.mu.Unlock()
		return false
	}
	s.pending[k] = true
	s.force[k] = true
	s.mu.Unlock()
	select {
	case s.jobs <- k:
		return true
	default:
		s.done(k)
		return false
	}
}

// Cached is safe for lists: no network requests or credentials are needed.
// A legacy row whose title has a catalogue namesake is withheld (the guid may
// name the other work) and its key is enqueued for priority re-verification.
func (s *Service) Cached(ctx context.Context, k core.RatingKey) string {
	var guid string
	var hasIdentity bool
	_ = s.db.QueryRowContext(ctx, `SELECT m.guid, `+identityExists+` FROM plexguid_mappings m WHERE m.rating_key=? AND m.checked_at>? AND `+identityVerified, int64(k), time.Now().Add(-cacheTTL).Unix()).Scan(&guid, &hasIdentity)
	if guid != "" && !hasIdentity && s.hasNamesake(ctx, k) {
		s.enqueuePriority(k)
		return ""
	}
	return guid
}

// CachedMany batch-loads cached non-empty plex:// GUIDs for keys without
// network activity.
func (s *Service) CachedMany(ctx context.Context, keys []core.RatingKey) map[core.RatingKey]string {
	if s == nil || s.db == nil || len(keys) == 0 {
		return nil
	}
	uniq := make([]core.RatingKey, 0, len(keys))
	seen := make(map[core.RatingKey]struct{}, len(keys))
	for _, k := range keys {
		if k == 0 {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		uniq = append(uniq, k)
	}
	if len(uniq) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-cacheTTL).Unix()
	out := make(map[core.RatingKey]string, len(uniq))
	const chunkSize = 200
	for start := 0; start < len(uniq); start += chunkSize {
		end := min(start+chunkSize, len(uniq))
		chunk := uniq[start:end]
		var b strings.Builder
		b.WriteString(`SELECT m.rating_key,m.guid, ` + identityExists + ` FROM plexguid_mappings m WHERE m.rating_key IN (`)
		args := make([]any, 0, len(chunk)+1)
		for i, k := range chunk {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('?')
			args = append(args, int64(k))
		}
		b.WriteString(`) AND m.checked_at>? AND ` + identityVerified)
		args = append(args, cutoff)
		rows, err := s.db.QueryContext(ctx, b.String(), args...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var rk int64
			var guid string
			var hasIdentity bool
			if err := rows.Scan(&rk, &guid, &hasIdentity); err == nil && guid != "" {
				k := core.RatingKey(rk)
				// A legacy row on a namesake title is withheld, never served.
				if !hasIdentity && s.hasNamesake(ctx, k) {
					s.enqueuePriority(k)
					continue
				}
				out[k] = guid
			}
		}
		if err := rows.Err(); err != nil {
			slog.Default().Warn("plexguid: cached mappings not fully loaded", "err", err)
		}
		_ = rows.Close()
	}
	return out
}

func (s *Service) freshCached(ctx context.Context, k core.RatingKey) (string, bool) {
	var cached string
	var at int64
	var hasIdentity bool
	if e := s.db.QueryRowContext(ctx, `SELECT m.guid,m.checked_at, `+identityExists+` FROM plexguid_mappings m WHERE m.rating_key=? AND (m.guid='' OR `+identityVerified+`) AND (m.rating_key<3000000000000000 OR m.rating_key>=5000000000000000 OR m.guid='' OR EXISTS (SELECT 1 FROM plexguid_parentage p WHERE p.rating_key=m.rating_key))`, int64(k)).Scan(&cached, &at, &hasIdentity); e == nil && time.Since(time.Unix(at, 0)) < recheckInterval {
		// A legacy row on a namesake title is not fresh: resolution re-verifies
		// it inline, which either proves the guid or drops it.
		if cached != "" && !hasIdentity && s.hasNamesake(ctx, k) {
			s.enqueuePriority(k)
			return "", false
		}
		return cached, true
	}
	return "", false
}

// identityRecord is the evidence that a plex:// guid belongs to one local
// title: the canonical Plex record's TMDB id and, for movies and shows, its
// title and year. An empty GUID (no verified identity) stores no record.
type identityRecord struct {
	GUID  string
	TMDB  int64
	Title string
	Year  int
}

// storeMapping records a title's own identity. A guid is stored with the
// evidence its resolution proved; storeMapping's callers each supply it.
func (s *Service) storeMapping(ctx context.Context, k core.RatingKey, id identityRecord, now int64) error {
	return s.storeMappingIdentity(ctx, k, id, now, 0, "")
}

func (s *Service) storeMappingUnder(ctx context.Context, k core.RatingKey, id identityRecord, now int64, parent core.RatingKey, parentGUID string) error {
	return s.storeMappingIdentity(ctx, k, id, now, parent, parentGUID)
}

func (s *Service) storeMappingIdentity(ctx context.Context, k core.RatingKey, id identityRecord, now int64, parent core.RatingKey, parentGUID string) error {
	guid := id.GUID
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if parent != 0 {
		var current string
		err = tx.QueryRowContext(ctx, `SELECT m.guid FROM plexguid_mappings m JOIN plexguid_verified v ON v.rating_key=m.rating_key AND v.guid=m.guid WHERE m.rating_key=?`, int64(parent)).Scan(&current)
		if err != nil || current != parentGUID {
			return fmt.Errorf("Plex parent identity changed")
		}
	}
	var old string
	if err = tx.QueryRowContext(ctx, `SELECT guid FROM plexguid_mappings WHERE rating_key=?`, int64(k)).Scan(&old); err != nil && err != sql.ErrNoRows {
		return err
	}
	if old != guid {
		// Delete only children whose recorded parentage proves they belong
		// to the old tree. Unknown legacy children stay available.
		remove := func(query string, args ...any) error {
			for _, table := range []string{"plexguid_verified", "plexguid_identity", "plexguid_mappings", "plexguid_parentage"} {
				if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rating_key IN (`+query+`)`, args...); err != nil {
					return err
				}
			}
			return nil
		}
		if k.Kind() == core.KindShow {
			q := `SELECT ep.rating_key FROM plexguid_parentage ep JOIN plexguid_parentage sea ON ep.parent_key=sea.rating_key WHERE sea.parent_key=? AND sea.parent_guid<>?`
			if err = remove(q, int64(k), guid); err != nil {
				return err
			}
		}
		if k.Kind() == core.KindShow || k.Kind() == core.KindSeason {
			q := `SELECT rating_key FROM plexguid_parentage WHERE parent_key=? AND parent_guid<>?`
			if err = remove(q, int64(k), guid); err != nil {
				return err
			}
		}
		// A child's proof is tied to its parent's identity. Drop those proofs
		// when a parent changes so they are resolved through the new tree.
		var ranges [][2]core.RatingKey
		switch k.Kind() {
		case core.KindShow:
			ranges = append(ranges, [2]core.RatingKey{core.SeasonKey(k.TMDB(), 0), core.SeasonKey(k.TMDB()+1, 0)}, [2]core.RatingKey{core.EpisodeKey(k.TMDB(), 0, 0), core.EpisodeKey(k.TMDB()+1, 0, 0)})
		case core.KindSeason:
			ranges = append(ranges, [2]core.RatingKey{core.EpisodeKey(k.TMDB(), k.Season(), 0), core.EpisodeKey(k.TMDB(), k.Season()+1, 0)})
		}
		for _, bounds := range ranges {
			for _, table := range []string{"plexguid_verified", "plexguid_identity"} {
				if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rating_key>=? AND rating_key<?`, int64(bounds[0]), int64(bounds[1])); err != nil {
					return err
				}
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plexguid_mappings(rating_key,guid,checked_at) VALUES(?,?,?) ON CONFLICT(rating_key) DO UPDATE SET guid=excluded.guid,checked_at=excluded.checked_at`, int64(k), guid, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plexguid_verified(rating_key,guid) VALUES(?,?) ON CONFLICT(rating_key) DO UPDATE SET guid=excluded.guid`, int64(k), guid); err != nil {
		return err
	}
	// Only a verified, externally identified record may be served; any other
	// mapping is stored without proof so no client sees an unverified guid.
	verified := id.TMDB > 0
	if _, err = tx.ExecContext(ctx, `DELETE FROM plexguid_identity WHERE rating_key=?`, int64(k)); err != nil {
		return err
	}
	if verified {
		if _, err = tx.ExecContext(ctx, `INSERT INTO plexguid_identity(rating_key,guid,tmdb,title,year) VALUES(?,?,?,?,?) ON CONFLICT(rating_key) DO UPDATE SET guid=excluded.guid,tmdb=excluded.tmdb,title=excluded.title,year=excluded.year`, int64(k), guid, id.TMDB, id.Title, id.Year); err != nil {
			return err
		}
	}
	if parent != 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO plexguid_parentage(rating_key,parent_key,parent_guid) VALUES(?,?,?) ON CONFLICT(rating_key) DO UPDATE SET parent_key=excluded.parent_key,parent_guid=excluded.parent_guid`, int64(k), int64(parent), parentGUID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) Resolve(ctx context.Context, k core.RatingKey) (string, error) {
	return s.resolve(ctx, k, true)
}

// reverify is the repair worker's entry point. For a stored movie or show it
// re-checks the existing guid against its canonical record: a pass grants the
// identity record so the row keeps serving, a fail falls back to a fresh
// search (which may find the right guid) and otherwise drops the mapping. Any
// other key (season, episode, or an empty-guid negative cache) resolves
// through the normal path without the hot-cache shortcut.
func (s *Service) reverify(ctx context.Context, k core.RatingKey) (string, error) {
	if !resolvableKey(k) || (k.Kind() != core.KindMovie && k.Kind() != core.KindShow) {
		return s.resolve(ctx, k, false)
	}
	if s.o.Token == nil || s.o.Token() == "" {
		return "", core.ErrUnauthorized
	}
	kind := "movie"
	if k.Kind() == core.KindShow {
		kind = "show"
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, `SELECT guid FROM plexguid_mappings WHERE rating_key=?`, int64(k)).Scan(&stored); err != nil {
		stored = ""
	}
	if validKindGUID(stored, kind) {
		id, ok, err := s.verifyTitle(ctx, k, kind, stored, s.localIdentity(ctx, k))
		if err != nil {
			return "", err
		}
		if ok {
			// Keep the exact stored guid and record its proof.
			if _, err := s.db.ExecContext(ctx, `INSERT INTO plexguid_verified(rating_key,guid) VALUES(?,?) ON CONFLICT(rating_key) DO UPDATE SET guid=excluded.guid`, int64(k), stored); err != nil {
				return "", err
			}
			return stored, s.storeMappingIdentity(ctx, k, id, time.Now().Unix(), 0, "")
		}
	}
	return s.resolve(ctx, k, false)
}

// resolve is Resolve with an explicit fresh-cache gate. The repair worker
// calls it with useCache=false so a legacy row (which serves on the hot path
// without proof) is still re-verified under the stricter rule.
func (s *Service) resolve(ctx context.Context, k core.RatingKey, useCache bool) (string, error) {
	if !resolvableKey(k) {
		return "", nil
	}
	if useCache {
		if cached, ok := s.freshCached(ctx, k); ok {
			return cached, nil
		}
	}
	if s.o.Token == nil || s.o.Token() == "" {
		return "", core.ErrUnauthorized
	}
	switch k.Kind() {
	case core.KindMovie, core.KindShow:
		kind, typ := "movie", "1"
		if k.Kind() == core.KindShow {
			kind, typ = "show", "2"
		}
		guid, err := s.resolveMovieOrShow(ctx, k, kind, typ)
		if err == nil && guid == "" {
			// A namesake from another year or an unverified title no longer
			// proves a mapping; drop it so Plex apps stop loading its cast
			// and metadata instead of retaining a wrong plex:// identity.
			_, _ = s.db.ExecContext(ctx, `DELETE FROM plexguid_mappings WHERE rating_key=? AND guid<>'' AND NOT EXISTS (SELECT 1 FROM plexguid_identity i WHERE i.rating_key=plexguid_mappings.rating_key AND i.guid=plexguid_mappings.guid)`, int64(k))
		}
		return guid, err
	case core.KindSeason:
		return s.resolveSeason(ctx, k)
	case core.KindEpisode:
		return s.resolveEpisode(ctx, k)
	default:
		return "", nil
	}
}

type providerItem struct {
	GUID            string `json:"guid"`
	RatingKey       string `json:"ratingKey"`
	Type            string `json:"type"`
	Year            int    `json:"year"`
	Index           *int   `json:"index"`
	ParentIndex     *int   `json:"parentIndex"`
	ParentGUID      string `json:"parentGuid"`
	GrandparentGUID string `json:"grandparentGuid"`
	Title           string `json:"title"`
	IDs             []struct {
		ID string `json:"id"`
	} `json:"Guid"`
}

func (s *Service) getPage(ctx context.Context, path string, q url.Values) ([]providerItem, int, error) {
	u := strings.TrimRight(s.o.BaseURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return nil, 0, fmt.Errorf("Plex metadata request invalid")
	}
	req.Header.Set("X-Plex-Token", s.o.Token())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", "Ether")
	req.Header.Set("X-Plex-Client-Identifier", s.clientIdentifier())
	// Never forward account credentials to a redirect target.
	client := *s.o.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, e := client.Do(req)
	if e != nil {
		return nil, 0, fmt.Errorf("Plex metadata unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, 0, core.ErrNotFound
	}
	if resp.StatusCode != 200 {
		return nil, 0, fmt.Errorf("Plex metadata HTTP %d", resp.StatusCode)
	}
	var body struct {
		Container struct {
			TotalSize int            `json:"totalSize"`
			Metadata  []providerItem `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); e != nil {
		return nil, 0, fmt.Errorf("Plex metadata response invalid")
	}
	return body.Container.Metadata, body.Container.TotalSize, nil
}

func (s *Service) getItems(ctx context.Context, path string, q url.Values) ([]providerItem, error) {
	items, _, err := s.getPage(ctx, path, q)
	return items, err
}

func (s *Service) getChildren(ctx context.Context, hex string) ([]providerItem, error) {
	path := "/library/metadata/" + url.PathEscape(hex) + "/children"
	var all []providerItem
	for start := 0; start < maxChildrenItems; {
		q := url.Values{
			"X-Plex-Container-Start": {strconv.Itoa(start)},
			"X-Plex-Container-Size":  {strconv.Itoa(childrenPageSize)},
		}
		page, totalSize, err := s.getPage(ctx, path, q)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		n := len(page)
		if n == 0 {
			break
		}
		start += n
		if totalSize > 0 {
			if start >= totalSize {
				break
			}
		} else if n < childrenPageSize {
			break
		}
	}
	return all, nil
}

func (s *Service) clientIdentifier() string {
	if s.o.ClientIdentifier != nil {
		if id := strings.TrimSpace(s.o.ClientIdentifier()); id != "" {
			return id
		}
	}
	return ""
}

func validKindGUID(guid, kind string) bool {
	return validGUID.MatchString(guid) && strings.HasPrefix(guid, "plex://"+kind+"/")
}

func guidHex(guid, kind string) string {
	prefix := "plex://" + kind + "/"
	if !validKindGUID(guid, kind) {
		return ""
	}
	return strings.TrimPrefix(guid, prefix)
}

func hasExternalID(item providerItem, want ...string) bool {
	for _, id := range item.IDs {
		for _, w := range want {
			if w != "" && id.ID == w {
				return true
			}
		}
	}
	return false
}

// localIdentity is what Ether knows about a local title, read once per
// resolution so verification can compare identity rather than title text.
type localIdentity struct {
	year int
}

func (s *Service) localIdentity(ctx context.Context, k core.RatingKey) localIdentity {
	var li localIdentity
	_ = s.db.QueryRowContext(ctx, `SELECT year FROM catalog_titles WHERE key=?`, int64(k)).Scan(&li.year)
	return li
}

// verifyTitle checks the canonical metadata record, not just a search hit, and
// returns the identity it established. A guid verifies only when the record
// carries the local title's TMDB id and a year that cannot be a namesake: the
// title and year must agree within the release-year tolerance. Without a
// title/year match the external ids alone are not proof for a movie or show.
func (s *Service) verifyTitle(ctx context.Context, k core.RatingKey, kind, guid string, li localIdentity) (identityRecord, bool, error) {
	items, err := s.getItems(ctx, "/library/metadata/"+guidHex(guid, kind), url.Values{"includeGuids": {"1"}})
	if errors.Is(err, core.ErrNotFound) {
		return identityRecord{}, false, nil // no canonical record: not verified
	}
	if err != nil {
		return identityRecord{}, false, err
	}
	if id := s.verifyItems(ctx, k, kind, guid, items, li); id.GUID != "" {
		return id, true, nil
	}
	return identityRecord{}, false, nil
}

// verifyItems is verifyTitle over a canonical record already fetched.
func (s *Service) verifyItems(ctx context.Context, k core.RatingKey, kind, guid string, items []providerItem, li localIdentity) identityRecord {
	tmdbID := fmt.Sprintf("tmdb://%d", k.TMDB())
	titleWanted := kind == "movie" || kind == "show"
	localTitle := ""
	if titleWanted {
		localTitle, _ = s.localTitle(ctx, k)
	}
	for _, item := range items {
		if item.GUID != guid || item.Type != kind {
			continue
		}
		if !hasExternalID(item, tmdbID) {
			continue
		}
		// TMDB and Plex years can differ by one (festival vs release); a
		// namesake from another year cannot. Where Ether has the local year,
		// it is the discriminator; the title must also agree.
		if li.year > 0 && item.Year > 0 && (item.Year < li.year-1 || item.Year > li.year+1) {
			continue
		}
		// When the canonical record and the local catalogue both name the
		// work, those titles must be the same work. Without both, the exact
		// TMDB id is the only identity available.
		if titleWanted && localTitle != "" && item.Title != "" && !titlesAgree(localTitle, item.Title) {
			continue
		}
		return identityRecord{GUID: guid, TMDB: k.TMDB(), Title: item.Title, Year: item.Year}
	}
	return identityRecord{}
}

func (s *Service) localTitle(ctx context.Context, k core.RatingKey) (string, bool) {
	var title string
	if err := s.db.QueryRowContext(ctx, `SELECT title FROM catalog_titles WHERE key=?`, int64(k)).Scan(&title); err != nil {
		return "", false
	}
	return title, true
}

// titlesAgree reports whether a canonical Plex title names the same work as
// the local title. It normalises case, whitespace and a trailing release year
// ("Runner (2026)") and requires equality: a different title that merely shares
// a leading word is a namesake, never the same work.
func titlesAgree(local, canonical string) bool {
	return normalizeTitle(local) == normalizeTitle(canonical)
}

// normalizeTitle folds a title for identity comparison: case, surrounding and
// repeated whitespace, and trailing release years ("Runner (2026)").
func normalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = trailingYear.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

var trailingYear = regexp.MustCompile(`\s*\(?(19|20)\d{2}\)?$`)

func (s *Service) resolveMovieOrShow(ctx context.Context, k core.RatingKey, kind, typ string) (string, error) {
	external := fmt.Sprintf("tmdb://%d", k.TMDB())
	items, err := s.getItems(ctx, "/library/metadata/matches", url.Values{"type": {typ}, "guid": {external}, "includeGuids": {"1"}})
	if err != nil {
		return "", err
	}
	li := s.localIdentity(ctx, k)
	var found identityRecord
	foundGUID := ""
	for _, item := range items {
		if item.Type != kind || !validKindGUID(item.GUID, kind) {
			continue
		}
		if !hasExternalID(item, external) {
			continue
		}
		id, ok, err := s.verifyTitle(ctx, k, kind, item.GUID, li)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		if foundGUID != "" && foundGUID != item.GUID {
			return "", fmt.Errorf("ambiguous Plex identity")
		}
		found, foundGUID = id, item.GUID
	}
	now := time.Now().Unix()
	err = s.storeMapping(ctx, k, found, now)
	return foundGUID, err
}

func (s *Service) resolveSeason(ctx context.Context, k core.RatingKey) (string, error) {
	showID := k.TMDB()
	seasonNum := k.Season()
	showKey := core.ShowKey(showID)
	// Resolve only through a verified show and its actual children. Match
	// results alone do not establish which show owns a season.
	showGUID, err := s.Resolve(ctx, showKey)
	if err != nil {
		return "", err
	}
	showHex := guidHex(showGUID, "show")
	if showHex == "" {
		_ = s.storeMapping(ctx, k, identityRecord{}, time.Now().Unix())
		return "", nil
	}
	children, err := s.getChildren(ctx, showHex)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	found := ""
	seen := make(map[int]string)
	for _, item := range children {
		if item.Type != "season" || !validKindGUID(item.GUID, "season") || item.Index == nil || *item.Index < 0 || *item.Index >= 9000 {
			continue
		}
		if prior := seen[*item.Index]; prior != "" && prior != item.GUID {
			return "", fmt.Errorf("ambiguous Plex season")
		}
		seen[*item.Index] = item.GUID
	}
	for n, guid := range seen {
		sk := core.SeasonKey(showID, n)
		if err := s.storeMappingIdentity(ctx, sk, identityRecord{GUID: guid, TMDB: showID}, now, showKey, showGUID); err != nil {
			return "", err
		}
		if n == seasonNum {
			found = guid
		}
	}
	if found == "" {
		if err := s.storeMappingIdentity(ctx, k, identityRecord{}, now, showKey, showGUID); err != nil {
			return "", err
		}
	}
	return found, nil
}

func (s *Service) resolveEpisode(ctx context.Context, k core.RatingKey) (string, error) {
	showID := k.TMDB()
	seasonNum := k.Season()
	epNum := k.Episode()
	seasonKey := core.SeasonKey(showID, seasonNum)
	// A verified season is obtained through its verified show; fetch its
	// children rather than trusting an independent episode search hit.
	seasonGUID, err := s.Resolve(ctx, seasonKey)
	if err != nil {
		return "", err
	}
	seasonHex := guidHex(seasonGUID, "season")
	if seasonHex == "" {
		_ = s.storeMapping(ctx, k, identityRecord{}, time.Now().Unix())
		return "", nil
	}
	children, err := s.getChildren(ctx, seasonHex)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	found := ""
	seen := make(map[int]string)
	for _, item := range children {
		if item.Type != "episode" || !validKindGUID(item.GUID, "episode") || item.Index == nil || *item.Index <= 0 || *item.Index >= 10000 || (item.ParentIndex != nil && *item.ParentIndex != seasonNum) {
			continue
		}
		if prior := seen[*item.Index]; prior != "" && prior != item.GUID {
			return "", fmt.Errorf("ambiguous Plex episode")
		}
		seen[*item.Index] = item.GUID
	}
	for n, guid := range seen {
		ek := core.EpisodeKey(showID, seasonNum, n)
		if err := s.storeMappingIdentity(ctx, ek, identityRecord{GUID: guid, TMDB: showID}, now, seasonKey, seasonGUID); err != nil {
			return "", err
		}
		if n == epNum {
			found = guid
		}
	}
	if found == "" {
		if err := s.storeMappingIdentity(ctx, k, identityRecord{}, now, seasonKey, seasonGUID); err != nil {
			return "", err
		}
	}
	return found, nil
}
