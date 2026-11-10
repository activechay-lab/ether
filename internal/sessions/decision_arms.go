package sessions

import (
	"context"
	"database/sql"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
	"log/slog"
	"sync"
	"time"
)

const decisionDBTimeout = 5 * time.Second

// DecisionArms owns per-player decision authorization and durable publication.
// The host cancels ctx and joins wg before clearing its durable rows on shutdown.
type DecisionArms struct {
	ctx                context.Context
	db                 *sql.DB
	now                func() time.Time
	node               string
	log                *slog.Logger
	wg                 *sync.WaitGroup
	mu                 sync.Mutex
	decisionArms       map[string]decisionArm
	decisionWriterOnce sync.Once
	decisionWriter     chan decisionArmWrite
}

func NewDecisionArms(ctx context.Context, db *sql.DB, now func() time.Time, node string, log *slog.Logger, wg *sync.WaitGroup) *DecisionArms {
	return &DecisionArms{ctx: ctx, db: db, now: now, node: node, log: log, wg: wg}
}

func (s *DecisionArms) Expire(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, arm := range s.decisionArms {
		if now.Sub(arm.at) > 3*time.Minute {
			delete(s.decisionArms, id)
		}
	}
}

func init() {
	store.Register("plex_decision_arms", 1, `CREATE TABLE plex_decision_arms(user_id BIGINT NOT NULL,client_id TEXT NOT NULL,session_id TEXT NOT NULL,title_key BIGINT NOT NULL,expires BIGINT NOT NULL,PRIMARY KEY(user_id,client_id,session_id,title_key)); CREATE INDEX plex_decision_arms_expiry ON plex_decision_arms(expires);`)
	store.Register("plex_decision_arms", 2, `ALTER TABLE plex_decision_arms ADD COLUMN node_id TEXT NOT NULL DEFAULT ''; CREATE INDEX plex_decision_arms_node ON plex_decision_arms(node_id);`)
	store.Register("plex_decision_arms", 3, `ALTER TABLE plex_decision_arms ADD COLUMN active INTEGER NOT NULL DEFAULT 1;`)
	store.RegisterViewerTable("plex_decision_arms", "plex_decision_arms", "user_id", store.ViewerMergeDrop)
}

type decisionArm struct {
	user            int64
	client, session string
	items           map[core.RatingKey]bool
	at              time.Time
}

// The public player session owns its decision state; the encoder's session=
// value is an independent delivery handle and cannot authorize another player.
func (s *DecisionArms) Arm(pr DecisionOwner, key core.RatingKey) {
	if pr.ID == "" || key == 0 {
		return
	}
	s.mu.Lock()
	if s.decisionArms == nil {
		s.decisionArms = make(map[string]decisionArm)
	}
	id := pr.ID
	a := s.decisionArms[id]
	if a.items == nil {
		a.items = make(map[core.RatingKey]bool)
	}
	a.user, a.client, a.session = pr.UserID, pr.ClientID, pr.SessionID
	a.items[key] = true
	a.at = s.now()
	s.decisionArms[id] = a
	s.mu.Unlock()
	s.persistDecisionArm(pr, key)
}
func (s *DecisionArms) Armed(pr DecisionOwner, key core.RatingKey) bool {
	if pr.ID == "" {
		return false
	}
	id := pr.ID
	s.mu.Lock()
	a, ok := s.decisionArms[id]
	if ok && s.now().Sub(a.at) <= 3*time.Minute && (key == 0 || a.items[key]) {
		a.at = s.now()
		s.decisionArms[id] = a
		s.mu.Unlock()
		if s.db == nil {
			return true
		}
	} else {
		s.mu.Unlock()
	}
	if s.db == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(s.ctx, decisionDBTimeout)
	defer cancel()
	query := `SELECT 1 FROM plex_decision_arms WHERE user_id=? AND client_id=? AND session_id=? AND expires>=?`
	args := []any{pr.UserID, pr.ClientID, pr.SessionID, s.now().UnixMilli()}
	if key != 0 {
		query += ` AND title_key=? AND active=1`
		args = append(args, key)
	}
	query += ` LIMIT 1`
	var found int
	return s.db.QueryRowContext(ctx, query, args...).Scan(&found) == nil
}

// Stopping one item clears its start/bandwidth authorization while retaining
// the session's history of having made a decision.
func (s *DecisionArms) Disarm(pr DecisionOwner, key core.RatingKey) {
	if pr.ID == "" {
		return
	}
	id := pr.ID
	s.mu.Lock()
	if a, ok := s.decisionArms[id]; ok {
		if _, seen := a.items[key]; seen {
			a.items[key] = false
			s.decisionArms[id] = a
		}
	}
	s.mu.Unlock()
	s.persistDecisionChange(pr, key, true)
}

type decisionArmWrite struct {
	disarm          bool
	user            int64
	client, session string
	key             core.RatingKey
	expires         int64
	done            chan struct{}
}

