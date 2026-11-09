package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("plex_downloads", 1, `CREATE TABLE plex_downloads (server_id TEXT PRIMARY KEY, snapshot TEXT NOT NULL);`)
}

// The single writer coalesces queue mutations; GET handlers never perform SQL
// writes. Only queue state is visited, independently of the catalogue size.
type savedDownload struct {
	ID                                int64
	Key                               core.RatingKey
	Title                             string
	Caps                              core.ClientCaps
	Status, Error, Path, Video, Audio string
	Size                              int64
	Ready                             time.Time
	Duration                          time.Duration
	Bandwidth                         int
	Burn                              bool
}
type savedDownloadQueue struct {
	ID, UserID int64
	ClientID   string
	Items      []savedDownload
}
type savedDownloads struct {
	NextQueue, NextItem, NextRun int64
	Queues                       []savedDownloadQueue
}

type retainedDownloads interface{ RetainDownload(string, time.Time) error }

// DownloadQueuePersistence owns coalesced snapshots and retained-file recovery.
// Callbacks supply current host configuration without exposing an adapter.
type DownloadQueuePersistence struct {
	Queue     *DownloadQueueStore
	DB        *sql.DB
	Artifacts core.DownloadRemuxer
	Log       *slog.Logger
	Expiry    func() time.Duration
	StorageID func() string
	Users     func(context.Context) ([]core.User, error)
}

func (p DownloadQueuePersistence) Retain(path string, ready time.Time) {
	if p.DB == nil {
		return
	}
	if d, ok := p.Artifacts.(retainedDownloads); ok {
		expiry := p.Expiry()
		if expiry <= 0 {
			expiry = 24 * time.Hour
		}
		if err := d.RetainDownload(path, ready.Add(expiry)); err != nil {
			p.Log.Warn("retain download", "err", err)
		}
	}
}
func (p DownloadQueuePersistence) Save() error {
	if p.DB == nil {
		return nil
	}
	st := p.Queue
	st.Mu.Lock()
	snap := savedDownloads{NextQueue: st.NextQueue, NextItem: st.NextItem, NextRun: st.NextRun}
	for _, q := range st.Queues {
		sq := savedDownloadQueue{ID: q.ID, UserID: q.UserID, ClientID: q.ClientID}
		for _, it := range q.Items {
			sq.Items = append(sq.Items, savedDownload{ID: it.ID, Key: it.Key, Title: it.Title, Caps: it.Caps, Status: it.Status, Error: it.Error, Path: it.Path, Video: it.VideoCodec, Audio: it.AudioCodec, Size: it.Size, Ready: it.ReadyAt, Duration: it.Duration, Bandwidth: it.Bandwidth, Burn: it.Burn})
		}
		snap.Queues = append(snap.Queues, sq)
	}
	st.Mu.Unlock()
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = p.DB.ExecContext(ctx, `INSERT INTO plex_downloads(server_id,snapshot) VALUES(?,?) ON CONFLICT(server_id) DO UPDATE SET snapshot=excluded.snapshot`, p.StorageID(), string(data))
	return err
}
func (p DownloadQueuePersistence) Load(ctx context.Context) {
	if p.DB == nil || p.Users == nil {
		return
	}
	var data string
	if err := p.DB.QueryRowContext(ctx, `SELECT snapshot FROM plex_downloads WHERE server_id=?`, p.StorageID()).Scan(&data); err != nil {
		return
	}
	var snap savedDownloads
	if err := json.Unmarshal([]byte(data), &snap); err != nil {
		p.Log.Warn("load downloads", "err", err)
		return
	}
	users, err := p.Users(ctx)
	if err != nil {
		return
	}
	byID := map[int64]*core.User{}
	for i := range users {
		byID[users[i].ID] = &users[i]
	}
	st := p.Queue
	st.NextQueue, st.NextItem, st.NextRun = snap.NextQueue, snap.NextItem, snap.NextRun
	for _, sq := range snap.Queues {
		u := byID[sq.UserID]
		if u == nil {
			continue
		}
		q := &DownloadQueue{ID: sq.ID, UserID: sq.UserID, ClientID: sq.ClientID}
		for _, si := range sq.Items {
			it := &DownloadItem{ID: si.ID, QueueID: q.ID, Key: si.Key, Title: si.Title, Caps: si.Caps, User: u, Status: si.Status, Error: si.Error, Path: si.Path, Size: si.Size, ReadyAt: si.Ready, VideoCodec: si.Video, AudioCodec: si.Audio, Duration: si.Duration, Bandwidth: si.Bandwidth, Burn: si.Burn}
			switch it.Status {
			case downloadDeciding, downloadWaiting, downloadProcessing:
				it.Status = downloadDeciding
			}
			if it.Status == downloadAvailable {
				if _, err := os.Stat(it.Path); err != nil {
					it.Status = downloadExpired
					it.Path = ""
					it.Size = 0
				}
				if it.Path != "" {
					p.Retain(it.Path, it.ReadyAt)
					it.Progress = 1
				}
			}
			q.Items = append(q.Items, it)
		}
		st.Queues[q.ID] = q
		st.ByClient[strconv.FormatInt(q.UserID, 10)+"\x00"+q.ClientID] = q.ID
	}
}
func (p DownloadQueuePersistence) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.Queue.Wake:
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err := p.Save(); err != nil {
			p.Log.Warn("persist downloads", "err", err)
		}
	}
}

// A restart keeps completed files; pending jobs restart from their saved caps
// only after the current account is authorized again.
