package availability

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

const (
	// hiddenRetentionSeconds covers the 5-minute pagingCut lifetime plus a
	// 1-minute in-flight query slack so paginated clients see an immutable cut.
	hiddenRetentionSeconds = 6 * 60
	// maxRetiredHiddenSeqs bounds how many retired snapshots stay readable.
	maxRetiredHiddenSeqs = 5
	hiddenInsertBatch    = 500
	// hiddenOpenSeq marks a still-current interval: from_seq<=N<to_seq.
	hiddenOpenSeq = math.MaxInt64
)

// hiddenDB is the query surface shared by *sql.DB and *sql.Tx.
type hiddenDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// pruneHiddenRetention deletes intervals no retained snapshot can still see.
// Retained seqs are the current seq plus the newest maxRetiredHiddenSeqs
// retired seqs inside hiddenRetentionSeconds; retire times are monotonic in
// seq, so they form a contiguous range [floor, curSeq] and an interval is
// needed iff to_seq > floor.
func pruneHiddenRetention(ctx context.Context, db hiddenDB, kind string, curSeq, now int64) (int64, error) {
	floor := curSeq
	rows, err := db.QueryContext(ctx, `SELECT seq FROM availability_hidden_retired WHERE kind=? AND retired_at>=? ORDER BY seq DESC LIMIT ?`, kind, now-hiddenRetentionSeconds, maxRetiredHiddenSeqs)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			rows.Close()
			return 0, err
		}
		if seq < floor {
			floor = seq
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	res, err := db.ExecContext(ctx, `DELETE FROM availability_hidden WHERE kind=? AND to_seq<=?`, kind, floor)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := db.ExecContext(ctx, `DELETE FROM availability_hidden_retired WHERE kind=? AND seq<?`, kind, floor); err != nil {
		return n, err
	}
	return n, nil
}

// HiddenPublish reports one publishHidden: the membership rows it wrote
// (Added inserts + Removed closes), the resulting seq, and how long its
// transaction held the writer.
type HiddenPublish struct {
	Added, Removed int
	Seq            int64
	Hold           time.Duration
}

// PublishHiddenSet publishes keys as kind's hidden set under the current
// policy. It exists for the library-scale performance harness, which
// publishes a set, churns a few dozen keys, and republishes to prove the
// rows written track the delta, not the set.
func (s *Service) PublishHiddenSet(ctx context.Context, kind string, keys []core.RatingKey) (HiddenPublish, error) {
	switch kind {
	case hiddenMovies, hiddenShows, hiddenEpisodes:
	default:
		return HiddenPublish{}, fmt.Errorf("availability: unknown hidden kind %q", kind)
	}
	input := s.snapshotFor(ctx)
	return s.publishHiddenMeasured(ctx, kind, hiddenPolicy(kind, input), keys)
}

func (s *Service) publishHidden(ctx context.Context, kind, policy string, keys []core.RatingKey) error {
	_, err := s.publishHiddenMeasured(ctx, kind, policy, keys)
	return err
}

