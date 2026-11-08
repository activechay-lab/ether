package onair

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// On Air asset bytes live in files, not in the database.
//
// Every onair_assets row is the asset's record and its reference count
// anchor. Its bytes are an immutable, content-addressed file in the node's
// blob store (blobStore). The row's stored column says where the database
// side stands:
//
//   - 'db': content holds the bytes. Rows written before files existed, and
//     rows written while some cluster member cannot read files yet, start
//     here. Every node copies them into its blob store and records a replica.
//   - 'file': content is empty. A row moves here only once every member
//     advertises file support and holds a verified replica, so no node is
//     ever left without a copy it can reach.
//
// The move-out is a background pass, one asset per short statement, that
// is idempotent and safe to stop at any point: the file is durable before
// its replica row, the replica row before the content is emptied. Reads
// honour both states. A member that lacks an asset whose row is already
// 'file' (a node that joined later, or an older binary rejoining) is served
// by a holder putting the bytes back ('file' -> 'db'); the shared database
// is the only transport this needs, and it already authenticates every node.
//
// A standalone server is its own only member: new uploads go straight to
// files, and the move-out empties existing rows on the first pass.

func init() {
	// stored is the last column, so on SQLite reading it for a row written
	// before this migration, or emptied since, never walks an overflow chain.
	// The index keeps the move-out's scans off the table pages.
	store.Register("onair", 5, `ALTER TABLE onair_assets ADD COLUMN stored TEXT NOT NULL DEFAULT 'db';
CREATE INDEX onair_assets_stored ON onair_assets(stored,digest);
CREATE TABLE onair_asset_replicas(digest TEXT NOT NULL,node_id TEXT NOT NULL,verified_at INTEGER NOT NULL,PRIMARY KEY(digest,node_id));`)
}

// AssetFilesVersion is advertised in a node's capabilities once it keeps
// On Air asset bytes in files. Nodes without it read content from the
// database, so content stays there while any member lacks it.
const AssetFilesVersion = 1

// Member is one node sharing this database.
type Member struct {
	ID string
	// Files is true when the node advertises AssetFilesVersion.
	Files bool
}

const (
	// assetPassInterval spaces background move-out passes; a poke (upload,
	// membership change, a peer asking for bytes) runs one at once.
	assetPassInterval = 30 * time.Second
	// assetSweepInterval spaces orphan sweeps and replica verification.
	assetSweepInterval = time.Hour
	// assetOrphanAge protects a file being recorded from the sweep.
	assetOrphanAge = time.Hour
	// assetReleaseBytes of emptied rows are returned to the filesystem at
	// a time during a move-out.
	assetReleaseBytes = 64 << 20
	// assetFetchWait bounds how long a read waits for a peer to put an
	// asset's bytes back into the database.
	assetFetchWait = 15 * time.Second
)

var errAssetUnavailable = errors.New("onair: asset bytes are not available on this server yet")

func (s *Service) members(ctx context.Context) ([]Member, error) {
	if s.o.Members == nil {
		return []Member{{ID: s.nodeID, Files: true}}, nil
	}
	return s.o.Members(ctx)
}

// soleMember reports whether this node is the only member, so a new asset
// can go straight to a file.
func (s *Service) soleMember(ctx context.Context) bool {
	ms, e := s.members(ctx)
	return e == nil && len(ms) == 1 && ms[0].ID == s.nodeID && ms[0].Files
}

func (s *Service) pokeAssets() {
	select {
	case s.assetPoke <- struct{}{}:
	default:
	}
}

// PokeAssets runs a move-out pass soon; the app calls it for peer requests
// and membership changes.
func (s *Service) PokeAssets() { s.pokeAssets() }

func (s *Service) announceAssets(ctx context.Context) {
	s.pokeAssets()
	if s.o.AssetsChanged != nil {
		s.o.AssetsChanged(ctx)
	}
}

