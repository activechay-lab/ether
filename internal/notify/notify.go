// Package notify provides durable, rule-filtered owner notifications. Channel
// secrets never appear in API responses, delivery logs, or transport errors.
package notify

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/faillog"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/usenet"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

func init() {
	store.Register("notify", 1, `
CREATE TABLE notify_channels(id TEXT PRIMARY KEY,config TEXT NOT NULL);
CREATE TABLE notify_queue(id INTEGER PRIMARY KEY AUTOINCREMENT,channel TEXT NOT NULL,event TEXT NOT NULL,created INTEGER NOT NULL,due INTEGER NOT NULL,status TEXT NOT NULL DEFAULT 'pending',attempted INTEGER NOT NULL DEFAULT 0);
CREATE INDEX notify_due ON notify_queue(status,due);
`)
	// Claim columns let several nodes drain one queue: a row is delivered
	// by the node whose conditional update moved it out of 'pending'.
	store.Register("notify", 3, `ALTER TABLE notify_queue ADD COLUMN error TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_queue ADD COLUMN immediate INTEGER NOT NULL DEFAULT 0;
CREATE TABLE notify_settings(id TEXT PRIMARY KEY,config TEXT NOT NULL);
CREATE TABLE notify_push_keys(id TEXT PRIMARY KEY,private_key TEXT NOT NULL,public_key TEXT NOT NULL);`)
	store.Register("notify", 2, `
ALTER TABLE notify_queue ADD COLUMN claimed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_queue ADD COLUMN claimed_at INTEGER NOT NULL DEFAULT 0;
`)
}

const Redacted = "[configured]"

// Kinds lists every event kind Ether accepts. Definitions live in events.go.
var Kinds = eventKindIDs()

// Channel is one notification destination. Kind-specific values live in
// Settings (plain) and Secrets (sealed at rest, redacted by the owner API),
// keyed by the field names of the kind's schema (kinds.go).
type Channel struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	Enabled       bool              `json:"enabled"`
	Events        []string          `json:"events"`
	Settings      map[string]string `json:"settings,omitempty"`
	Secrets       map[string]string `json:"secrets,omitempty"`
	PerMinute     int               `json:"perMinute"`
	DigestMinutes int               `json:"digestMinutes"`
}

// Setting returns a non-secret field value, or "".
func (c Channel) Setting(key string) string { return c.Settings[key] }

// Secret returns a secret field value, or "".
func (c Channel) Secret(key string) string { return c.Secrets[key] }

// Subscription decodes the browser push subscription held in Secrets.
func (c Channel) Subscription() (*PushSubscription, error) {
	raw := c.Secret("subscription")
	if raw == "" {
		return nil, errors.New("browser subscription required; connect this device from Notifications")
	}
	var p PushSubscription
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, errors.New("stored browser subscription is unreadable")
	}
	return &p, nil
}

// SetSubscription stores p as the channel's subscription secret.
func (c *Channel) SetSubscription(p *PushSubscription) {
	if c.Secrets == nil {
		c.Secrets = map[string]string{}
	}
	if p == nil {
		delete(c.Secrets, "subscription")
		return
	}
	raw, _ := json.Marshal(p)
	c.Secrets["subscription"] = string(raw)
}

// clone deep-copies the channel's value maps.
func (c Channel) clone() Channel {
	if c.Settings != nil {
		m := make(map[string]string, len(c.Settings))
		for k, v := range c.Settings {
			m[k] = v
		}
		c.Settings = m
	}
	if c.Secrets != nil {
		m := make(map[string]string, len(c.Secrets))
		for k, v := range c.Secrets {
			m[k] = v
		}
		c.Secrets = m
	}
	if c.Events != nil {
		c.Events = append([]string(nil), c.Events...)
	}
	return c
}

