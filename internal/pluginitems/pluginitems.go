// Package pluginitems owns the storage, keyset browse, full-text search,
// sync engine, Title projection, matching and resolution cache for plugin
// content kinds (spec §2.2-2.5, §3.1-3.6, §4.4, §6.3, §7).
//
// Plugin items never enter catalog_titles; every query is scoped by
// library_id first, and item identity is the namespaced core.ItemRef
// ("pk:<pluginID>:<kindKey>:<externalID>") paired with a stored,
// never-reused RatingKey sequence number (core.PluginKey).
package pluginitems

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pluginitems", 1, `
CREATE TABLE plugin_kinds (
	plugin_id      TEXT    NOT NULL,
	kind_key       TEXT    NOT NULL,
	library_id     INTEGER NOT NULL,
	decl           TEXT    NOT NULL,
	decl_hash      TEXT    NOT NULL,
	schema_version INTEGER NOT NULL DEFAULT 1,
	PRIMARY KEY (plugin_id, kind_key)
);
CREATE INDEX plugin_kinds_library ON plugin_kinds(library_id);

CREATE TABLE plugin_items_seq (
	id   INTEGER PRIMARY KEY,
	next INTEGER NOT NULL
);
INSERT INTO plugin_items_seq(id, next) VALUES(1, 1);

CREATE TABLE plugin_items (
	item_ref     TEXT    PRIMARY KEY,
	seq          INTEGER NOT NULL UNIQUE,
	plugin_id    TEXT    NOT NULL,
	kind_key     TEXT    NOT NULL,
	library_id   INTEGER NOT NULL,
	external_id  TEXT    NOT NULL,
	parent_ref   TEXT    NOT NULL DEFAULT '',
	level        TEXT    NOT NULL,
	idx          INTEGER NOT NULL DEFAULT 0,
	title        TEXT    NOT NULL,
	sort_title   TEXT    NOT NULL,
	summary      TEXT    NOT NULL DEFAULT '',
	year         INTEGER NOT NULL DEFAULT 0,
	released     INTEGER NOT NULL DEFAULT 0,
	rating       REAL    NOT NULL DEFAULT 0,
	playable     INTEGER NOT NULL DEFAULT 0,
	child_count  INTEGER NOT NULL DEFAULT 0,
	leaf_count   INTEGER NOT NULL DEFAULT 0,
	hints        TEXT    NOT NULL DEFAULT '{}',
	fields       TEXT    NOT NULL DEFAULT '{}',
	etag         TEXT    NOT NULL DEFAULT '',
	added_at     INTEGER NOT NULL,
	updated_at   INTEGER NOT NULL,
	seen_at      INTEGER NOT NULL,
	missed_walks INTEGER NOT NULL DEFAULT 0,
	UNIQUE (plugin_id, kind_key, external_id)
);
CREATE INDEX plugin_items_browse       ON plugin_items(library_id, parent_ref, sort_title, item_ref);
CREATE INDEX plugin_items_level_sort   ON plugin_items(library_id, level, sort_title, item_ref);
CREATE INDEX plugin_items_parent       ON plugin_items(parent_ref, idx, sort_title, item_ref);
CREATE INDEX plugin_items_added        ON plugin_items(library_id, added_at DESC, item_ref);
CREATE INDEX plugin_items_released     ON plugin_items(library_id, released DESC, item_ref);
CREATE INDEX plugin_items_year         ON plugin_items(library_id, year DESC, item_ref);
CREATE INDEX plugin_items_rating       ON plugin_items(library_id, rating DESC, item_ref);
CREATE INDEX plugin_items_idx          ON plugin_items(library_id, idx ASC, item_ref);
CREATE INDEX plugin_items_search_title ON plugin_items(library_id, sort_title);

CREATE TABLE plugin_item_fields (
	item_ref TEXT    NOT NULL,
	field    TEXT    NOT NULL,
	v_text   TEXT    NOT NULL DEFAULT '',
	v_int    INTEGER NOT NULL DEFAULT 0,
	v_real   REAL    NOT NULL DEFAULT 0,
	PRIMARY KEY (item_ref, field, v_text, v_int)
);
CREATE INDEX plugin_item_fields_lookup     ON plugin_item_fields(field, v_text, item_ref);
CREATE INDEX plugin_item_fields_lookup_int ON plugin_item_fields(field, v_int, item_ref);

