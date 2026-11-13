package plugins

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Shared across managers: never more than two large-file readers in this process.
var folderHashSlots = make(chan struct{}, 2)

type folderHashWork struct {
	mu                 sync.Mutex
	plugin, root, path string
	status             FolderHashJob
	cancel             context.CancelFunc
}

func (h *host) FoldersHashStart(ctx context.Context, root, path string) (FolderHashJob, error) {
	if _, err := h.FoldersStat(ctx, root, path); err != nil {
		return FolderHashJob{}, err
	}
	m := h.m
	m.hashMu.Lock()
	defer m.hashMu.Unlock()
	if m.hashJobs == nil {
		m.hashJobs = make(map[string]*folderHashWork)
	}
	for id, w := range m.hashJobs {
		if w.plugin != h.plugin {
			continue
		}
		w.mu.Lock()
		running := w.status.State == "running"
		status := w.status
		w.mu.Unlock()
		if running {
			if w.root == root && w.path == path {
				return status, nil
			}
			return FolderHashJob{}, fmt.Errorf("folders.hash: plugin already has a running job")
		}
		delete(m.hashJobs, id)
	}
	select {
	case folderHashSlots <- struct{}{}:
	default:
		return FolderHashJob{}, fmt.Errorf("folders.hash: global job limit reached; retry later")
	}
	// The job intentionally survives the initiating host call, but has a finite lifetime.
	jobCtx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	m.hashSequence++
	w := &folderHashWork{plugin: h.plugin, root: root, path: path, cancel: cancel, status: FolderHashJob{ID: fmt.Sprintf("hash-%d", m.hashSequence), State: "running"}}
	m.hashJobs[w.status.ID] = w
	go func() {
		defer func() { cancel(); <-folderHashSlots }()
		result, err := h.hashFile(jobCtx, root, path, true, func(n int64) { w.mu.Lock(); w.status.Bytes = n; w.mu.Unlock() })
		w.mu.Lock()
		defer w.mu.Unlock()
		if err != nil {
			w.status.State = "error"
			w.status.Error = err.Error()
			if jobCtx.Err() != nil {
				w.status.State = "canceled"
			}
		} else {
			w.status.State = "done"
			w.status.Bytes = result.Size
			w.status.Result = &result
		}
	}()
	return FolderHashJob{ID: w.status.ID, State: "running"}, nil
}

func (h *host) hashJob(ctx context.Context, id string, cancel bool) (FolderHashJob, error) {
	h.m.hashMu.Lock()
	w := h.m.hashJobs[id]
	h.m.hashMu.Unlock()
	if w == nil || w.plugin != h.plugin {
		return FolderHashJob{}, fmt.Errorf("folders.hash: unknown job")
	}
	if _, err := h.folderCheck(ctx, w.root, w.path); err != nil {
		w.cancel()
		return FolderHashJob{}, err
	}
	if cancel {
		w.cancel()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	status := w.status
	if status.Result != nil {
		result := *status.Result
		status.Result = &result
	}
	return status, nil
}

func (h *host) FoldersHashPoll(ctx context.Context, id string) (FolderHashJob, error) {
	return h.hashJob(ctx, id, false)
}
func (h *host) FoldersHashCancel(ctx context.Context, id string) (FolderHashJob, error) {
	return h.hashJob(ctx, id, true)
}

func (s *storage) hashCacheGet(ctx context.Context, plugin, key string) ([]byte, bool, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM plugins_hash_cache WHERE plugin=? AND kkey=?`, plugin, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return raw, err == nil, err
}
func (s *storage) hashCachePut(ctx context.Context, plugin, key string, raw []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO plugins_hash_cache(plugin,kkey,value) VALUES(?,?,?) ON CONFLICT(plugin,kkey) DO UPDATE SET value=excluded.value`, plugin, key, raw)
	return err
}