type Event struct {
	HTML    string    `json:"html,omitempty"`
	Kind    string    `json:"kind"`
	Title   string    `json:"title"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}
type Options struct {
	// NodeID names this node on the queue rows it claims; a random id is
	// used when empty.
	NodeID     string
	Providers  func() []usenet.ProviderStats
	CacheUsage func() (int64, int64)
	Client     *http.Client
	Now        func() time.Time
	Logger     *slog.Logger
	// Sealer protects stored channel secrets and the push identity
	// (secrets.go); nil stores them as given.
	Sealer config.FieldSealer
	// Kinds is the channel-kind registry; nil uses the built-ins.
	Kinds *KindRegistry
}
type Service struct {
	subscriberMu            sync.RWMutex
	subscribers             map[uint64]func(Event)
	nextSubscriber          uint64
	monitorDone             <-chan struct{}
	watchMu                 sync.Mutex
	watchUpdate             func() error
	pushPrivate, pushPublic string

	drainMu        chan struct{}
	healthMu       sync.Mutex
	sampleMu       sync.Mutex // serializes provider/cache sampling independently of indexer callbacks
	providerStates map[string]string
	indexerAlerts  map[string]*indexerAlert // indexer+kind -> notification state
	indexerLog     *faillog.Tracker
	cacheFull      bool
	db             *sql.DB
	opts           Options
	kinds          *KindRegistry
}

// Kinds returns the service's channel-kind registry.
func (s *Service) Kinds() *KindRegistry { return s.kinds }

// RegisterChannelKind adds a channel kind (plugin kinds; WS9f).
func (s *Service) RegisterChannelKind(k ChannelKind) error { return s.kinds.Register(k) }

// Subscribe observes successfully emitted events, independent of channel selection.
func (s *Service) Subscribe(fn func(Event)) func() {
	s.subscriberMu.Lock()
	if s.subscribers == nil {
		s.subscribers = make(map[uint64]func(Event))
	}
	s.nextSubscriber++
	id := s.nextSubscriber
	s.subscribers[id] = fn
	s.subscriberMu.Unlock()
	return func() { s.subscriberMu.Lock(); delete(s.subscribers, id); s.subscriberMu.Unlock() }
}

func (s *Service) publishEvent(e Event) {
	s.subscriberMu.RLock()
	fns := make([]func(Event), 0, len(s.subscribers))
	for _, fn := range s.subscribers {
		fns = append(fns, fn)
	}
	s.subscriberMu.RUnlock()
	for _, fn := range fns {
		fn(e)
	}
}

// HTTPClient is the client channel kinds deliver with.
func (s *Service) HTTPClient() *http.Client { return s.opts.Client }

func New(db *sql.DB, opts Options) *Service {
	if opts.Client == nil {
		opts.Client = egress.Client(egress.CapDirect, 15*time.Second)
		opts.Client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.NodeID == "" {
		var b [8]byte
		_, _ = rand.Read(b[:])
		opts.NodeID = hex.EncodeToString(b[:])
	}
	kinds := opts.Kinds
	if kinds == nil {
		kinds = builtinRegistry()
	}
	return &Service{drainMu: make(chan struct{}, 1), db: db, opts: opts, kinds: kinds, indexerLog: faillog.New(opts.Logger, "enqueue indexer notification")}
}

// claimTimeout is how long a claimed row may stay in 'sending' before it is
// presumed abandoned by a node that stopped mid-delivery.
const claimTimeout = 10 * time.Minute

func (s *Service) channels(ctx context.Context) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT config FROM notify_channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		c, _, err := s.decodeChannel(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func redacted(c Channel) Channel {
	c = c.clone()
	for k, v := range c.Secrets {
		if v != "" {
			c.Secrets[k] = Redacted
		}
	}
	return c
}

// normalize moves each value into the map its kind's schema declares
// (Settings or Secrets), applies field defaults and drops empty values.
func (s *Service) normalize(c Channel) (Channel, error) {
	kind, ok := s.kinds.Get(c.Kind)
	if !ok {
		return c, errors.New("unsupported channel kind")
	}
	settings := map[string]string{}
	secrets := map[string]string{}
	known := map[string]bool{}
	for _, f := range kind.Fields {
		known[f.Key] = true
		v := c.Settings[f.Key]
		if sv, ok := c.Secrets[f.Key]; ok && sv != "" {
			v = sv
		}
		if v == "" {
			v = f.Default
		}
		if v == "" {
			continue
		}
		if f.Secret {
			secrets[f.Key] = v
		} else {
			settings[f.Key] = v
		}
	}
	for k := range c.Settings {
		if !known[k] {
			return c, fmt.Errorf("unknown setting %q for %s channels", k, c.Kind)
		}
	}
	for k := range c.Secrets {
		if !known[k] {
			return c, fmt.Errorf("unknown setting %q for %s channels", k, c.Kind)
		}
	}
	if len(settings) == 0 {
		settings = nil
	}
	if len(secrets) == 0 {
		secrets = nil
	}
	c.Settings, c.Secrets = settings, secrets
	return c, nil
}

func validateWith(reg *KindRegistry, c Channel) error {
	kind, ok := reg.Get(c.Kind)
	if !ok {
		return errors.New("unsupported channel kind")
	}
	if err := kind.Validate(c); err != nil {
		return err
	}
	for _, v := range c.Events {
		if v != "*" && !known(v) {
			return errors.New("unsupported event rule")
		}
	}
	return nil
}

func (s *Service) deliver(ctx context.Context, c Channel, e Event) error {
	kind, ok := s.kinds.Get(c.Kind)
	if !ok {
		return errors.New("unsupported channel kind")
	}
	return kind.Deliver(ctx, s, c, e)
}
func (s *Service) Channels(ctx context.Context) ([]Channel, error) {
	cs, err := s.channels(ctx)
	for i := range cs {
		cs[i] = redacted(cs[i])
	}
	return cs, err
}
func (s *Service) Save(ctx context.Context, c Channel) error {
	if c.ID == "" || len(c.ID) > 100 || strings.ContainsAny(c.ID, "/\\") {
		return errors.New("channel ID required (no slashes)")
	}
	c, err := s.normalize(c)
	if err != nil {
		return err
	}
	cs, err := s.channels(ctx)
	if err != nil {
		return err
	}
	for _, old := range cs {
		if old.ID == c.ID {
			if err := keepChannelSecrets(&c, old); err != nil {
				return err
			}
		}
	}
	if c.PerMinute == 0 {
		c.PerMinute = 5
	}
	if c.PerMinute < 1 || c.PerMinute > 60 || c.DigestMinutes < 0 || c.DigestMinutes > 10080 {
		return errors.New("rate must be 1–60/minute; digest must be 0–10080 minutes")
	}
	if err := validateWith(s.kinds, c); err != nil {
		return err
	}
	raw, err := s.encodeChannel(c)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO notify_channels(id,config) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET config=excluded.config`, c.ID, raw)
	if err == nil {
		s.refreshMonitor()
	}
	return err
}
func (s *Service) Emit(ctx context.Context, e Event) error {
	e, selected, err := s.prepare(ctx, e)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		s.publishEvent(e)
		return nil
	}
	// Commit every destination together. A failed fan-out can be retried
	// without duplicating notifications already queued for earlier channels.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range selected {
		if err := enqueueWith(ctx, tx, c, e, s.opts.Now()); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.publishEvent(e)
	return nil
}

