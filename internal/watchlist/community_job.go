package watchlist

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/go-chi/chi/v5"
)

const (
	CommunityJobQueued      = "queued"
	CommunityJobRunning     = "running"
	CommunityJobCompleted   = "completed"
	CommunityJobFailed      = "failed"
	CommunityJobInterrupted = "interrupted"
)

type CommunityJob struct {
	ID         int64      `json:"id"`
	State      string     `json:"state"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Count      int        `json:"count"`
	Plex       string     `json:"plex"`
	Error      string     `json:"error,omitempty"`
}

func (s *Service) initCommunityJobs() error {
	// Only expire jobs past their hard deadline; another cluster node may be running them.
	_, err := s.db.Exec(`UPDATE watchlist_community_jobs SET state='interrupted',finished_at=?,error='Watchlist sync incomplete' WHERE state IN ('queued','running') AND created_at<?`, s.o.Now().Unix(), s.o.Now().Add(-6*time.Minute).Unix())
	return err
}

func (s *Service) CommunityJob(ctx context.Context, id int64) (CommunityJob, error) {
	if err := s.initCommunityJobs(); err != nil {
		return CommunityJob{}, err
	}
	var j CommunityJob
	var c, st, fin int64
	e := s.db.QueryRowContext(ctx, `SELECT id,state,created_at,COALESCE(started_at,0),COALESCE(finished_at,0),count,error,plex FROM watchlist_community_jobs WHERE id=?`, id).Scan(&j.ID, &j.State, &c, &st, &fin, &j.Count, &j.Error, &j.Plex)
	if e != nil {
		return j, e
	}
	j.CreatedAt = time.Unix(c, 0).UTC()
	if st > 0 {
		x := time.Unix(st, 0).UTC()
		j.StartedAt = &x
	}
	if fin > 0 {
		x := time.Unix(fin, 0).UTC()
		j.FinishedAt = &x
	}
	return j, nil
}

func (s *Service) startCommunityJob(ctx context.Context, u *core.User) (CommunityJob, bool, error) {
	if err := s.initCommunityJobs(); err != nil {
		return CommunityJob{}, false, err
	}
	now := s.o.Now().Unix()
	// Keep a bounded history per viewer, without a background cleanup loop.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM watchlist_community_jobs WHERE user_id=? AND state NOT IN ('queued','running') AND id NOT IN (SELECT id FROM watchlist_community_jobs WHERE user_id=? ORDER BY id DESC LIMIT 100)`, u.ID, u.ID); err != nil {
		return CommunityJob{}, false, err
	}
	var id int64
	e := s.db.QueryRowContext(ctx, `INSERT INTO watchlist_community_jobs(user_id,state,created_at) VALUES(?,'queued',?) ON CONFLICT(user_id) WHERE state IN ('queued','running') DO NOTHING RETURNING id`, u.ID, now).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		e = s.db.QueryRowContext(ctx, `SELECT id FROM watchlist_community_jobs WHERE user_id=? AND state IN ('queued','running') ORDER BY id DESC LIMIT 1`, u.ID).Scan(&id)
		if e != nil {
			return CommunityJob{}, false, e
		}
		j, e := s.CommunityJob(ctx, id)
		return j, false, e
	}
	if e != nil {
		return CommunityJob{}, false, e
	}
	j, e := s.CommunityJob(ctx, id)
	return j, true, e
}

func (s *Service) runCommunityJob(id int64, u *core.User) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := s.db.ExecContext(ctx, `UPDATE watchlist_community_jobs SET state='running',started_at=? WHERE id=? AND state='queued'`, s.o.Now().Unix(), id)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return
	}
	result := SyncResult{}
	latest, err := s.user(ctx, u.ID)
	if err == nil {
		err = s.syncViewer(ctx, latest, &result)
	}
	state, msg := CommunityJobCompleted, ""
	if err != nil {
		state, msg = CommunityJobFailed, "Watchlist sync incomplete"
	}
	finish, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	_, err = s.db.ExecContext(finish, `UPDATE watchlist_community_jobs SET state=?,finished_at=?,count=?,error=?,plex=? WHERE id=? AND state='running'`, state, s.o.Now().Unix(), result.Count, msg, result.Plex, id)
	if err != nil {
		s.o.Logger.Warn("watchlist job completion failed", "err", err)
	}
}

func (s *Service) withCommunityWriterLock(ctx context.Context, userID string, fn func() error) error {
	deadline := time.Now().Add(5 * time.Minute)
	var lease int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := s.o.Now().Unix()
		res, err := s.db.ExecContext(ctx, `INSERT INTO watchlist_community_locks(user_id,touched) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET touched=excluded.touched WHERE watchlist_community_locks.touched < ?`, userID, now, now-300)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			lease = now
			break
		}
		if time.Now().After(deadline) {
			return errors.New("Community sync writer lock timeout")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = s.db.ExecContext(releaseCtx, `DELETE FROM watchlist_community_locks WHERE user_id=? AND touched=?`, userID, lease)
	}()
	return fn()
}

func (s *Service) communityJobRoutes(r chi.Router) {
	r.Post("/sync/jobs", func(w http.ResponseWriter, r *http.Request) {
		u := viewer(r)
		j, fresh, e := s.startCommunityJob(r.Context(), u)
		if e != nil {
			http.Error(w, "Community sync unavailable", 500)
			return
		}
		if fresh {
			go s.runCommunityJob(j.ID, u)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		replyJSON(w, j)
	})
	r.Get("/sync/jobs/{jobID}", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.ParseInt(chi.URLParam(r, "jobID"), 10, 64)
		if e != nil {
			http.Error(w, "Invalid job", 400)
			return
		}
		u := viewer(r)
		if u == nil {
			http.NotFound(w, r)
			return
		}
		j, e := s.communityJobForUser(r.Context(), id, u.ID)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		replyJSON(w, j)
	})
}

func (s *Service) communityJobForUser(ctx context.Context, id, userID int64) (CommunityJob, error) {
	if err := s.initCommunityJobs(); err != nil {
		return CommunityJob{}, err
	}
	var j CommunityJob
	var c, st, fin int64
	err := s.db.QueryRowContext(ctx, `SELECT id,state,created_at,COALESCE(started_at,0),COALESCE(finished_at,0),count,error,plex FROM watchlist_community_jobs WHERE id=? AND user_id=?`, id, userID).Scan(&j.ID, &j.State, &c, &st, &fin, &j.Count, &j.Error, &j.Plex)
	if err != nil {
		return j, err
	}
	j.CreatedAt = time.Unix(c, 0).UTC()
	if st > 0 {
		x := time.Unix(st, 0).UTC()
		j.StartedAt = &x
	}
	if fin > 0 {
		x := time.Unix(fin, 0).UTC()
		j.FinishedAt = &x
	}
	return j, nil
}

func replyJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