CREATE TABLE plugin_item_art (
	item_ref TEXT    NOT NULL,
	role     TEXT    NOT NULL,
	url      TEXT    NOT NULL,
	width    INTEGER NOT NULL DEFAULT 0,
	height   INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (item_ref, role)
);

CREATE TABLE plugin_item_tally (
	library_id INTEGER NOT NULL,
	level      TEXT    NOT NULL,
	n          INTEGER NOT NULL,
	PRIMARY KEY (library_id, level)
);

CREATE TABLE plugin_sync_state (
	plugin_id    TEXT    NOT NULL,
	kind_key     TEXT    NOT NULL,
	cursor       TEXT    NOT NULL DEFAULT '',
	generation   INTEGER NOT NULL DEFAULT 0,
	last_started INTEGER NOT NULL DEFAULT 0,
	last_ok      INTEGER NOT NULL DEFAULT 0,
	last_error   TEXT    NOT NULL DEFAULT '',
	items        INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (plugin_id, kind_key)
);

CREATE TABLE plugin_resolutions (
	item_ref   TEXT    PRIMARY KEY,
	pattern    TEXT    NOT NULL DEFAULT '',
	source     TEXT    NOT NULL DEFAULT '{}',
	negative   INTEGER NOT NULL DEFAULT 0,
	expires_at INTEGER NOT NULL
);
CREATE INDEX plugin_resolutions_expires ON plugin_resolutions(expires_at);

CREATE TABLE plugin_matches (
	item_ref      TEXT    NOT NULL,
	candidate_ref TEXT    NOT NULL,
	score         REAL    NOT NULL,
	reasons       TEXT    NOT NULL DEFAULT '[]',
	source        TEXT    NOT NULL DEFAULT 'auto',
	chosen        INTEGER NOT NULL DEFAULT 0,
	updated_at    INTEGER NOT NULL,
	PRIMARY KEY (item_ref, candidate_ref)
);
CREATE INDEX plugin_matches_chosen ON plugin_matches(item_ref) WHERE chosen = 1;

CREATE VIRTUAL TABLE plugin_item_fts USING fts5(item_ref UNINDEXED, library_id UNINDEXED, text);
`)

	store.RegisterDialect("pluginitems", 1, store.Postgres, `
CREATE TABLE plugin_kinds (
	plugin_id      TEXT   NOT NULL,
	kind_key       TEXT   NOT NULL,
	library_id     BIGINT NOT NULL,
	decl           TEXT   NOT NULL,
	decl_hash      TEXT   NOT NULL,
	schema_version BIGINT NOT NULL DEFAULT 1,
	PRIMARY KEY (plugin_id, kind_key)
);
CREATE INDEX plugin_kinds_library ON plugin_kinds(library_id);

CREATE TABLE plugin_items_seq (
	id   BIGINT PRIMARY KEY,
	next BIGINT NOT NULL
);
INSERT INTO plugin_items_seq(id, next) VALUES(1, 1);

CREATE TABLE plugin_items (
	item_ref     TEXT COLLATE "C" PRIMARY KEY,
	seq          BIGINT           NOT NULL UNIQUE,
	plugin_id    TEXT             NOT NULL,
	kind_key     TEXT             NOT NULL,
	library_id   BIGINT           NOT NULL,
	external_id  TEXT COLLATE "C" NOT NULL,
	parent_ref   TEXT COLLATE "C" NOT NULL DEFAULT '',
	level        TEXT COLLATE "C" NOT NULL,
	idx          BIGINT           NOT NULL DEFAULT 0,
	title        TEXT             NOT NULL,
	sort_title   TEXT COLLATE "C" NOT NULL,
	summary      TEXT             NOT NULL DEFAULT '',
	year         BIGINT           NOT NULL DEFAULT 0,
	released     BIGINT           NOT NULL DEFAULT 0,
	rating       DOUBLE PRECISION NOT NULL DEFAULT 0,
	playable     INTEGER          NOT NULL DEFAULT 0,
	child_count  BIGINT           NOT NULL DEFAULT 0,
	leaf_count   BIGINT           NOT NULL DEFAULT 0,
	hints        TEXT             NOT NULL DEFAULT '{}',
	fields       TEXT             NOT NULL DEFAULT '{}',
	etag         TEXT             NOT NULL DEFAULT '',
	added_at     BIGINT           NOT NULL,
	updated_at   BIGINT           NOT NULL,
	seen_at      BIGINT           NOT NULL,
	missed_walks INTEGER          NOT NULL DEFAULT 0,
	search_tsv   TSVECTOR         NOT NULL DEFAULT ''::tsvector,
	UNIQUE (plugin_id, kind_key, external_id)
);
CREATE INDEX plugin_items_browse       ON plugin_items(library_id, parent_ref, sort_title, item_ref);
CREATE INDEX plugin_items_level_sort   ON plugin_items(library_id, level, sort_title, item_ref);
CREATE INDEX plugin_items_parent       ON plugin_items(parent_ref, idx, sort_title, item_ref);
CREATE INDEX plugin_items_added        ON plugin_items(library_id, added_at DESC, item_ref);
CREATE INDEX plugin_items_released     ON plugin_items(library_id, released DESC, item_ref);
CREATE INDEX plugin_items_year         ON plugin_items(library_id, year DESC, item_ref);
CREATE INDEX plugin_items_rating       ON plugin_items(library_id, rating DESC, item_ref);
CREATE INDEX plugin_items_idx          ON plugin_items(library_id, idx ASC, item_ref);
CREATE INDEX plugin_items_search_title ON plugin_items(library_id, sort_title);
CREATE INDEX plugin_items_search_tsv   ON plugin_items USING gin (search_tsv);

