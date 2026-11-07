package availability

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// publishHidden leaves older generations available to requests already using
// them. The pointer moves only after every member of the new set is stored.
func (s *Service) publishHidden(ctx context.Context, kind, policy string, keys []core.RatingKey) error {
	sorted := append([]core.RatingKey(nil), keys...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	hash := sha256.New()
	var buf [8]byte
	for _, key := range sorted {
		binary.BigEndian.PutUint64(buf[:], uint64(key))
		_, _ = hash.Write(buf[:])
	}
	gen := hex.EncodeToString(hash.Sum(nil))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = store.LockKey(ctx, store.DialectOf(s.db), tx, "availability-hidden-"+kind); err != nil {
		return err
	}
	// A refresh usually republishes the same set. Browse results read only
	// the current generation and policy, so an unchanged pair keeps the
	// catalogue's page cache: invalidating it on every refresh made each
	// browse on a large library a cache miss.
	var prevGen, prevPolicy string
	err = tx.QueryRowContext(ctx, `SELECT generation,policy FROM availability_hidden_current WHERE kind=?`, kind).Scan(&prevGen, &prevPolicy)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	changed := prevGen != gen || prevPolicy != policy
	now := time.Now().Unix()
	for _, key := range sorted {
		if _, err = tx.ExecContext(ctx, `INSERT INTO availability_hidden(kind,generation,rating_key,created_at) VALUES(?,?,?,?) ON CONFLICT(kind,generation,rating_key) DO UPDATE SET created_at=excluded.created_at`, kind, gen, int64(key), now); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO availability_hidden_current(kind,generation,policy) VALUES(?,?,?) ON CONFLICT(kind) DO UPDATE SET generation=excluded.generation,policy=excluded.policy`, kind, gen, policy); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM availability_hidden WHERE kind=? AND generation<>? AND created_at<?`, kind, gen, now-int64(time.Hour.Seconds())); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if c, ok := s.d.Catalog.(interface{ ConsoleGateChanged() }); ok && changed {
		c.ConsoleGateChanged()
	}
	return nil
}

type pagingCut struct {
	at                   time.Time
	movie, show, episode string
}

// PagingFilter captures the current immutable SQL generations once. On a new
// installation the first set is computed before it can be used for a page.
func (s *Service) PagingFilter(ctx context.Context, f core.Filter) (core.Filter, error) {
	if f.IncludeKeys != nil {
		return f, nil
	}
	input := s.snapshotFor(ctx)
	f.DigitalDiscovery = input.availability.EffectiveMode() == config.ModeDigital
	f.DigitalDateEvidence = input.availability.DigitalDateEvidence
	// A tokenless client shares this cut with all requests for the same filter.
	// Adapters supply a client/section/sort scope; callers without one share
	// a cut by filter. Policy changes select a new cut immediately.
	cutKey := core.BrowseScope(ctx) + ":" + hiddenKey("paging", input) + ":" + pagingFilterKey(f)
	s.visibilityMu.Lock()
	cut, pinned := s.pagingCuts[cutKey]
	s.visibilityMu.Unlock()
	if pinned && input.now.Sub(cut.at) < 5*time.Minute && !input.now.Before(cut.at) {
		f.HiddenMovieGeneration, f.HiddenShowGeneration, f.HiddenEpisodeGeneration = cut.movie, cut.show, cut.episode
		for kind, gen := range map[string]string{hiddenMovies: cut.movie, hiddenShows: cut.show, hiddenEpisodes: cut.episode} {
			if gen != "" {
				s.currentHidden(kind, input)
			}
		}
		return f, nil
	}
	var kinds []string
	if f.Type != core.TypeMovie {
		kinds = append(kinds, hiddenShows)
	}
	if (f.Type == "" || f.Type == core.TypeMovie) && (f.DigitalDiscovery || !input.availability.DisableMovieEvidenceGate) {
		kinds = append(kinds, hiddenMovies)
	}
	if f.Type == core.TypeEpisode {
		kinds = append(kinds, hiddenEpisodes)
	}
	for _, kind := range kinds {
		var gen, policy string
		err := s.db.QueryRowContext(ctx, `SELECT generation,policy FROM availability_hidden_current WHERE kind=?`, kind).Scan(&gen, &policy)
		want := hiddenPolicy(kind, input)
		if err == nil && policy != want {
			if _, e := s.waitHidden(ctx, kind, input, true); e != nil {
				return f, e
			}
			err = s.db.QueryRowContext(ctx, `SELECT generation,policy FROM availability_hidden_current WHERE kind=?`, kind).Scan(&gen, &policy)
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
			if e := s.db.QueryRowContext(ctx, `SELECT generation FROM availability_hidden_current WHERE kind=?`, kind).Scan(&gen); e != nil {
				return f, e
			}
		} else {
			s.currentHidden(kind, input)
		}
		switch kind {
		case hiddenMovies:
			f.HiddenMovieGeneration = gen
		case hiddenShows:
			f.HiddenShowGeneration = gen
		case hiddenEpisodes:
			f.HiddenEpisodeGeneration = gen
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
		f.HiddenMovieGeneration, f.HiddenShowGeneration, f.HiddenEpisodeGeneration = old.movie, old.show, old.episode
	} else {
		s.pagingCuts[cutKey] = pagingCut{input.now, f.HiddenMovieGeneration, f.HiddenShowGeneration, f.HiddenEpisodeGeneration}
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
	f.HiddenMovieGeneration, f.HiddenShowGeneration, f.HiddenEpisodeGeneration = "", "", ""
	raw, _ := json.Marshal(group)
	childKey := ""
	if child != nil {
		childKey = pagingFilterKey(*child)
	}
	return fmt.Sprintf("%+v|%s|%s", f, raw, childKey)
}
