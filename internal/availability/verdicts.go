package availability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// One durable "When they can play" verdict per (policy, title, release
	// descriptor): a feed item and a recorded search can describe the same
	// release id differently, and each keeps its own verdict.
	// The policy key covers the owner's profile and resolver settings, so a
	// section load only evaluates feed items it has never judged (#303).
	store.Register("availability_verdicts", 1, `
CREATE TABLE availability_verdicts (
 policy TEXT NOT NULL, rating_key INTEGER NOT NULL, release_id TEXT NOT NULL,
 digest TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', evaluated_at INTEGER NOT NULL,
 PRIMARY KEY(policy, rating_key, release_id, digest));
CREATE INDEX availability_verdicts_release ON availability_verdicts(release_id);
CREATE INDEX availability_verdicts_age ON availability_verdicts(evaluated_at);`)
}

// feedPair is one retained release considered as evidence for one title.
// The release carries its parsed Info.
type feedPair struct {
	t *core.Title
	r core.Release
}

type feedEvaluator interface {
	FeedEvaluation(context.Context) (core.FeedEvaluation, error)
}

// judge reports which pairs the owner's quality policy accepts. A validator
// that prepares evaluation passes is asked once per pass, and only for
// pairs with no durable verdict under the current policy; others are asked
// one release at a time. With anyPerTitle the caller only needs to know
// whether each title has an accepted release: a title's remaining pairs are
// skipped (reported false) once one is accepted.
func (s *Service) judge(ctx context.Context, pairs []feedPair, anyPerTitle bool) []bool {
	ok := make([]bool, len(pairs))
	if len(pairs) == 0 {
		return ok
	}
	source, batched := s.d.Validator.(feedEvaluator)
	if !batched {
		accepted := map[core.RatingKey]bool{}
		for i, p := range pairs {
			if anyPerTitle && accepted[p.t.Key] {
				continue
			}
			_, reason, err := s.d.Validator.FeedCandidate(ctx, p.t, p.r)
			ok[i] = err == nil && reason == ""
			accepted[p.t.Key] = accepted[p.t.Key] || ok[i]
		}
		return ok
	}
	ev, err := source.FeedEvaluation(ctx)
	if err != nil {
		return ok
	}
	defer ev.Close()
	policy := ev.Key()
	digests := make([]string, len(pairs))
	titles := map[core.RatingKey]string{}
	for i, p := range pairs {
		td, seen := titles[p.t.Key]
		if !seen {
			td = titleDigest(p.t)
			titles[p.t.Key] = td
		}
		digests[i] = verdictDigest(td, p.r)
	}
	stored := s.storedVerdicts(ctx, policy, titles)
	type group struct {
		t       *core.Title
		indexes []int
	}
	var order []core.RatingKey
	pending := map[core.RatingKey]*group{}
	accepted := map[core.RatingKey]bool{}
	hits := make([]bool, len(pairs))
	for i, p := range pairs {
		if reason, hit := stored[verdictKey(p.t.Key, p.r.ID, digests[i])]; hit {
			hits[i] = true
			ok[i] = reason == "" && ev.Transient(p.r.ID) == ""
			accepted[p.t.Key] = accepted[p.t.Key] || ok[i]
		}
	}
	for i, p := range pairs {
		if hits[i] || (anyPerTitle && accepted[p.t.Key]) {
			continue
		}
		g := pending[p.t.Key]
		if g == nil {
			g = &group{t: p.t}
			pending[p.t.Key] = g
			order = append(order, p.t.Key)
		}
		g.indexes = append(g.indexes, i)
	}
	var durable []verdictRow
	written := map[string]bool{}
	now := s.now().Unix()
	for _, k := range order {
		g := pending[k]
		// The whole title in one call, or release by release until one is
		// accepted when that is all the caller needs.
		step := len(g.indexes)
		if anyPerTitle {
			step = 1
		}
		for start := 0; start < len(g.indexes) && !(anyPerTitle && accepted[k]); start += step {
			batch := g.indexes[start:min(start+step, len(g.indexes))]
			rs := make([]core.Release, len(batch))
			for j, i := range batch {
				rs[j] = pairs[i].r
			}
			verdicts, err := ev.Evaluate(ctx, g.t, rs)
			if err != nil || len(verdicts) != len(rs) {
				break
			}
			for j, i := range batch {
				v := verdicts[j]
				ok[i] = v.Reason == ""
				accepted[k] = accepted[k] || ok[i]
				vk := verdictKey(k, pairs[i].r.ID, digests[i])
				if !v.Volatile && !written[vk] {
					written[vk] = true
					durable = append(durable, verdictRow{policy: policy, key: k, id: pairs[i].r.ID, digest: digests[i], why: v.Reason, at: now})
				}
			}
		}
	}
	s.rememberVerdicts(ctx, durable)
	return ok
}

