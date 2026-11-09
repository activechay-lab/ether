package sessions

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

const (
	downloadDeciding   = "deciding"
	downloadWaiting    = "waiting"
	downloadProcessing = "processing"
	downloadAvailable  = "available"
	downloadError      = "error"
	downloadExpired    = "expired"
	downloadDone       = "done" // queue-level only
)

type DownloadItem struct {
	ID      int64
	QueueID int64
	Key     core.RatingKey
	Title   string // display Title, for the served filename

	Caps core.ClientCaps // as requested at add time (Download set at Run)
	User *core.User

	Status   string
	Error    string
	Progress float64 // 0..1
	Started  bool
	Cancel   context.CancelFunc
	// Run numbers each start of the Item, so a restarted job gets its own
	// playback session instead of sharing (and later stopping) the
	// previous Run's.
	Run int64

	Path       string
	Size       int64
	ReadyAt    time.Time
	VideoCodec string
	AudioCodec string
	Duration   time.Duration
	Bandwidth  int // requested maxVideoBitrate, kbps
	Burn       bool
}

type DownloadQueue struct {
	ID       int64
	UserID   int64
	ClientID string
	Items    []*DownloadItem
}

type DownloadQueueStore struct {
	Mu        sync.Mutex
	Queues    map[int64]*DownloadQueue
	ByClient  map[string]int64 // "UserID\x00clientID" -> queue ID
	NextQueue int64
	NextItem  int64
	NextRun   int64
	Wake      chan struct{}
}

func NewDownloadQueueStore() *DownloadQueueStore {
	return &DownloadQueueStore{Wake: make(chan struct{}, 1), Queues: map[int64]*DownloadQueue{}, ByClient: map[string]int64{}}
}

func (st *DownloadQueueStore) QueueFor(UserID int64, ClientID string, create bool) *DownloadQueue {
	st.Mu.Lock()
	defer st.Mu.Unlock()
	k := strconv.FormatInt(UserID, 10) + "\x00" + ClientID
	if ID, ok := st.ByClient[k]; ok {
		return st.Queues[ID]
	}
	if !create {
		return nil
	}
	st.NextQueue++
	q := &DownloadQueue{ID: st.NextQueue, UserID: UserID, ClientID: ClientID}
	st.Queues[q.ID] = q
	st.ByClient[k] = q.ID
	return q
}

// QueueByID returns the queue when it belongs to this User and client.
func (st *DownloadQueueStore) QueueByID(ID, UserID int64, ClientID string) *DownloadQueue {
	st.Mu.Lock()
	defer st.Mu.Unlock()
	q := st.Queues[ID]
	if q == nil || q.UserID != UserID || q.ClientID != ClientID {
		return nil
	}
	return q
}

func (q *DownloadQueue) Item(ID int64) *DownloadItem {
	for _, it := range q.Items {
		if it.ID == ID {
			return it
		}
	}
	return nil
}

func (st *DownloadQueueStore) Add(q *DownloadQueue, u *core.User, t core.Title, caps core.ClientCaps, burn bool) *DownloadItem {
	st.Mu.Lock()
	defer st.Mu.Unlock()
	for _, it := range q.Items {
		if it.Key == t.Key && it.Caps.MaxHeight == caps.MaxHeight &&
			it.Caps.MaxBitrateKbps == caps.MaxBitrateKbps && it.Burn == burn &&
			it.Status != downloadError && it.Status != downloadExpired {
			return it
		}
	}
	st.NextItem++
	it := &DownloadItem{
		ID: st.NextItem, QueueID: q.ID, Key: t.Key, Title: t.Title,
		Caps: caps, User: u, Status: downloadDeciding, Bandwidth: caps.MaxBitrateKbps,
		Burn: burn,
	}
	q.Items = append(q.Items, it)
	return it
}

type DownloadStart struct {
	Item    *DownloadItem
	Context context.Context
	Run     int64
}

