package prerolls

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/store"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Default storage caps.
const (
	DefaultMaxFileBytes  int64 = 500 << 20
	DefaultMaxTotalBytes int64 = 5 << 30
)

// Activity is the part of watch state the binge rule reads: the viewer's
// most recent finished play and most recent stopped (resumable) play. Both
// are single indexed rows.
type Activity interface {
	History(ctx context.Context, userID int64, since time.Time, limit int) ([]core.HistoryEntry, error)
	InProgress(ctx context.Context, userID int64, limit int) ([]core.WatchStatus, error)
}

// Options are the service dependencies.
type Options struct {
	DB *sql.DB
	// Dir holds converted clips and thumbnails (<data>/prerolls).
	Dir string
	// MaxFileBytes caps one source file; MaxTotalBytes caps the folder.
	MaxFileBytes, MaxTotalBytes int64
	// Converter probes and converts clips; nil leaves new clips processing
	// until FFmpeg is available.
	Converter Converter
	// Activity feeds the binge rule; nil disables it.
	Activity Activity
	// Title loads one title (an episode's show for its genres, rating and
	// collections; a simulated feature). Nil skips the show lookup.
	Title func(ctx context.Context, key core.RatingKey) (*core.Title, error)
	// InCollection reports whether key is a member of owner collection id.
	// Nil makes collection rules never match.
	InCollection func(ctx context.Context, id string, key core.RatingKey) bool
	// Viewer loads a viewer for the simulator.
	Viewer func(ctx context.Context, id int64) (*core.User, error)
	Logger *slog.Logger
	Now    func() time.Time
	// Rand returns a number in [0,n); nil uses math/rand.
	Rand func(n int) int
}

// Service implements the owner API and core.Prerolls.
type Service struct {
	db       *sql.DB
	dir      string
	maxFile  int64
	maxTotal int64
	conv     Converter
	activity Activity
	title    func(context.Context, core.RatingKey) (*core.Title, error)
	inColl   func(context.Context, string, core.RatingKey) bool
	viewer   func(context.Context, int64) (*core.User, error)
	log      *slog.Logger
	now      func() time.Time
	rand     func(int) int

	jobs chan job

	mu        sync.Mutex
	cache     []Preroll
	cacheSel  string
	cacheAt   time.Time
	cacheGen  uint64
	started   map[string]time.Time
	pending   map[string]pendingPlay
	lastPrune time.Time
}

type pendingPlay struct {
	feature core.RatingKey
	title   string
	at      time.Time
}

var _ core.Prerolls = (*Service)(nil)

// New returns the service. It creates Dir.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Dir == "" {
		return nil, errors.New("prerolls: DB and Dir are required")
	}
	if err := os.MkdirAll(o.Dir, 0o750); err != nil {
		return nil, err
	}
	s := &Service{
		db: o.DB, dir: o.Dir, maxFile: o.MaxFileBytes, maxTotal: o.MaxTotalBytes, conv: o.Converter,
		activity: o.Activity, title: o.Title, inColl: o.InCollection, viewer: o.Viewer,
		log: o.Logger, now: o.Now, rand: o.Rand, jobs: make(chan job, 64),
		started: map[string]time.Time{}, pending: map[string]pendingPlay{},
	}
	if s.maxFile <= 0 {
		s.maxFile = DefaultMaxFileBytes
	}
	if s.maxTotal <= 0 {
		s.maxTotal = DefaultMaxTotalBytes
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.rand == nil {
		s.rand = rand.IntN
	}
	return s, nil
}

// cacheTTL bounds how stale another cluster node's edits can look.
const cacheTTL = 10 * time.Second

func (s *Service) invalidate() {
	s.mu.Lock()
	s.cache, s.cacheAt = nil, time.Time{}
	s.cacheGen++
	s.mu.Unlock()
}

// cached returns every pre-roll, re-read at most every cacheTTL.
func (s *Service) cached(ctx context.Context) ([]Preroll, error) {
	all, _, err := s.cachedWithSelection(ctx)
	return all, err
}

// cachedWithSelection is cached plus the selection mode.
func (s *Service) cachedWithSelection(ctx context.Context) ([]Preroll, string, error) {
	s.mu.Lock()
	if s.cache != nil && s.now().Sub(s.cacheAt) < cacheTTL {
		out, sel := s.cache, s.cacheSel
		s.mu.Unlock()
		return out, sel, nil
	}
	gen := s.cacheGen
	s.mu.Unlock()
	all, err := s.loadAll(ctx)
	if err != nil {
		return nil, "", err
	}
	if all == nil {
		all = []Preroll{}
	}
	sel := s.Selection(ctx)
	s.mu.Lock()
	if gen == s.cacheGen {
		s.cache, s.cacheSel, s.cacheAt = all, sel, s.now()
	}
	s.mu.Unlock()
	return all, sel, nil
}

// List returns every pre-roll with its 30-day play count and the selection
// mode.
func (s *Service) List(ctx context.Context) ([]Preroll, string, error) {
	all, err := s.loadAll(ctx)
	if err != nil {
		return nil, "", err
	}
	counts, err := s.plays30d(ctx)
	if err != nil {
		return nil, "", err
	}
	if all == nil {
		all = []Preroll{}
	}
	for i := range all {
		all[i].Plays30d = counts[all[i].seq]
	}
	return all, s.Selection(ctx), nil
}

// Get returns one pre-roll.
func (s *Service) Get(ctx context.Context, id string) (Preroll, error) {
	p, err := s.byID(ctx, id)
	if err != nil {
		return Preroll{}, err
	}
	counts, err := s.plays30d(ctx)
	if err == nil {
		p.Plays30d = counts[p.seq]
	}
	return p, nil
}

