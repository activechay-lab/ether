package watchlist

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
	"github.com/go-chi/chi/v5"
)

func init() {
	store.Register("watchlist", 6, `CREATE TABLE watchlist_availability_jobs(user_id INTEGER PRIMARY KEY,state TEXT NOT NULL,done INTEGER NOT NULL DEFAULT 0,total INTEGER NOT NULL DEFAULT 0,touched INTEGER NOT NULL);`)
}

type availabilityJob struct {
	State     string    `json:"state"`
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	Enabled   bool      `json:"enabled"`
	NextCheck time.Time `json:"nextCheck"`
}

func (s *Service) availabilityStatus(ctx context.Context, uid int64) (availabilityJob, error) {
	j := availabilityJob{State: "idle"}
	cfg, err := s.settings(ctx)
	if err != nil {
		return j, err
	}
	j.Enabled = cfg.Enabled && s.o.Resolver != nil
	// The UI needs the persisted schedule even when ResumeSchedule is false.
	var ms int64
	_ = s.db.QueryRowContext(ctx, `SELECT CAST(v AS INTEGER) FROM watchlist_meta WHERE k='last_pass'`).Scan(&ms)
	j.NextCheck = time.UnixMilli(ms).Add(s.o.SyncInterval)
	if j.NextCheck.Before(s.o.Now()) {
		j.NextCheck = s.o.Now()
	}
	_, err = s.db.ExecContext(ctx, `UPDATE watchlist_availability_jobs SET state='interrupted' WHERE user_id=? AND state='running' AND touched<?`, uid, s.o.Now().Add(-5*time.Minute).Unix())
	if err != nil {
		return j, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT state,done,total FROM watchlist_availability_jobs WHERE user_id=?`, uid).Scan(&j.State, &j.Done, &j.Total)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return j, err
}

func (s *Service) availabilityRoutes(r chi.Router) {
	r.Get("/check", func(w http.ResponseWriter, r *http.Request) {
		j, err := s.availabilityStatus(r.Context(), viewer(r).ID)
		if err != nil {
			http.Error(w, "Availability check incomplete", 500)
			return
		}
		replyJSON(w, j)
	})
	r.Post("/check", func(w http.ResponseWriter, r *http.Request) {
		u := viewer(r)
		j, err := s.availabilityStatus(r.Context(), u.ID)
		if err != nil {
			http.Error(w, "Availability check incomplete", 500)
			return
		}
		if !j.Enabled {
			http.Error(w, "Watchlist monitoring is off", 409)
			return
		}
		res, err := s.db.ExecContext(r.Context(), `INSERT INTO watchlist_availability_jobs(user_id,state,touched) VALUES(?,'running',?) ON CONFLICT(user_id) DO UPDATE SET state='running',done=0,total=0,touched=excluded.touched WHERE state!='running'`, u.ID, s.o.Now().Unix())
		if err != nil {
			http.Error(w, "Availability check incomplete", 500)
			return
		}
		if n, _ := res.RowsAffected(); n == 1 {
			go s.runAvailabilityJob(u.ID)
		}
		j, _ = s.availabilityStatus(r.Context(), u.ID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		replyJSON(w, j)
	})
}

func (s *Service) runAvailabilityJob(uid int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	// Keep the lease alive even while a scheduled pass owns the resolver mutex.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.db.ExecContext(ctx, `UPDATE watchlist_availability_jobs SET touched=? WHERE user_id=? AND state='running'`, s.o.Now().Unix(), uid)
			}
		}
	}()
	state := "failed"
	defer func() {
		finish, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = s.db.ExecContext(finish, `UPDATE watchlist_availability_jobs SET state=?,touched=? WHERE user_id=?`, state, s.o.Now().Unix(), uid)
	}()
	u, err := s.user(ctx, uid)
	if err != nil {
		return
	}
	items, err := s.Items(ctx, u)
	if err != nil {
		return
	}
	orderChecks(items)
	_, err = s.db.ExecContext(ctx, `UPDATE watchlist_availability_jobs SET total=? WHERE user_id=?`, len(items), uid)
	if err != nil {
		return
	}
	incomplete := false
	for i, it := range items {
		if ctx.Err() != nil {
			return
		}
		// Do not hold the scheduled-pass mutex while waiting for another title.
		s.mu.Lock()
		cfg, e := s.settings(ctx)
		if e == nil && cfg.Enabled && s.o.Resolver != nil {
			cfg.CheckHours = 0 // explicit owner check bypasses the per-title freshness interval
			it.CheckedAt = time.Time{}
			job, stop := context.WithTimeout(ctx, time.Minute)
			e = s.checkSet(job, cfg, uid, u, u, []Item{it})
			stop()
		} else if e == nil {
			e = errors.New("monitoring disabled")
		}
		s.mu.Unlock()
		if e != nil {
			incomplete = true
		}
		_, err = s.db.ExecContext(ctx, `UPDATE watchlist_availability_jobs SET done=?,touched=? WHERE user_id=?`, i+1, s.o.Now().Unix(), uid)
		if err != nil {
			return
		}
	}
	if !incomplete {
		state = "completed"
	}
}
