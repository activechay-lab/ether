package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// A stored batch is the recovery unit. If saving its cursor fails, at most
// that batch is replayed; no completed slice or pass is deliberately restarted.
// The fingerprint invalidates continuation when discovery settings change.
type syncResume struct {
	Fingerprint string          `json:"fingerprint"`
	Passes      map[string]bool `json:"passes"`
	// Walks are the /discover walks in progress (sync_walk.go).
	Walks map[string]walkCursor `json:"walks,omitempty"`
}
type syncResumeKey struct{}

func (c *Catalog) resumeSync(ctx context.Context) (context.Context, error) {
	cfg, err := json.Marshal(struct {
		Config any
		Years  [2]int
	}{c.config(), c.syncYears})
	if err != nil {
		return ctx, err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(cfg))
	r := &syncResume{}
	var raw string
	if err := c.db.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k='sync_resume'`).Scan(&raw); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ctx, err
	}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), r); err != nil {
			return ctx, fmt.Errorf("catalog: read sync cursor: %w", err)
		}
	}
	if r.Fingerprint != fingerprint || r.Passes == nil {
		r = &syncResume{Fingerprint: fingerprint, Passes: map[string]bool{}}
	}
	if r.Walks == nil {
		r.Walks = map[string]walkCursor{}
	}
	return context.WithValue(ctx, syncResumeKey{}, r), nil
}
func (c *Catalog) saveSyncResume(ctx context.Context) error {
	r, ok := ctx.Value(syncResumeKey{}).(*syncResume)
	if !ok {
		return nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := c.setMeta(ctx, "sync_resume", string(b)); err != nil {
		return fmt.Errorf("catalog: save sync cursor: %w", err)
	}
	return nil
}
func syncRetryDelay(failures int) time.Duration {
	delay := time.Minute
	for i := 1; i < failures && delay < time.Hour; i++ {
		delay = min(2*delay, time.Hour)
	}
	return delay
}
