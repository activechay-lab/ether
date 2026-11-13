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
	Error      string     `json:"error,omitempty"`
}

func (s *Service) initCommunityJobs() error {
	var recoveryErr error
	s.communityJobRecovery.Do(func() {
		_, recoveryErr = s.db.Exec(`UPDATE watchlist_community_jobs SET state='interrupted',finished_at=?,error='Sync interrupted by restart' WHERE state IN ('queued','running')`, s.o.Now().Unix())
	})
	return recoveryErr
}

func (s *Service) CommunityJob(ctx context.Context, id int64) (CommunityJob, error) {
	if err := s.initCommunityJobs(); err != nil {
		return CommunityJob{}, err
	}
	var j CommunityJob
	var c, st, fin int64
	e := s.db.QueryRowContext(ctx, `SELECT id,state,created_at,COALESCE(started_at,0),COALESCE(finished_at,0),count,error FROM watchlist_community_jobs WHERE id=?`, id).Scan(&j.ID, &j.State, &c, &st, &fin, &j.Count, &j.Error)
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
	now := s.o.Now().Unix()
	result, err := s.db.ExecContext(ctx, `UPDATE watchlist_community_jobs SET state='running',started_at=? WHERE id=? AND state='queued'`, now, id)
	if err != nil {
		s.o.Logger.Error("community watchlist job could not start", "job_id", id, "error", err)
		return
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return
	}
	finish := func(state, msg string, count int) {
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer finishCancel()
		if _, err := s.db.ExecContext(finishCtx, `UPDATE watchlist_community_jobs SET state=?,finished_at=?,count=?,error=? WHERE id=?`, state, s.o.Now().Unix(), count, msg, id); err != nil {
			s.o.Logger.Error("community watchlist job could not finish", "job_id", id, "error", err)
		}
	}
	enabled, e := s.CommunitySyncEnabled(ctx, u.ID)
	if e != nil || !enabled || u.IsOwner || u.UUID == "" || !s.viewerCommunityEligible(ctx, u) {
		finish(CommunityJobFailed, "Community sync is not eligible", 0)
		return
	}
	var users []core.User
	if s.o.Users == nil {
		finish(CommunityJobFailed, "Community sync unavailable", 0)
		return
	}
	users, e = s.o.Users(ctx)
	owner := int64(0)
	if e == nil {
		for _, x := range users {
			if x.IsOwner {
				owner = x.ID
				break
			}
		}
	}
	token := ""
	if e == nil && owner > 0 && s.o.Tokens != nil {
		token, e = s.o.Tokens(ctx, owner)
	}
	if e != nil || token == "" {
		finish(CommunityJobFailed, "Community sync unavailable", 0)
		return
	}
	titles, e := s.communityWatchlist(ctx, token, u)
	if errors.Is(e, core.ErrUnauthorized) && s.o.Tokens != nil {
		if viewerToken, tokErr := s.o.Tokens(ctx, u.ID); tokErr == nil && viewerToken != "" {
			titles, e = s.plex(ctx, viewerToken)
		} else if u.Username == "" {
			titles, e = nil, nil
		}
	}
	if e != nil {
		s.o.Logger.Warn("community watchlist fetch failed", "job_id", id, "user_id", u.ID, "err", e)
		finish(CommunityJobFailed, "Community sync failed", 0)
		return
	}
	if !s.communityJobEligible(ctx, u) {
		finish(CommunityJobFailed, "Community sync is not eligible", 0)
		return
	}
	var stored int
	if e = s.withCommunityWriterLock(ctx, u.ID, func() error {
		var err error
		stored, err = s.replaceCommunitySnapshot(ctx, u, titles)
		return err
	}); e != nil {
		s.o.Logger.Warn("community watchlist commit failed", "job_id", id, "user_id", u.ID, "err", e)
		finish(CommunityJobFailed, "Community sync failed", 0)
		return
	}
	finish(CommunityJobCompleted, "", stored)
	_ = s.Check(ctx, u)
}

func (s *Service) viewerCommunityEligible(ctx context.Context, u *core.User) bool {
	if u == nil || u.IsOwner || u.UUID == "" || s.o.Users == nil || s.o.CommunityEligible == nil {
		return false
	}
	users, err := s.o.Users(ctx)
	if err != nil {
		return false
	}
	var latest *core.User
	for i := range users {
		if users[i].ID == u.ID {
			latest = &users[i]
			break
		}
	}
	return latest != nil && latest.UUID == u.UUID && !latest.IsOwner && s.o.CommunityEligible(latest)
}

func (s *Service) communityJobEligible(ctx context.Context, u *core.User) bool {
	enabled, err := s.CommunitySyncEnabled(ctx, u.ID)
	return err == nil && enabled && s.viewerCommunityEligible(ctx, u)
}