// prepare validates e, stamps its time and selects the channels it goes to.
func (s *Service) prepare(ctx context.Context, e Event) (Event, []Channel, error) {
	if !known(e.Kind) {
		return e, nil, errors.New("unknown event kind")
	}
	if len(e.Title) > 500 || len(e.Message) > 8000 || len(e.HTML) > 256000 {
		return e, nil, errors.New("event too large")
	}
	if e.At.IsZero() {
		e.At = s.opts.Now()
	}
	cs, err := s.channels(ctx)
	if err != nil {
		return e, nil, err
	}
	var selected []Channel
	for _, c := range cs {
		if c.Enabled && matches(c.Events, e.Kind) && eventAllowsKind(e.Kind, c.Kind) {
			selected = append(selected, c)
		}
	}
	if len(selected) == 0 && e.Kind == "newsletter.weekly" {
		return e, nil, errors.New("no enabled SMTP channel selected for weekly newsletters")
	}
	return e, selected, nil
}
func known(k string) bool {
	for _, v := range Kinds {
		if k == v {
			return true
		}
	}
	return false
}
func matches(events []string, k string) bool {
	for _, e := range events {
		if e == k || e == "*" {
			return true
		}
	}
	return false
}

func enqueueWith(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, c Channel, e Event, now time.Time) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO notify_queue(channel,event,created,due,immediate) VALUES(?,?,?,?,?)`, c.ID, string(raw), now.Unix(), deliveryDue(c, e, now).Unix(), immediateEvent(e))
	return err
}

// ObservePlayback only enqueues; network delivery runs separately in Run.
// The playback bus also carries console-refresh kinds (update, progress,
// surface) that are not notification events; those are skipped silently.
func (s *Service) ObservePlayback(ev playback.Event) {
	kind := ev.Kind
	if kind == "downfailover" {
		kind = "failover"
	}
	if !known("playback." + kind) {
		return
	}
	e := Event{Kind: "playback." + kind, Title: "Ether playback " + ev.Kind, At: ev.At}
	if ev.Session != nil {
		e.Message = ev.Session.Username + ": " + ev.Session.TitleName
	}
	if ev.Error != "" {
		e.Message = ev.Error
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Emit(ctx, e); err != nil {
		s.opts.Logger.Warn("enqueue notification", "err", err)
	}
}

// TitleAvailable lets the discovery/watchlist flow publish availability.
func (s *Service) TitleAvailable(ctx context.Context, title string) error {
	return s.Emit(ctx, Event{Kind: "title.available", Title: "Title available", Message: title})
}

// Run is the single queue consumer. Failed sends are recorded without automatic
// retry, avoiding duplicate delivery when an upstream timeout is ambiguous.
func (s *Service) Run(ctx context.Context) {
	healthTicker := time.NewTicker(30 * time.Second)
	defer healthTicker.Stop()
	_ = s.SampleHealth(ctx)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-healthTicker.C:
			_ = s.SampleHealth(ctx)
		case <-ticker.C:
			if err := s.Drain(ctx); err != nil && ctx.Err() == nil {
				s.opts.Logger.Warn("notification queue unavailable")
			}
		}
	}
}
func (s *Service) Drain(ctx context.Context) error {
	select {
	case s.drainMu <- struct{}{}:
		defer func() { <-s.drainMu }()
	case <-ctx.Done():
		return ctx.Err()
	}
	cs, err := s.channels(ctx)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if !c.Enabled {
			continue
		}
		now := s.opts.Now().Unix()
		var recent int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notify_queue WHERE channel=? AND attempted>?`, c.ID, now-60).Scan(&recent); err != nil {
			return err
		}
		if recent >= c.PerMinute {
			continue
		}
		var immediate int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notify_queue WHERE channel=? AND status='pending' AND immediate=1 AND due<=?`, c.ID, now).Scan(&immediate); err != nil {
			return err
		}
		limit := 1
		if c.DigestMinutes > 0 && immediate == 0 {
			limit = 100
		}
		query := `SELECT id,event FROM notify_queue WHERE channel=? AND status='pending' AND due<=? ORDER BY id LIMIT ?`
		args := []any{c.ID, now, limit}
		if immediate > 0 {
			query = `SELECT id,event FROM notify_queue WHERE channel=? AND status='pending' AND immediate=1 AND due<=? ORDER BY id LIMIT ?`
		} else if c.DigestMinutes > 0 {
			// A digest window opens with its first event: once that event
			// is due, everything queued behind it goes in the same message.
			// Selecting only due rows would send each event on its own as
			// it individually came due, so nothing was ever batched.
			var due int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notify_queue WHERE channel=? AND status='pending' AND immediate=0 AND due<=?`, c.ID, now).Scan(&due); err != nil {
				return err
			}
			if due == 0 {
				continue
			}
			query = `SELECT id,event FROM notify_queue WHERE channel=? AND status='pending' AND immediate=0 ORDER BY id LIMIT ?`
			args = []any{c.ID, limit}
		}
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		candidates := []int64{}
		pending := []Event{}
		for rows.Next() {
			var id int64
			var raw string
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			var e Event
			if json.Unmarshal([]byte(raw), &e) == nil {
				candidates = append(candidates, id)
				pending = append(pending, e)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		// Claim each row with a conditional update; a row another node
		// claimed first is skipped, so every event is delivered by one node.
		ids := []int64{}
		events := []Event{}
		for i, id := range candidates {
			res, err := s.db.ExecContext(ctx, `UPDATE notify_queue SET status='sending',attempted=?,claimed_by=?,claimed_at=? WHERE id=? AND status='pending'`,
				now, s.opts.NodeID, now, id)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n == 1 {
				ids = append(ids, id)
				events = append(events, pending[i])
			}
		}
		if len(ids) == 0 {
			continue
		}
		e := events[0]
		if len(events) > 1 {
			e.HTML = ""
			e.Title = fmt.Sprintf("Ether digest: %d events", len(events))
			parts := []string{}
			for _, v := range events {
				parts = append(parts, v.Title+": "+v.Message)
			}
			e.Message = strings.Join(parts, "\n")
		}
		status, detail := "sent", ""
		if err := s.deliver(ctx, c, e); err != nil {
			status, detail = "failed", err.Error()
		}
		for _, id := range ids {
			if _, err := s.db.ExecContext(ctx, `UPDATE notify_queue SET status=?,error=? WHERE id=? AND claimed_by=?`, status, detail, id, s.opts.NodeID); err != nil {
				return err
			}
		}
	}
	// A claim whose node stopped mid-delivery is recorded as failed rather
	// than resent: the event may already have been delivered.
	nowTime := s.opts.Now()
	staleClaimCutoff := nowTime.Add(-claimTimeout).Unix()
	var hasStaleSending bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notify_queue WHERE status='sending' AND claimed_at<?)`, staleClaimCutoff).Scan(&hasStaleSending); err != nil {
		return err
	}
	if hasStaleSending {
		if _, err := s.db.ExecContext(ctx, `UPDATE notify_queue SET status='failed',error='Delivery interrupted; receipt unknown' WHERE status='sending' AND claimed_at<?`, staleClaimCutoff); err != nil {
			return err
		}
	}
	retentionCutoff := nowTime.AddDate(0, 0, -30).Unix()
	var hasExpired bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notify_queue WHERE status IN ('sent','failed','cancelled') AND created<?)`, retentionCutoff).Scan(&hasExpired); err != nil {
		return err
	}
	if hasExpired {
		_, err = s.db.ExecContext(ctx, `DELETE FROM notify_queue WHERE status IN ('sent','failed','cancelled') AND created<?`, retentionCutoff)
		return err
	}
	return nil
}

// Critical lifecycle events bypass digest delays. Crash delivery itself is handled
// by the independent monitor, without relying on this queue or database.
func deliveryDue(c Channel, e Event, now time.Time) time.Time {
	if immediateEvent(e) == 1 {
		return now
	}
	return now.Add(time.Duration(c.DigestMinutes) * time.Minute)
}

func immediateEvent(e Event) int {
	if strings.HasPrefix(e.Kind, "server.") || e.Kind == "test" || e.Kind == "newsletter.weekly" {
		return 1
	}
	return 0
}
