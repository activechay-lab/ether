package availability

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	hiddenRetentionSeconds = 6 * 60
	maxRetiredHiddenSeqs   = 5
	hiddenInsertBatch      = 500
	hiddenOpenSeq          = math.MaxInt64
)

type hiddenDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

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
// policy. Retained for the library-scale performance harness.
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

// PagingFilter attaches the sticky availability_gate policy and request-clock
// evidence window bounds to f with zero SQL round trips in steady state. On
// legacy read-only databases without availability_gate, it falls back to any
// published availability_hidden_current seqs without issuing writes.
func (s *Service) PagingFilter(ctx context.Context, f core.Filter) (core.Filter, error) {
	if f.IncludeKeys != nil {
		return f, nil
	}
	input := s.snapshotFor(ctx)
	movieMode := input.availability.ModeFor(hiddenMovies)
	showMode := input.availability.ModeFor(hiddenShows)
	f.DigitalDiscovery = movieMode == config.ModeDigital
	f.DigitalDateEvidence = input.availability.DigitalDateEvidence
	if f.DigitalDateEvidence {
		f.HomeReleaseOffsetDays = input.availability.HomeReleaseOffsetDays
	}
	if s.hasGateTable {
		s.ensureGatePolicySeeded(ctx, input)
		f.GatePolicy = input.gatePolicy
		f.GateNow = input.now.Unix()
		f.GateUpcomingMovies = input.showUpcomingMovies
		f.GateRev = s.gateRev.Load()
		if movieMode == config.ModePlayable && !input.availability.DisableMovieEvidenceGate {
			f.MovieGateCutoff = input.now.Add(-time.Duration(input.availability.EffectiveMovieEvidenceDays()) * 24 * time.Hour).Unix()
		}
		if showMode == config.ModePlayable {
			f.EpisodeGateCutoff = input.now.Add(-episodeEvidence).Unix()
			f.ShowGateCutoff = f.EpisodeGateCutoff
		}
		return f, nil
	}

	// Legacy read-only database fallback.
	for _, kind := range []string{hiddenMovies, hiddenShows, hiddenEpisodes} {
		if kind == hiddenMovies && (!f.DigitalDiscovery && (movieMode == config.ModeImmediate || input.availability.DisableMovieEvidenceGate)) {
			continue
		}
		if kind != hiddenMovies && showMode != config.ModePlayable {
			continue
		}
		var seq int64
		if err := s.db.QueryRowContext(ctx, `SELECT seq FROM availability_hidden_current WHERE kind=?`, kind).Scan(&seq); err == nil {
			switch kind {
			case hiddenMovies:
				f.HiddenMovieSeq = seq
			case hiddenShows:
				f.HiddenShowSeq = seq
			case hiddenEpisodes:
				f.HiddenEpisodeSeq = seq
			}
		}
	}
	return f, nil
}

func hiddenPolicy(kind string, input discoverySnapshot) string {
	sum := sha256.Sum256([]byte(hiddenKey(kind, input)))
	return hex.EncodeToString(sum[:])
}