func (s *Service) withCommunityWriterLock(ctx context.Context, userID int64, fn func() error) error {
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

func (s *Service) enableAllCommunityUsers(ctx context.Context) {
	if s.o.Users == nil {
		return
	}
	users, err := s.o.Users(ctx)
	if err != nil {
		return
	}
	var eligible []core.User
	for _, u := range users {
		if !u.IsOwner && u.UUID != "" && (s.o.CommunityEligible == nil || s.o.CommunityEligible(&u)) {
			_ = s.SetCommunitySyncEnabled(ctx, u.ID, true)
			eligible = append(eligible, u)
		}
	}
	if len(eligible) == 0 {
		return
	}
	go func(targets []core.User) {
		for i := range targets {
			u := targets[i]
			jobCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			j, fresh, err := s.startCommunityJob(jobCtx, &u)
			cancel()
			if err == nil && fresh {
				s.runCommunityJob(j.ID, &u)
			}
		}
	}(eligible)
}

func (s *Service) replaceCommunitySnapshot(ctx context.Context, u *core.User, titles []core.Title) (int, error) {
	enabled, err := s.CommunitySyncEnabled(ctx, u.ID)
	if err != nil || !enabled || !s.viewerCommunityEligible(ctx, u) {
		return 0, errors.New("Community sync no longer eligible")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	keys := map[core.RatingKey]bool{}
	for _, title := range titles {
		if !title.Key.Valid() || (title.Type != core.TypeMovie && title.Type != core.TypeShow) {
			return 0, errors.New("invalid Community snapshot")
		}
		if !s.allowed(u, &title) {
			continue
		}
		keys[title.Key] = true
		raw, e := json.Marshal(title)
		if e != nil {
			return 0, e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO watchlist_items(user_id,item_key,source,title,added) VALUES(?,?,?,?,?) ON CONFLICT(user_id,item_key,source) DO UPDATE SET title=excluded.title`, u.ID, title.Key, communitySource, string(raw), s.o.Now().Unix()); e != nil {
			return 0, e
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT item_key FROM watchlist_items WHERE user_id=? AND source=?`, u.ID, communitySource)
	if err != nil {
		return 0, err
	}
	var removed []core.RatingKey
	for rows.Next() {
		var k core.RatingKey
		if err = rows.Scan(&k); err != nil {
			rows.Close()
			return 0, err
		}
		if !keys[k] {
			removed = append(removed, k)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, k := range removed {
		if _, err = tx.ExecContext(ctx, `DELETE FROM watchlist_items WHERE user_id=? AND source=? AND item_key=?`, u.ID, communitySource, k); err != nil {
			return 0, err
		}
		if err = forgetEpisodes(ctx, tx, u.ID, k); err != nil {
			return 0, err
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT enabled FROM watchlist_community_users WHERE user_id=?`, u.ID).Scan(&enabled); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, errors.New("Community sync no longer eligible")
		}
		cfg, cfgErr := s.settings(ctx)
		if cfgErr != nil || !cfg.Community {
			return 0, errors.New("Community sync no longer eligible")
		}
	} else if !enabled {
		return 0, errors.New("Community sync no longer eligible")
	}
	if !s.viewerCommunityEligible(ctx, u) {
		return 0, errors.New("Community sync no longer eligible")
	}
	if err = tx.Commit(); err == nil {
		s.gen.Add(1)
	}
	return len(keys), err
}

func (s *Service) communityJobRoutes(r chi.Router) {
	r.Post("/community/sync", func(w http.ResponseWriter, r *http.Request) {
		u := viewer(r)
		if u == nil || u.IsOwner || u.UUID == "" || !s.viewerCommunityEligible(r.Context(), u) {
			http.Error(w, "Community sync unavailable", 403)
			return
		}
		enabled, e := s.CommunitySyncEnabled(r.Context(), u.ID)
		if e != nil || !enabled {
			http.Error(w, "Community sync unavailable", 403)
			return
		}
		j, fresh, e := s.startCommunityJob(r.Context(), u)
		if e != nil {
			http.Error(w, "Community sync unavailable", 500)
			return
		}
		if fresh {
			go s.runCommunityJob(j.ID, u)
		}
		w.WriteHeader(http.StatusAccepted)
		replyJSON(w, j)
	})
	r.Get("/community/jobs/{jobID}", func(w http.ResponseWriter, r *http.Request) {
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
	err := s.db.QueryRowContext(ctx, `SELECT id,state,created_at,COALESCE(started_at,0),COALESCE(finished_at,0),count,error FROM watchlist_community_jobs WHERE id=? AND user_id=?`, id, userID).Scan(&j.ID, &j.State, &c, &st, &fin, &j.Count, &j.Error)
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