// publishHiddenMeasured writes only the delta between the current published
// set and keys: membership is an interval [from_seq,to_seq) per key,
// publishing seq N+1 inserts added keys and closes removed ones. An
// unchanged set (and policy) writes nothing. Older seqs stay readable for
// in-flight paging.
func (s *Service) publishHiddenMeasured(ctx context.Context, kind, policy string, keys []core.RatingKey) (HiddenPublish, error) {
	sorted := append([]core.RatingKey(nil), keys...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	sorted = slices.Compact(sorted)
	started := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HiddenPublish{}, err
	}
	defer tx.Rollback()
	if err = store.LockKey(ctx, store.DialectOf(s.db), tx, "availability-hidden-"+kind); err != nil {
		return HiddenPublish{}, err
	}
	var prevSeq int64
	var prevPolicy string
	err = tx.QueryRowContext(ctx, `SELECT seq,policy FROM availability_hidden_current WHERE kind=?`, kind).Scan(&prevSeq, &prevPolicy)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return HiddenPublish{}, err
	}
	published := err == nil
	// Current membership, in rating_key order via the (kind,to_seq,rating_key)
	// index, merged against sorted to get the delta.
	var added, removed []core.RatingKey
	rows, err := tx.QueryContext(ctx, `SELECT rating_key FROM availability_hidden WHERE kind=? AND to_seq=? ORDER BY rating_key`, kind, int64(hiddenOpenSeq))
	if err != nil {
		return HiddenPublish{}, err
	}
	i := 0
	for rows.Next() {
		var cur int64
		if err := rows.Scan(&cur); err != nil {
			rows.Close()
			return HiddenPublish{}, err
		}
		for i < len(sorted) && int64(sorted[i]) < cur {
			added = append(added, sorted[i])
			i++
		}
		if i < len(sorted) && int64(sorted[i]) == cur {
			i++
		} else {
			removed = append(removed, core.RatingKey(cur))
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return HiddenPublish{}, err
	}
	rows.Close()
	added = append(added, sorted[i:]...)
	now := s.now().Unix()
	setChanged := !published || len(added) > 0 || len(removed) > 0
	changed := setChanged || prevPolicy != policy
	s.log.Debug("availability hidden publish", "kind", kind, "added", len(added), "removed", len(removed), "set", len(sorted), "published", published)
	if !changed {
		// Unchanged set and policy: write nothing (retention runs on the
		// next changing publish and in Prune).
		return HiddenPublish{Seq: prevSeq, Hold: time.Since(started)}, nil
	}
	newSeq := prevSeq
	if setChanged {
		newSeq = prevSeq + 1
		for start := 0; start < len(added); start += hiddenInsertBatch {
			chunk := added[start:min(start+hiddenInsertBatch, len(added))]
			var sb strings.Builder
			sb.Grow(96 + len(chunk)*10)
			sb.WriteString(`INSERT INTO availability_hidden(kind,rating_key,from_seq,to_seq) VALUES`)
			args := make([]any, 0, len(chunk)*4)
			for j, key := range chunk {
				if j > 0 {
					sb.WriteByte(',')
				}
				sb.WriteString(`(?,?,?,?)`)
				args = append(args, kind, int64(key), newSeq, int64(hiddenOpenSeq))
			}
			sb.WriteString(` ON CONFLICT(kind,rating_key,from_seq) DO UPDATE SET to_seq=excluded.to_seq`)
			if _, err = tx.ExecContext(ctx, sb.String(), args...); err != nil {
				return HiddenPublish{}, err
			}
		}
		for start := 0; start < len(removed); start += hiddenInsertBatch {
			chunk := removed[start:min(start+hiddenInsertBatch, len(removed))]
			marks := make([]string, len(chunk))
			args := make([]any, 0, len(chunk)+3)
			args = append(args, newSeq, kind, int64(hiddenOpenSeq))
			for j, key := range chunk {
				marks[j] = "?"
				args = append(args, int64(key))
			}
			if _, err = tx.ExecContext(ctx, `UPDATE availability_hidden SET to_seq=? WHERE kind=? AND to_seq=? AND rating_key IN (`+strings.Join(marks, ",")+`)`, args...); err != nil {
				return HiddenPublish{}, err
			}
		}
		if published && prevSeq > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO availability_hidden_retired(kind,seq,retired_at) VALUES(?,?,?) ON CONFLICT(kind,seq) DO NOTHING`, kind, prevSeq, now); err != nil {
				return HiddenPublish{}, err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO availability_hidden_current(kind,seq,policy) VALUES(?,?,?) ON CONFLICT(kind) DO UPDATE SET seq=excluded.seq,policy=excluded.policy`, kind, newSeq, policy); err != nil {
		return HiddenPublish{}, err
	}
	if _, err = pruneHiddenRetention(ctx, tx, kind, newSeq, now); err != nil {
		return HiddenPublish{}, err
	}
	if err := tx.Commit(); err != nil {
		return HiddenPublish{}, err
	}
	stats := HiddenPublish{Added: len(added), Removed: len(removed), Seq: newSeq, Hold: time.Since(started)}
	if c, ok := s.d.Catalog.(interface{ ConsoleGateChanged() }); ok {
		c.ConsoleGateChanged()
	}
	return stats, nil
}

type pagingCut struct {
	at                   time.Time
	movie, show, episode int64
}

