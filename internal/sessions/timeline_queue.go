package sessions

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"strconv"
)

// TimelineQueue resolves an explicit queue item before watch persistence. An
// omitted item selector follows the owning client's first matching queue row.
// Cluster fallback is capped at the same 50 recent queues as selectShared.
func (s *PlayQueues) TimelineQueue(user int64, client string, key core.RatingKey, raw string, present bool) core.RatingKey {
	itemID, _ := strconv.ParseInt(raw, 10, 64)
	if present && itemID <= 0 {
		return key
	}
	find := func(q *PlayQueue) (int, bool) {
		if q.UserID != user || q.ClientID != client {
			return 0, false
		}
		for i, it := range q.Items {
			if (present && it.ID == itemID) || (!present && it.Key == key) {
				return i, true
			}
		}
		return 0, false
	}
	var qid int64
	s.mu.Lock()
	for _, q := range s.queues {
		if i, ok := find(q); ok {
			key = q.Items[i].Key
			qid = q.ID
			break
		}
	}
	s.mu.Unlock()
	if qid == 0 && s.cl != nil {
		ctx, cancel := context.WithTimeout(context.Background(), playQueueDBTimeout)
		defer cancel()
		rows, err := s.cl.DB.QueryContext(ctx, `SELECT id,rev,data,updated_at FROM plex_playqueues WHERE user_id=? ORDER BY updated_at DESC LIMIT 50`, user)
		if err != nil {
			return key
		}
		for rows.Next() {
			var id, rev, updated int64
			var data string
			if rows.Scan(&id, &rev, &data, &updated) != nil {
				continue
			}
			q, err := DecodeQueue(id, user, rev, updated, data)
			if err != nil {
				continue
			}
			if i, ok := find(q); ok {
				key = q.Items[i].Key
				qid = id
				s.cache(q)
				break
			}
		}
		rows.Close()
	}
	if qid != 0 {
		_, _ = s.With(qid, user, s.now(), func(q *PlayQueue) {
			if i, ok := find(q); ok {
				q.Selected = i
			}
		})
	}
	return key
}