// Selection is "priority" (default) or "shuffle".
func (s *Service) Selection(ctx context.Context) string {
	if s.setting(ctx, "selection") == SelectShuffle {
		return SelectShuffle
	}
	return SelectPriority
}

// SetSelection stores the selection mode.
func (s *Service) SetSelection(ctx context.Context, mode string) error {
	if mode != SelectPriority && mode != SelectShuffle {
		return invalid("selection", "use priority or shuffle")
	}
	if err := s.putSetting(ctx, "selection", mode); err != nil {
		return err
	}
	s.invalidate()
	return nil
}

// Create stores a new pre-roll (without a video yet).
func (s *Service) Create(ctx context.Context, in Input) (Preroll, error) {
	if err := normalize(&in); err != nil {
		return Preroll{}, err
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM prerolls`).Scan(&n); err != nil {
		return Preroll{}, err
	}
	if n >= maxPrerolls {
		return Preroll{}, invalid("name", "Ether keeps at most %d pre-rolls", maxPrerolls)
	}
	rules, _ := json.Marshal(in.Rules)
	freq, _ := json.Marshal(in.Frequency)
	enabled := 1
	if in.Enabled != nil && !*in.Enabled {
		enabled = 0
	}
	id := newPublicID()
	now := s.now().UnixNano()
	var seq int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO prerolls(id,name,enabled,rules_json,frequency_json,priority,play_order,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?) RETURNING seq`, id, in.Name, enabled, string(rules), string(freq), in.Priority, in.Order, now, now).Scan(&seq); err != nil {
		return Preroll{}, err
	}
	if seq > core.MaxPrerollID {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM prerolls WHERE seq=?`, seq)
		return Preroll{}, errors.New("prerolls: id space exhausted")
	}
	s.invalidate()
	return s.Get(ctx, id)
}

// Update replaces a pre-roll's settings. An omitted enabled keeps the
// current value.
func (s *Service) Update(ctx context.Context, id string, in Input) (Preroll, error) {
	cur, err := s.byID(ctx, id)
	if err != nil {
		return Preroll{}, err
	}
	if err := normalize(&in); err != nil {
		return Preroll{}, err
	}
	enabled := cur.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	rules, _ := json.Marshal(in.Rules)
	freq, _ := json.Marshal(in.Frequency)
	if _, err := s.db.ExecContext(ctx, `UPDATE prerolls SET name=?,enabled=?,rules_json=?,frequency_json=?,priority=?,play_order=?,updated_at=? WHERE seq=?`,
		in.Name, store.BoolInt(enabled), string(rules), string(freq), in.Priority, in.Order, s.now().UnixNano(), cur.seq); err != nil {
		return Preroll{}, err
	}
	s.invalidate()
	return s.Get(ctx, id)
}

// Delete removes a pre-roll, its files and its plays.
func (s *Service) Delete(ctx context.Context, id string) error {
	cur, err := s.byID(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM prerolls WHERE seq=?`, cur.seq); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM prerolls_plays WHERE preroll_seq=?`, cur.seq); err != nil {
		return err
	}
	s.invalidate()
	s.removeFiles(cur.seq)
	return nil
}

// PlayRow is one entry of a pre-roll's play log.
type PlayRow struct {
	ViewerID   int64     `json:"viewerId"`
	ViewerName string    `json:"viewerName"`
	Title      string    `json:"title"`
	RatingKey  string    `json:"ratingKey"`
	At         time.Time `json:"at"`
	Client     string    `json:"client"`
}

// Plays returns a pre-roll's most recent plays (limit 1..500).
func (s *Service) Plays(ctx context.Context, id string, limit int) ([]PlayRow, error) {
	cur, err := s.byID(ctx, id)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT viewer_id,viewer_name,rating_key,title,client,at FROM prerolls_plays
		WHERE preroll_seq=? ORDER BY at DESC LIMIT ?`, cur.seq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlayRow{}
	for rows.Next() {
		var r PlayRow
		var key, at int64
		if err := rows.Scan(&r.ViewerID, &r.ViewerName, &key, &r.Title, &r.Client, &at); err != nil {
			return nil, err
		}
		if key != 0 {
			r.RatingKey = strconv.FormatInt(key, 10)
		}
		r.At = time.Unix(0, at).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// retention is how long the play log keeps rows: the longest frequency
// window, at least the 30-day statistics window, at most MaxRetention.
func retention(all []Preroll) time.Duration {
	keep := statsWindow
	for _, p := range all {
		if w := p.Frequency.window(); w > keep {
			keep = w
		}
	}
	return min(keep, MaxRetention)
}

// Prune deletes plays older than the retention window.
func (s *Service) Prune(ctx context.Context) error {
	all, err := s.cached(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM prerolls_plays WHERE at<?`, s.now().Add(-retention(all)).UnixNano())
	return err
}

// Run converts queued clips and prunes the play log until ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.resume(ctx)
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	if err := s.Prune(ctx); err != nil && ctx.Err() == nil {
		s.log.Warn("prune pre-roll plays", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.jobs:
			s.process(ctx, j)
		case <-tick.C:
			if err := s.Prune(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("prune pre-roll plays", "err", err)
			}
		}
	}
}

func (s *Service) path(seq int64, ext string) string {
	return filepath.Join(s.dir, strconv.FormatInt(seq, 10)+ext)
}

func (s *Service) removeFiles(seq int64) {
	for _, ext := range []string{".mp4", ".jpg", ".part.mp4", ".part.jpg"} {
		_ = os.Remove(s.path(seq, ext))
	}
	uploads, _ := filepath.Glob(filepath.Join(s.dir, strconv.FormatInt(seq, 10)+".*.upload"))
	for _, u := range uploads {
		_ = os.Remove(u)
	}
}