// verdictRow is one durable verdict awaiting or being written.
type verdictRow struct {
	policy          string
	key             core.RatingKey
	id, digest, why string
	at              int64
}

func (v verdictRow) queueKey() string { return v.policy + "\x00" + verdictKey(v.key, v.id, v.digest) }

// verdictQueue holds verdicts judged on a request until one writer stores
// them. Reads consult it as they consult the table, so a verdict counts as
// stored from the moment it is judged: the next section load judges nothing
// even while the write is still behind (#303).
type verdictQueue struct {
	behind   bool                  // set by New; a bare Service writes in line
	pending  map[string]verdictRow // judged, not yet taken by the writer
	inflight map[string]verdictRow // taken by the writer, not yet committed
	writing  bool
}

// maxQueuedVerdicts bounds the verdicts held in memory behind requests. A
// burst beyond it is not remembered; the next read that needs one judges it.
const maxQueuedVerdicts = 4096

type offRequestKey struct{}

// offRequest marks a context as service-owned background work: it stores
// its verdicts before it returns instead of behind it.
func offRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, offRequestKey{}, true)
}

// rememberVerdicts stores rows. Background passes (the hidden-set refresh)
// write in line, so their verdicts are durable before the set they computed
// is published. A browse request has its answer already; it queues the rows
// for the single writer and does not write itself.
func (s *Service) rememberVerdicts(ctx context.Context, rows []verdictRow) {
	if len(rows) == 0 {
		return
	}
	s.verdictMu.Lock()
	q := &s.verdictQueue
	if !q.behind || ctx.Value(offRequestKey{}) != nil {
		s.verdictMu.Unlock()
		s.writeVerdicts(ctx, rows)
		return
	}
	if q.pending == nil {
		q.pending = map[string]verdictRow{}
	}
	for _, r := range rows {
		if len(q.pending)+len(q.inflight) >= maxQueuedVerdicts {
			break
		}
		q.pending[r.queueKey()] = r
	}
	start := !q.writing
	q.writing = true
	s.verdictMu.Unlock()
	if !start {
		return
	}
	s.visibilityMu.Lock()
	closed := s.closed
	if !closed {
		s.bg.Add(1)
	}
	s.visibilityMu.Unlock()
	bg := store.DetachQueryStats(context.WithoutCancel(ctx))
	if closed {
		s.drainVerdicts(bg)
		return
	}
	go func() {
		defer s.bg.Done()
		s.drainVerdicts(bg)
	}()
}

// drainVerdicts writes queued verdicts until none are left. Close waits for
// it, so a restart finds every verdict judged before it.
func (s *Service) drainVerdicts(ctx context.Context) {
	for {
		s.verdictMu.Lock()
		q := &s.verdictQueue
		if len(q.pending) == 0 {
			q.inflight, q.writing = nil, false
			s.verdictMu.Unlock()
			return
		}
		q.inflight, q.pending = q.pending, nil
		rows := make([]verdictRow, 0, len(q.inflight))
		for _, r := range q.inflight {
			rows = append(rows, r)
		}
		s.verdictMu.Unlock()
		wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		s.writeVerdicts(wctx, rows)
		cancel()
	}
}

// writeVerdicts upserts rows in one transaction. A failed write leaves them
// unstored; the next read that needs one judges it again.
func (s *Service) writeVerdicts(ctx context.Context, rows []verdictRow) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	for _, f := range rows {
		if _, err := tx.ExecContext(ctx, `INSERT INTO availability_verdicts(policy,rating_key,release_id,digest,reason,evaluated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(policy,rating_key,release_id,digest) DO UPDATE SET reason=excluded.reason,evaluated_at=excluded.evaluated_at`, f.policy, int64(f.key), f.id, f.digest, f.why, f.at); err != nil {
			return
		}
	}
	_ = tx.Commit()
}

