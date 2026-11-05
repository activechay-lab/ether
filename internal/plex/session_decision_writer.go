package plex

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

type decisionArmWrite struct {
	user            int64
	client, session string
	key             core.RatingKey
	expires         int64
	done            chan struct{}
}

// Decisions acknowledge a background batch before returning, so an immediate
// start on another node observes the arm. Rendering and starts only read it.
func (s *Server) persistDecisionArm(pr *Request, key core.RatingKey) {
	if s.d.DB == nil {
		return
	}
	s.decisionWriterOnce.Do(func() {
		s.decisionWriter = make(chan decisionArmWrite, 64)
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.writeDecisionArms() }()
	})
	cmd := decisionArmWrite{user: pr.User.ID, client: pr.ClientID, session: pr.SessionID, key: key, expires: s.now().Add(3 * time.Minute).UnixMilli(), done: make(chan struct{})}
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

func (s *Server) writeDecisionArms() {
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
			ctx, cancel := context.WithTimeout(s.ctx, dbTimeout)
			tx, err := s.d.DB.BeginTx(ctx, nil)
			if err == nil {
				for _, cmd := range batch {
					_, err = tx.ExecContext(ctx, `INSERT INTO plex_decision_arms(user_id,client_id,session_id,title_key,expires) VALUES(?,?,?,?,?) ON CONFLICT(user_id,client_id,session_id,title_key) DO UPDATE SET expires=excluded.expires`, cmd.user, cmd.client, cmd.session, cmd.key, cmd.expires)
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
func (s *Server) refreshDecisionArms(now time.Time) {
	if s.d.DB == nil {
		return
	}
	s.termMu.Lock()
	rows := make([]decisionArmWrite, 0, len(s.decisionArms))
	for _, arm := range s.decisionArms {
		if now.Sub(arm.at) > 3*time.Minute {
			continue
		}
		for key := range arm.items {
			rows = append(rows, decisionArmWrite{user: arm.user, client: arm.client, session: arm.session, key: key, expires: arm.at.Add(3 * time.Minute).UnixMilli()})
		}
	}
	s.termMu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, dbTimeout)
	defer cancel()
	tx, err := s.d.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	for _, row := range rows {
		if _, err = tx.ExecContext(ctx, `INSERT INTO plex_decision_arms(user_id,client_id,session_id,title_key,expires) VALUES(?,?,?,?,?) ON CONFLICT(user_id,client_id,session_id,title_key) DO UPDATE SET expires=excluded.expires`, row.user, row.client, row.session, row.key, row.expires); err != nil {
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