func (s *Service) recordReplica(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, d string) error {
	_, e := q.ExecContext(ctx, "INSERT INTO onair_asset_replicas(digest,node_id,verified_at) VALUES(?,?,?) ON CONFLICT(digest,node_id) DO UPDATE SET verified_at=excluded.verified_at", d, s.nodeID, time.Now().Unix())
	return e
}

// newContent is what a new row stores in content: nothing when this node is
// the only member (the file is the copy), otherwise the bytes, until every
// member holds a replica. bytes is called only when they are needed.
func (s *Service) newContent(ctx context.Context, bytes func() ([]byte, error)) (string, []byte, error) {
	if s.soleMember(ctx) {
		return "file", []byte{}, nil
	}
	b, e := bytes()
	return "db", b, e
}

// assetPath returns the local file holding the asset, fetching it into the
// blob store first when needed. The file is immutable: callers read it in
// place and never remove it.
func (s *Service) assetPath(ctx context.Context, d string) (string, error) {
	var size int64
	e := s.db.QueryRowContext(ctx, "SELECT size FROM onair_assets WHERE digest=?", d).Scan(&size)
	if errors.Is(e, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if e != nil {
		return "", e
	}
	if s.blobs.Has(d, size) {
		return s.blobs.path(d), nil
	}
	return s.fetchAsset(ctx, d, size)
}

// assetBytes reads a small asset (fonts, images, packs) into memory.
func (s *Service) assetBytes(ctx context.Context, d string) ([]byte, error) {
	p, e := s.assetPath(ctx, d)
	if e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}

// serveAsset serves an asset's file with Range support and bounded memory.
func (s *Service) serveAsset(w http.ResponseWriter, r *http.Request, d string) {
	var mime string
	if e := s.db.QueryRowContext(r.Context(), "SELECT mime FROM onair_assets WHERE digest=?", d).Scan(&mime); e != nil {
		result(w, nil, ErrNotFound)
		return
	}
	p, e := s.assetPath(r.Context(), d)
	if e != nil {
		if errors.Is(e, errAssetUnavailable) {
			http.Error(w, e.Error(), http.StatusServiceUnavailable)
			return
		}
		result(w, nil, ErrNotFound)
		return
	}
	f, e := os.Open(p)
	if e != nil {
		result(w, nil, ErrNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("ETag", `"`+d+`"`)
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "asset", time.Time{}, f)
}

// fetchAsset brings an asset this node lacks into its blob store: from the
// database when the row still holds the bytes, otherwise by asking the
// members holding it to put them back, within assetFetchWait.
func (s *Service) fetchAsset(ctx context.Context, d string, size int64) (string, error) {
	s.fetchMu.Lock()
	wait, busy := s.fetching[d]
	if !busy {
		wait = make(chan struct{})
		s.fetching[d] = wait
	}
	s.fetchMu.Unlock()
	if busy {
		select {
		case <-wait:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if s.blobs.Has(d, size) {
			return s.blobs.path(d), nil
		}
		return "", errAssetUnavailable
	}
	defer func() {
		s.fetchMu.Lock()
		delete(s.fetching, d)
		s.fetchMu.Unlock()
		close(wait)
	}()
	if s.blobs.Has(d, -1) && !s.blobs.Has(d, size) {
		// A file of the wrong size was damaged outside Ether.
		_ = s.blobs.Remove(d)
	}
	deadline := time.Now().Add(assetFetchWait)
	asked := false
	for {
		ok, e := s.materialize(ctx, d)
		if e != nil {
			return "", e
		}
		if ok {
			return s.blobs.path(d), nil
		}
		ms, e := s.members(ctx)
		if e != nil {
			return "", e
		}
		if len(ms) < 2 {
			// Nobody else can hold it: the file was lost (for example a
			// backup restored without its On Air media).
			return "", ErrNotFound
		}
		if !asked {
			asked = true
			s.announceAssets(ctx)
		}
		if time.Now().After(deadline) {
			return "", errAssetUnavailable
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// materialize copies a 'db' row's bytes into the blob store and records the
// replica. It reports whether the file is present afterwards; a 'file' row
// whose file this node lacks reports false.
func (s *Service) materialize(ctx context.Context, d string) (bool, error) {
	if s.blobs.Has(d, -1) {
		return true, nil
	}
	var stored string
	var b []byte
	e := s.db.QueryRowContext(ctx, "SELECT stored,content FROM onair_assets WHERE digest=?", d).Scan(&stored, &b)
	if errors.Is(e, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if e != nil {
		return false, e
	}
	if stored != "db" {
		return false, nil
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != d {
		return false, fmt.Errorf("onair: stored bytes of asset %s do not match its digest", d)
	}
	if _, _, e = s.blobs.Put(d, bytes.NewReader(b)); e != nil {
		return false, e
	}
	b = nil
	// A delete may have committed while the bytes were copied; it removed
	// the row, so the file goes too.
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	var n int
	if e = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM onair_assets WHERE digest=?", d).Scan(&n); e != nil {
		return false, e
	}
	if n == 0 {
		_ = s.blobs.Remove(d)
		return false, ErrNotFound
	}
	return true, s.recordReplica(ctx, s.db, d)
}

// removeUnreferenced deletes the local files of digests whose rows are gone;
// callers run it after their delete commits, holding assetMu.
func (s *Service) removeUnreferenced(ctx context.Context, digests ...string) {
	for _, d := range digests {
		var n int
		if s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM onair_assets WHERE digest=?", d).Scan(&n) == nil && n == 0 {
			if e := s.blobs.Remove(d); e != nil {
				s.o.Logger.Warn("onair asset file not removed; the sweep retries", "asset", d, "err", e)
			}
		}
	}
}

// AssetPassStats describes one move-out pass.
type AssetPassStats struct {
	Copied, Emptied, Restored int
	// CopiedBytes went from database rows into files; EmptiedBytes left the
	// database.
	CopiedBytes, EmptiedBytes int64
	// LongestWrite is the longest single write statement of the pass (the
	// write lock on SQLite).
	LongestWrite time.Duration
	// Released is the free pages returned to the filesystem afterwards.
	Released int64
}

func (st *AssetPassStats) wrote(start time.Time) {
	st.LongestWrite = max(st.LongestWrite, time.Since(start))
}

// moveAssets is one move-out pass: copy 'db' rows into this node's blob
// store, empty each row as soon as every member holds it, and put back the
// bytes of 'file' rows a member lacks. Every write is one short statement
// per asset, and emptied pages go back to the filesystem every
// assetReleaseBytes, so the extra disk the move needs stays near one asset.
func (s *Service) moveAssets(ctx context.Context) (AssetPassStats, error) {
	var st AssetPassStats
	ms, e := s.members(ctx)
	if e != nil {
		return st, e
	}
	allFiles, ids := true, make([]string, 0, len(ms))
	for _, m := range ms {
		allFiles = allFiles && m.Files
		ids = append(ids, m.ID)
	}
	dbRows, e := s.assetDigests(ctx, "db")
	if e != nil {
		return st, e
	}
	replicas, e := s.replicaHolders(ctx)
	if e != nil {
		return st, e
	}
	unreleased := int64(0)
	release := func() {
		n, e := store.ReleaseFreePages(ctx, s.db)
		st.Released += n
		if e != nil && ctx.Err() == nil {
			s.o.Logger.Warn("onair: freed asset pages not returned yet; daily maintenance retries", "err", e)
		}
		unreleased = 0
	}
	for _, d := range dbRows {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		had := s.blobs.Has(d.digest, d.size)
		ok, e := s.materialize(ctx, d.digest)
		if errors.Is(e, ErrNotFound) {
			continue
		}
		if e != nil {
			s.o.Logger.Warn("onair asset not copied to a file", "asset", d.digest, "err", e)
			continue
		}
		if !ok {
			continue
		}
		if !had {
			st.Copied++
			st.CopiedBytes += d.size
		}
		if replicas[d.digest] == nil {
			replicas[d.digest] = map[string]bool{}
		}
		if !replicas[d.digest][s.nodeID] {
			// The file may have arrived another way (a restore, a pass
			// that crashed after the rename): record it.
			start := time.Now()
			if e = s.recordReplica(ctx, s.db, d.digest); e != nil {
				return st, e
			}
			st.wrote(start)
			replicas[d.digest][s.nodeID] = true
		}
		if !allFiles || !holdsAll(replicas[d.digest], ids) {
			continue
		}
		start := time.Now()
		n, e := s.emptyRow(ctx, d.digest, ids)
		st.wrote(start)
		if e != nil {
			return st, e
		}
		if n > 0 {
			st.Emptied++
			st.EmptiedBytes += d.size
			if unreleased += d.size; unreleased >= assetReleaseBytes {
				release()
			}
		}
	}
	if unreleased > 0 {
		release()
	}
	if len(ms) > 1 || !allFiles {
		fileRows, e := s.assetDigests(ctx, "file")
		if e != nil {
			return st, e
		}
		for _, d := range fileRows {
			if allFiles && holdsAll(replicas[d.digest], ids) || !s.blobs.Has(d.digest, d.size) {
				continue
			}
			start := time.Now()
			n, e := s.restoreRow(ctx, d.digest)
			st.wrote(start)
			if e != nil {
				return st, e
			}
			if n > 0 {
				st.Restored++
			}
		}
	}
	if st.Copied+st.Emptied+st.Restored > 0 {
		s.o.Logger.Info("onair assets moved", "copied", st.Copied, "emptied", st.Emptied, "restored", st.Restored, "emptiedBytes", st.EmptiedBytes, "longestWrite", st.LongestWrite, "releasedPages", st.Released)
	}
	return st, nil
}

type assetRef struct {
	digest string
	size   int64
}

func (s *Service) assetDigests(ctx context.Context, stored string) ([]assetRef, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT digest,size FROM onair_assets WHERE stored=? ORDER BY digest", stored)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []assetRef
	for rows.Next() {
		var a assetRef
		if e = rows.Scan(&a.digest, &a.size); e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) replicaHolders(ctx context.Context) (map[string]map[string]bool, error) {
	rows, e := s.db.QueryContext(ctx, "SELECT digest,node_id FROM onair_asset_replicas")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var d, n string
		if e = rows.Scan(&d, &n); e != nil {
			return nil, e
		}
		if out[d] == nil {
			out[d] = map[string]bool{}
		}
		out[d][n] = true
	}
	return out, rows.Err()
}

func holdsAll(have map[string]bool, ids []string) bool {
	for _, id := range ids {
		if !have[id] {
			return false
		}
	}
	return len(ids) > 0
}

// emptyRow drops a row's bytes from the database once the replicas of every
// member in ids are recorded, re-checked in the same statement.
func (s *Service) emptyRow(ctx context.Context, d string, ids []string) (int64, error) {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{[]byte{}, d, d}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, len(ids))
	res, e := s.db.ExecContext(ctx, "UPDATE onair_assets SET content=?,stored='file' WHERE digest=? AND stored='db' AND (SELECT COUNT(*) FROM onair_asset_replicas WHERE digest=? AND node_id IN ("+marks+"))=?", args...)
	if e != nil {
		return 0, e
	}
	return res.RowsAffected()
}

// restoreRow puts an asset's bytes back into its row for a member that
// cannot reach them otherwise.
func (s *Service) restoreRow(ctx context.Context, d string) (int64, error) {
	f, e := s.blobs.Open(d)
	if e != nil {
		return 0, nil
	}
	b, e := io.ReadAll(f)
	f.Close()
	if e != nil {
		return 0, e
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != d {
		// Never publish damaged bytes; drop the bad file and its replica.
		_ = s.blobs.Remove(d)
		_, e = s.db.ExecContext(ctx, "DELETE FROM onair_asset_replicas WHERE digest=? AND node_id=?", d, s.nodeID)
		return 0, e
	}
	res, e := s.db.ExecContext(ctx, "UPDATE onair_assets SET content=?,stored='db' WHERE digest=? AND stored='file'", b, d)
	if e != nil {
		return 0, e
	}
	return res.RowsAffected()
}

// sweepAssets reconciles this node's blob store with the database: replica
// rows for files that are gone are dropped, files that arrived without a
// replica row are recorded, files no row references (deleted elsewhere, or
// left by a crash) are removed once older than assetOrphanAge, and replica
// rows of members that left are forgotten.
func (s *Service) sweepAssets(ctx context.Context) error {
	mine := map[string]bool{}
	rows, e := s.db.QueryContext(ctx, "SELECT digest FROM onair_asset_replicas WHERE node_id=?", s.nodeID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			mine[d] = true
		}
	}
	rows.Close()
	sizes := map[string]int64{}
	rows, e = s.db.QueryContext(ctx, "SELECT digest,size FROM onair_assets")
	if e != nil {
		return e
	}
	for rows.Next() {
		var d string
		var n int64
		if rows.Scan(&d, &n) == nil {
			sizes[d] = n
		}
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	rows.Close()
	for d := range mine {
		if size, ok := sizes[d]; !ok || !s.blobs.Has(d, size) {
			if _, e = s.db.ExecContext(ctx, "DELETE FROM onair_asset_replicas WHERE digest=? AND node_id=?", d, s.nodeID); e != nil {
				return e
			}
		}
	}
	var orphans []string
	e = s.blobs.Walk(assetOrphanAge, func(d string, mod time.Time) {
		size, ok := sizes[d]
		switch {
		case !ok && time.Since(mod) > assetOrphanAge:
			orphans = append(orphans, d)
		case ok && !mine[d] && s.blobs.Has(d, size):
			_ = s.recordReplica(ctx, s.db, d)
		}
	})
	if e != nil {
		return e
	}
	if len(orphans) > 0 {
		s.assetMu.Lock()
		s.removeUnreferenced(ctx, orphans...)
		s.assetMu.Unlock()
	}
	if ms, e := s.members(ctx); e == nil && len(ms) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(ms)), ",")
		args := make([]any, 0, len(ms))
		for _, m := range ms {
			args = append(args, m.ID)
		}
		if _, e = s.db.ExecContext(ctx, "DELETE FROM onair_asset_replicas WHERE node_id NOT IN ("+marks+")", args...); e != nil {
			return e
		}
	}
	_, e = s.db.ExecContext(ctx, "DELETE FROM onair_asset_replicas WHERE NOT EXISTS(SELECT 1 FROM onair_assets WHERE onair_assets.digest=onair_asset_replicas.digest)")
	return e
}

// runAssets runs move-out passes and sweeps until the service closes.
func (s *Service) runAssets() {
	defer s.wg.Done()
	sweep := time.Now()
	if e := s.sweepAssets(s.ctx); e != nil && s.ctx.Err() == nil {
		s.o.Logger.Warn("onair asset sweep failed", "err", e)
	}
	t := time.NewTimer(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		case <-s.assetPoke:
		}
		if _, e := s.moveAssets(s.ctx); e != nil && s.ctx.Err() == nil {
			s.o.Logger.Warn("onair asset move-out pass failed; it retries", "err", e)
		}
		if time.Since(sweep) > assetSweepInterval {
			sweep = time.Now()
			if e := s.sweepAssets(s.ctx); e != nil && s.ctx.Err() == nil {
				s.o.Logger.Warn("onair asset sweep failed", "err", e)
			}
		}
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(assetPassInterval)
	}
}

// BackupAssets lists the assets whose bytes live only in files (rows with
// stored='file' in db, which may be a snapshot copy) and opens each one
// from this node's blob store, for backup bundles.
func BackupAssets(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, e := db.QueryContext(ctx, "SELECT digest FROM onair_assets WHERE stored='file' ORDER BY digest")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if e = rows.Scan(&d); e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