CREATE TABLE plugin_item_fields (
	item_ref TEXT COLLATE "C" NOT NULL,
	field    TEXT             NOT NULL,
	v_text   TEXT             NOT NULL DEFAULT '',
	v_int    BIGINT           NOT NULL DEFAULT 0,
	v_real   DOUBLE PRECISION NOT NULL DEFAULT 0,
	PRIMARY KEY (item_ref, field, v_text, v_int)
);
CREATE INDEX plugin_item_fields_lookup     ON plugin_item_fields(field, v_text, item_ref);
CREATE INDEX plugin_item_fields_lookup_int ON plugin_item_fields(field, v_int, item_ref);

CREATE TABLE plugin_item_art (
	item_ref TEXT COLLATE "C" NOT NULL,
	role     TEXT             NOT NULL,
	url      TEXT             NOT NULL,
	width    BIGINT           NOT NULL DEFAULT 0,
	height   BIGINT           NOT NULL DEFAULT 0,
	PRIMARY KEY (item_ref, role)
);

CREATE TABLE plugin_item_tally (
	library_id BIGINT NOT NULL,
	level      TEXT   NOT NULL,
	n          BIGINT NOT NULL,
	PRIMARY KEY (library_id, level)
);

CREATE TABLE plugin_sync_state (
	plugin_id    TEXT   NOT NULL,
	kind_key     TEXT   NOT NULL,
	cursor       TEXT   NOT NULL DEFAULT '',
	generation   BIGINT NOT NULL DEFAULT 0,
	last_started BIGINT NOT NULL DEFAULT 0,
	last_ok      BIGINT NOT NULL DEFAULT 0,
	last_error   TEXT   NOT NULL DEFAULT '',
	items        BIGINT NOT NULL DEFAULT 0,
	PRIMARY KEY (plugin_id, kind_key)
);

CREATE TABLE plugin_resolutions (
	item_ref   TEXT COLLATE "C" PRIMARY KEY,
	pattern    TEXT             NOT NULL DEFAULT '',
	source     TEXT             NOT NULL DEFAULT '{}',
	negative   INTEGER          NOT NULL DEFAULT 0,
	expires_at BIGINT           NOT NULL
);
CREATE INDEX plugin_resolutions_expires ON plugin_resolutions(expires_at);