func verdictKey(key core.RatingKey, id, digest string) string {
	return observationKey(id, key, digest)
}

// storedVerdicts maps verdictKey to the stored reason ("" accepts).
func (s *Service) storedVerdicts(ctx context.Context, policy string, titles map[core.RatingKey]string) map[string]string {
	out := map[string]string{}
	keys := make([]any, 0, len(titles))
	for k := range titles {
		keys = append(keys, int64(k))
	}
	for start := 0; start < len(keys); start += 500 {
		batch := keys[start:min(start+500, len(keys))]
		args := append([]any{policy}, batch...)
		rows, err := s.db.QueryContext(ctx, `SELECT rating_key,release_id,digest,reason FROM availability_verdicts WHERE policy=? AND rating_key IN (`+store.Placeholders(len(batch))+`)`, args...)
		if err != nil {
			return out
		}
		for rows.Next() {
			var key int64
			var id, digest, reason string
			if rows.Scan(&key, &id, &digest, &reason) == nil {
				out[verdictKey(core.RatingKey(key), id, digest)] = reason
			}
		}
		rows.Close()
	}
	s.verdictMu.Lock()
	for _, m := range []map[string]verdictRow{s.verdictQueue.inflight, s.verdictQueue.pending} {
		for _, r := range m {
			if _, ok := titles[r.key]; ok && r.policy == policy {
				out[verdictKey(r.key, r.id, r.digest)] = r.why
			}
		}
	}
	s.verdictMu.Unlock()
	return out
}

// titleDigest covers the title facts release matching and the quality
// policy read: identity, names, year and dates, runtime (size sanity),
// languages, genres and keywords (anime), countries and directors.
// Popularity and artwork churn do not invalidate a verdict.
func titleDigest(t *core.Title) string {
	directors := make([]string, len(t.Directors))
	for i, d := range t.Directors {
		directors[i] = d.Name
	}
	b, _ := json.Marshal([]any{t.Key, t.Type, t.IDs, t.ShowKey, t.ShowIDs, t.ShowTitle, t.ShowOriginalTitle, t.ShowYear,
		t.Season, t.Episode, t.AbsoluteEpisode, t.Title, t.OriginalTitle, t.Year, t.Released, t.Runtime, t.Genres,
		t.Keywords, t.AlternativeTitles, t.ShowAlternativeTitles, t.Countries, t.Language, directors})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// verdictDigest binds a verdict to the exact release descriptor (its parsed
// Info derives from the name) and title facts it judged.
func verdictDigest(title string, r core.Release) string {
	r.Info = core.ReleaseInfo{}
	b, _ := json.Marshal(r)
	sum := sha256.Sum256(append([]byte(title+"\x00"), b...))
	return hex.EncodeToString(sum[:])
}

// pruneVerdicts keeps the verdict table to the current policy, to releases
// still retained as feed items or observations, and to 60,000 rows: the
// retained feed items plus the release observations it can describe.
func (s *Service) pruneVerdicts(ctx context.Context, retainedChanged bool) error {
	if source, ok := s.d.Validator.(feedEvaluator); ok {
		if ev, err := source.FeedEvaluation(ctx); err == nil {
			policy := ev.Key()
			ev.Close()
			var stale bool
			if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM availability_verdicts WHERE policy<>?)`, policy).Scan(&stale); err != nil {
				return err
			}
			if stale {
				if _, err := s.db.ExecContext(ctx, `DELETE FROM availability_verdicts WHERE policy<>?`, policy); err != nil {
					return err
				}
			}
		}
	}
	if retainedChanged {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM availability_verdicts WHERE NOT EXISTS(SELECT 1 FROM availability_feed_items f WHERE f.id=availability_verdicts.release_id) AND NOT EXISTS(SELECT 1 FROM availability_releases r WHERE r.id=availability_verdicts.release_id)`); err != nil {
			return err
		}
	}
	var overflow bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM availability_verdicts LIMIT 1 OFFSET 60000)`).Scan(&overflow); err != nil {
		return err
	}
	if overflow {
		_, err := s.db.ExecContext(ctx, `DELETE FROM availability_verdicts WHERE (policy,rating_key,release_id,digest) IN (SELECT policy,rating_key,release_id,digest FROM availability_verdicts ORDER BY evaluated_at DESC LIMIT 2147483647 OFFSET 60000)`)
		return err
	}
	return nil
}