// StartReady atomically reserves each eligible run before any worker starts.
func (st *DownloadQueueStore) StartReady(ctx context.Context, perUser int, allowed func(*core.User) bool) []DownloadStart {
	if ctx.Err() != nil {
		return nil
	}
	if perUser <= 0 {
		perUser = 1
	}
	var jobs []DownloadStart
	st.Mu.Lock()
	active := map[int64]int{}
	for _, q := range st.Queues {
		for _, it := range q.Items {
			if it.Started && (it.Status == downloadDeciding || it.Status == downloadWaiting || it.Status == downloadProcessing) {
				active[it.User.ID]++
			}
		}
	}
	for _, q := range st.Queues {
		for _, it := range q.Items {
			if !allowed(it.User) || it.Started || (it.Status != downloadDeciding && it.Status != downloadWaiting) {
				continue
			}
			if active[it.User.ID] >= perUser {
				it.Status = downloadWaiting
				continue
			}
			ctx, cancel := context.WithCancel(ctx)
			st.NextRun++
			it.Cancel = cancel
			it.Started = true
			it.Status = downloadDeciding
			it.Run = st.NextRun
			active[it.User.ID]++
			jobs = append(jobs, DownloadStart{it, ctx, it.Run})
		}
	}
	st.Mu.Unlock()
	return jobs
}

// Expire clears retained artifacts under the queue lock; callers release files
// after unlocking, keeping I/O outside the queue's state transition.
func (st *DownloadQueueStore) Expire(now time.Time, expiry time.Duration) []string {
	if expiry <= 0 {
		expiry = 24 * time.Hour
	}
	var paths []string
	st.Mu.Lock()
	for _, q := range st.Queues {
		for _, it := range q.Items {
			if it.Status == downloadAvailable && it.Path != "" && now.After(it.ReadyAt.Add(expiry)) {
				paths = append(paths, it.Path)
				it.Path, it.Size = "", 0
				it.Status = downloadExpired
			}
		}
	}
	st.Mu.Unlock()
	return paths
}

func (st *DownloadQueueStore) StopForRestart() {
	st.Mu.Lock()
	for _, q := range st.Queues {
		for _, it := range q.Items {
			if it.Cancel != nil {
				it.Cancel()
			}
		}
	}
	st.Mu.Unlock()
}

func (st *DownloadQueueStore) Remove(q *DownloadQueue, ids []int64) []*DownloadItem {
	st.Mu.Lock()
	defer st.Mu.Unlock()
	keep := q.Items[:0]
	var removed []*DownloadItem
	for _, it := range q.Items {
		drop := false
		for _, id := range ids {
			if it.ID == id {
				drop = true
			}
		}
		if drop {
			removed = append(removed, it)
		} else {
			keep = append(keep, it)
		}
	}
	q.Items = keep
	return removed
}

func (st *DownloadQueueStore) Restart(q *DownloadQueue, ids []int64, release func(string)) {
	st.Mu.Lock()
	defer st.Mu.Unlock()
	for _, id := range ids {
		it := q.Item(id)
		if it == nil {
			continue
		}
		if it.Cancel != nil {
			it.Cancel()
		}
		if it.Path != "" {
			release(it.Path)
		}
		it.Path, it.Size, it.ReadyAt = "", 0, time.Time{}
		it.Error, it.Progress, it.Started = "", 0, false
		it.Status = downloadDeciding
	}
}

func (st *DownloadQueueStore) CancelItem(it *DownloadItem) string {
	st.Mu.Lock()
	cancel, path := it.Cancel, it.Path
	it.Path = ""
	st.Mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return path
}

func (st *DownloadQueueStore) Fail(ctx context.Context, it *DownloadItem, message string) {
	st.Mu.Lock()
	defer st.Mu.Unlock()
	if ctx.Err() == nil {
		it.Status, it.Error = downloadError, message
	}
}

func (st *DownloadQueueStore) Prepared(ctx context.Context, it *DownloadItem, info core.MediaInfo) bool {
	st.Mu.Lock()
	defer st.Mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	it.Status, it.Duration = downloadWaiting, info.Duration
	if v := info.Video(); v != nil {
		it.VideoCodec = v.Codec
	}
	for _, stream := range info.Streams {
		if stream.Type == "audio" {
			it.AudioCodec = stream.Codec
			break
		}
	}
	return true
}

func (st *DownloadQueueStore) Progress(ctx context.Context, it *DownloadItem, fraction float64) {
	st.Mu.Lock()
	defer st.Mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	it.Progress = fraction
	if it.Status == downloadWaiting {
		it.Status = downloadProcessing
	}
}

func (st *DownloadQueueStore) Complete(ctx context.Context, it *DownloadItem, path string, size int64, now time.Time) bool {
	st.Mu.Lock()
	defer st.Mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	it.Path, it.Size, it.ReadyAt = path, size, now
	it.Progress, it.Status = 1, downloadAvailable
	return true
}