CREATE TABLE plugin_matches (
	item_ref      TEXT COLLATE "C" NOT NULL,
	candidate_ref TEXT             NOT NULL,
	score         DOUBLE PRECISION NOT NULL,
	reasons       TEXT             NOT NULL DEFAULT '[]',
	source        TEXT             NOT NULL DEFAULT 'auto',
	chosen        INTEGER          NOT NULL DEFAULT 0,
	updated_at    BIGINT           NOT NULL,
	PRIMARY KEY (item_ref, candidate_ref)
);
CREATE INDEX plugin_matches_chosen ON plugin_matches(item_ref) WHERE chosen = 1;
`)
}

// Caller invokes one plugin route (satisfied by *plugins.Manager).
type Caller interface {
	Call(ctx context.Context, pluginID string, req plugins.Request) (plugins.Response, error)
}

// HealthReporter records plugin health issues discovered by sync or item
// validation (satisfied by *plugins.Manager via RecordFailure, or custom hooks).
type HealthReporter interface {
	RecordFailure(ctx context.Context, pluginID, reason string)
}

// Options configures the pluginitems Service.
type Options struct {
	Logger         *slog.Logger
	Now            func() time.Time
	Caller         Caller
	Health         HealthReporter
	IsLeader       func() bool
	MaxPluginItems int
	// OnAttention is an optional hook notified when a sync guard or
	// contract error flags a plugin for owner attention (e.g. 50%-delete
	// guard, unknown fields dropped, auth required).
	OnAttention func(ctx context.Context, pluginID, reason string)
}

// Service manages plugin kinds, items, browse, search, sync, projections,
// matches and resolutions.
type Service struct {
	db             *sql.DB
	log            *slog.Logger
	now            func() time.Time
	caller         Caller
	health         HealthReporter
	isLeader       func() bool
	onAttention    func(ctx context.Context, pluginID, reason string)
	maxPluginItems int

	syncGen atomic.Uint64

	locksMu   sync.Mutex
	kindLocks map[string]*sync.Mutex

	workersMu sync.Mutex
	workers   map[string]*syncWorker
}

// New opens the pluginitems Service over a migrated database.
func New(ctx context.Context, db *sql.DB, o Options) (*Service, error) {
	if db == nil {
		return nil, errors.New("pluginitems: db required")
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.MaxPluginItems <= 0 {
		o.MaxPluginItems = DefaultMaxPluginItems
	}
	s := &Service{
		db:             db,
		log:            o.Logger.With("component", "pluginitems"),
		now:            o.Now,
		caller:         o.Caller,
		health:         o.Health,
		isLeader:       o.IsLeader,
		onAttention:    o.OnAttention,
		maxPluginItems: o.MaxPluginItems,
		kindLocks:      map[string]*sync.Mutex{},
		workers:        map[string]*syncWorker{},
	}
	// Seed the sequence row if a test opened the table without data, and
	// load the highest stored sync generation so PageFreshness never rewinds.
	if _, err := db.ExecContext(ctx, `INSERT INTO plugin_items_seq(id, next) VALUES(1, 1) ON CONFLICT(id) DO NOTHING`); err != nil {
		return nil, fmt.Errorf("pluginitems: seed seq: %w", err)
	}
	var maxGen int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation), 0) FROM plugin_sync_state`).Scan(&maxGen); err != nil {
		return nil, fmt.Errorf("pluginitems: load sync generation: %w", err)
	}
	if maxGen > 0 {
		s.syncGen.Store(uint64(maxGen))
	}
	return s, nil
}

// SetCaller sets or replaces the plugin route caller after construction.
func (s *Service) SetCaller(c Caller) {
	s.workersMu.Lock()
	s.caller = c
	s.workersMu.Unlock()
}

// SetHealth sets or replaces the health reporter after construction.
func (s *Service) SetHealth(h HealthReporter) {
	s.workersMu.Lock()
	s.health = h
	s.workersMu.Unlock()
}

// Generation returns the monotonic sync generation across all plugin kinds
// (spec §3.5, §6.4: feeds frontend.PageFreshness.KindSync).
func (s *Service) Generation() uint64 {
	if s == nil {
		return 0
	}
	return s.syncGen.Load()
}

// SyncGeneration is an alias for Generation (feeds PageFreshness.KindSync).
func (s *Service) SyncGeneration() uint64 {
	return s.Generation()
}

// KindBackends returns a plugins.KindBackends struct with KindUpsert,
// KindDelete and KindItems wired to this store (spec §3.6, §12 slice C).
func (s *Service) KindBackends() plugins.KindBackends {
	return plugins.KindBackends{
		KindUpsert: s.KindUpsert,
		KindDelete: s.KindDelete,
		KindItems:  s.KindItems,
	}
}

func (s *Service) kindLock(pluginID, kindKey string) *sync.Mutex {
	key := pluginID + "\x00" + kindKey
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	mu, ok := s.kindLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		s.kindLocks[key] = mu
	}
	return mu
}

func (s *Service) noteAttention(ctx context.Context, pluginID, reason string) {
	s.workersMu.Lock()
	att := s.onAttention
	hlth := s.health
	s.workersMu.Unlock()
	if att != nil {
		att(ctx, pluginID, reason)
	}
	if hlth != nil {
		hlth.RecordFailure(ctx, pluginID, reason)
	}
}

func (s *Service) leader() bool {
	if s.isLeader == nil {
		return true
	}
	return s.isLeader()
}