// PagingFilter captures the current hidden-set seqs once. On a new
// installation the first set is computed before it can be used for a page.
func (s *Service) PagingFilter(ctx context.Context, f core.Filter) (core.Filter, error) {
	if f.IncludeKeys != nil {
		return f, nil
	}
	input := s.snapshotFor(ctx)
	f.DigitalDiscovery = input.availability.ModeFor("movie") == config.ModeDigital
	f.DigitalDateEvidence = input.availability.DigitalDateEvidence || f.DigitalDiscovery
	if f.DigitalDateEvidence {
		f.HomeReleaseOffsetDays = input.availability.HomeReleaseOffsetDays
	}
	// A tokenless client shares this cut with all requests for the same filter.
	// Adapters supply a client/section/sort scope; callers without one share
	// a cut by filter. Policy changes select a new cut immediately.
	cutKey := core.BrowseScope(ctx) + ":" + hiddenKey("paging", input) + ":" + pagingFilterKey(f)
	s.visibilityMu.Lock()
	cut, pinned := s.pagingCuts[cutKey]
	s.visibilityMu.Unlock()
	if pinned && input.now.Sub(cut.at) < 5*time.Minute && !input.now.Before(cut.at) {
		f.HiddenMovieSeq, f.HiddenShowSeq, f.HiddenEpisodeSeq = cut.movie, cut.show, cut.episode
		for kind, seq := range map[string]int64{hiddenMovies: cut.movie, hiddenShows: cut.show, hiddenEpisodes: cut.episode} {
			if seq != 0 {
				s.currentHidden(kind, input)
			}
		}
		return f, nil
	}
	var kinds []string
	if f.Type != core.TypeMovie && input.availability.ShowMode != config.ModeImmediate {
		kinds = append(kinds, hiddenShows)
	}
	if (f.Type == "" || f.Type == core.TypeMovie) && !movieImmediate(input.availability) {
		kinds = append(kinds, hiddenMovies)
	}
	if f.Type == core.TypeEpisode && input.availability.ShowMode != config.ModeImmediate {
		kinds = append(kinds, hiddenEpisodes)
	}
	for _, kind := range kinds {
		var seq int64
		var policy string
		err := s.db.QueryRowContext(ctx, `SELECT seq,policy FROM availability_hidden_current WHERE kind=?`, kind).Scan(&seq, &policy)
		want := hiddenPolicy(kind, input)
		if err == nil && policy != want {
			if _, e := s.waitHidden(ctx, kind, input, true); e != nil {
				return f, e
			}
			err = s.db.QueryRowContext(ctx, `SELECT seq,policy FROM availability_hidden_current WHERE kind=?`, kind).Scan(&seq, &policy)
			if err == nil && policy != want {
				return f, fmt.Errorf("availability: %s policy changed during refresh", kind)
			}
		}
		if err != nil {
			if err != sql.ErrNoRows {
				return f, err
			}
			// waitHidden starts (or joins) a bounded refresh and propagates failures.
			if _, e := s.waitHidden(ctx, kind, input, true); e != nil {
				return f, fmt.Errorf("availability: initialize %s: %w", kind, e)
			}
			if e := s.db.QueryRowContext(ctx, `SELECT seq FROM availability_hidden_current WHERE kind=?`, kind).Scan(&seq); e != nil {
				return f, e
			}
		} else {
			s.currentHidden(kind, input)
		}
		switch kind {
		case hiddenMovies:
			f.HiddenMovieSeq = seq
		case hiddenShows:
			f.HiddenShowSeq = seq
		case hiddenEpisodes:
			f.HiddenEpisodeSeq = seq
		}
	}

	s.visibilityMu.Lock()
	if s.pagingCuts == nil {
		s.pagingCuts = map[string]pagingCut{}
	}
	for key, old := range s.pagingCuts {
		if input.now.Sub(old.at) >= 5*time.Minute {
			delete(s.pagingCuts, key)
		}
	}
	// Concurrent first pages must converge on the same cut.
	if old, ok := s.pagingCuts[cutKey]; ok && input.now.Sub(old.at) < 5*time.Minute {
		f.HiddenMovieSeq, f.HiddenShowSeq, f.HiddenEpisodeSeq = old.movie, old.show, old.episode
	} else {
		s.pagingCuts[cutKey] = pagingCut{input.now, f.HiddenMovieSeq, f.HiddenShowSeq, f.HiddenEpisodeSeq}
	}
	s.visibilityMu.Unlock()
	return f, nil
}

func hiddenPolicy(kind string, input discoverySnapshot) string {
	sum := sha256.Sum256([]byte(hiddenKey(kind, input)))
	return hex.EncodeToString(sum[:])
}

// Runtime pointer addresses and previously attached generations are not filter
// identity. Nested groups are serialized by value, including their SQL filters.
func pagingFilterKey(f core.Filter) string {
	group, child := f.Group, f.ChildVisibility
	f.Group, f.ChildVisibility = nil, nil
	f.HiddenMovieSeq, f.HiddenShowSeq, f.HiddenEpisodeSeq = 0, 0, 0
	raw, _ := json.Marshal(group)
	childKey := ""
	if child != nil {
		childKey = pagingFilterKey(*child)
	}
	return fmt.Sprintf("%+v|%s|%s", f, raw, childKey)
}