// Decisions acknowledge a background batch before returning, so an immediate
// start on another node observes the arm. Rendering and starts only read it.
func (s *DecisionArms) persistDecisionArm(pr DecisionOwner, key core.RatingKey) {
	s.persistDecisionChange(pr, key, false)
}
func (s *DecisionArms) persistDecisionChange(pr DecisionOwner, key core.RatingKey, disarm bool) {
	if s.db == nil {
		return
	}
	s.decisionWriterOnce.Do(func() {
		s.decisionWriter = make(chan decisionArmWrite, 64)
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.writeDecisionArms() }()
	})
	cmd := decisionArmWrite{disarm: disarm, user: pr.UserID, client: pr.ClientID, session: pr.SessionID, key: key, expires: s.now().Add(3 * time.Minute).UnixMilli(), done: make(chan struct{})}
	select {
	case s.decisionWriter <- cmd:
	case <-s.ctx.Done():
		return
	}
	select {
	case <-cmd.done:
	case <-s.ctx.Done():
	}
}

func (s *DecisionArms) writeDecisionArms() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case first := <-s.decisionWriter:
			batch := []decisionArmWrite{first}
		drain:
			for len(batch) < 64 {
				select {
				case cmd := <-s.decisionWriter:
					batch = append(batch, cmd)
				default:
					break drain
				}
			}
			ctx, cancel := context.WithTimeout(s.ctx, decisionDBTimeout)
			tx, err := s.db.BeginTx(ctx, nil)
			if err == nil {
				for _, cmd := range batch {
					if cmd.disarm {
						_, err = tx.ExecContext(ctx, `UPDATE plex_decision_arms SET active=0 WHERE user_id=? AND client_id=? AND session_id=? AND title_key=?`, cmd.user, cmd.client, cmd.session, cmd.key)
						if err != nil {
							break
						}
						continue
					}
					_, err = tx.ExecContext(ctx, `INSERT INTO plex_decision_arms(user_id,client_id,session_id,title_key,expires,node_id,active) VALUES(?,?,?,?,?,?,1) ON CONFLICT(user_id,client_id,session_id,title_key) DO UPDATE SET active=1,expires=CASE WHEN plex_decision_arms.expires>excluded.expires THEN plex_decision_arms.expires ELSE excluded.expires END,node_id=CASE WHEN plex_decision_arms.expires>excluded.expires THEN plex_decision_arms.node_id ELSE excluded.node_id END`, cmd.user, cmd.client, cmd.session, cmd.key, cmd.expires, s.node)
					if err != nil {
						break
					}
				}
				if err == nil {
					_, err = tx.ExecContext(ctx, `DELETE FROM plex_decision_arms WHERE expires<?`, s.now().UnixMilli())
				}
				if err == nil {
					err = tx.Commit()
				} else {
					_ = tx.Rollback()
				}
			}
			cancel()
			if err != nil {
				s.log.Warn("persist playback decision arms", "err", err)
			}
			for _, cmd := range batch {
				close(cmd.done)
			}
		}
	}
}

// The session janitor refreshes live arms and removes expired durable entries.
// Timeline and read handlers update only memory; SQLite writes stay here.
func (s *DecisionArms) Refresh(now time.Time) {
	if s.db == nil {
		return
	}
	s.mu.Lock()
	rows := make([]decisionArmWrite, 0, len(s.decisionArms))
	for _, arm := range s.decisionArms {
		if now.Sub(arm.at) > 3*time.Minute {
			continue
		}
		for key, active := range arm.items {
			rows = append(rows, decisionArmWrite{disarm: !active, user: arm.user, client: arm.client, session: arm.session, key: key, expires: arm.at.Add(3 * time.Minute).UnixMilli()})
		}
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, decisionDBTimeout)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	for _, row := range rows {
		active := 1
		if row.disarm {
			active = 0
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO plex_decision_arms(user_id,client_id,session_id,title_key,expires,node_id,active) VALUES(?,?,?,?,?,?,?) ON CONFLICT(user_id,client_id,session_id,title_key) DO UPDATE SET expires=CASE WHEN plex_decision_arms.expires>excluded.expires THEN plex_decision_arms.expires ELSE excluded.expires END,node_id=CASE WHEN plex_decision_arms.expires>excluded.expires THEN plex_decision_arms.node_id ELSE excluded.node_id END`, row.user, row.client, row.session, row.key, row.expires, s.node, active); err != nil {
			return
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM plex_decision_arms WHERE expires<?`, now.UnixMilli()); err != nil {
		return
	}
	if err = tx.Commit(); err != nil {
		s.log.Warn("refresh playback decision arms", "err", err)
	}
}

func (s *DecisionArms) ClearOwn() {
	if s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM plex_decision_arms WHERE node_id=?`, s.node); err != nil {
		s.log.Warn("clear playback decision arms", "err", err)
	}
}

type DecisionOwner = frontend.DecisionOwner
