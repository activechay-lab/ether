package sessions

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// Version ids are derived from (surface, title, release) with a keyed
	// hash, so one row per pair serves every viewer and device: no
	// per-viewer handle rows and no admission cap. The per-viewer table
	// and its capability headers go.
	store.RegisterAfter("sessions_version_ids", "sessions", "capability_headers")
	store.Register("sessions_version_ids", 1, `CREATE TABLE sessions_version_ids (
		handle TEXT PRIMARY KEY,
		surface TEXT NOT NULL,
		item_id BIGINT NOT NULL,
		release_id TEXT NOT NULL,
		seen_at BIGINT NOT NULL
	);
	CREATE INDEX sessions_version_ids_seen ON sessions_version_ids(seen_at);
	DROP TABLE sessions_version_handles;
	DELETE FROM capability_headers WHERE namespace='sessions.version';`)
}

// versionIDSeen spaces seen_at refreshes; versionIDRetention is how long an
// id nobody lists or resolves stays resolvable. A pruned id is not lost:
// the next listing derives the same id again.
const (
	versionIDSeen      = 24 * time.Hour
	versionIDRetention = 90 * 24 * time.Hour
)

type versionHandles struct {
	db       *sql.DB
	broker   *Broker
	protocol frontend.Protocol
	// key derives ids; only issuing needs it.
	key []byte
}

// NewVersionHandles creates a surface's version identifiers. An id is a
// keyed hash of (surface, title, release) under key (a server secret), so it
// is the same for every viewer and device and reveals nothing about the
// release. Ids are not credentials: every lookup rechecks viewer and title
// policy and the release must be one the viewer is offered.
func NewVersionHandles(db *sql.DB, b *Broker, protocol frontend.Protocol, key []byte) (frontend.VersionHandles, error) {
	if db == nil || b == nil || protocol == "" || len(key) < 16 {
		return nil, errors.New("sessions: missing version handle dependency")
	}
	return &versionHandles{db: db, broker: b, protocol: protocol, key: key}, nil
}

// versionID derives the canonical UUIDv4-shaped id for one version.
func versionID(key []byte, protocol frontend.Protocol, item core.RatingKey, release string) string {
	mac := hmac.New(sha256.New, key)
	var k [8]byte
	binary.BigEndian.PutUint64(k[:], uint64(item))
	mac.Write([]byte("ether-version-id-v1\x00" + string(protocol) + "\x00"))
	mac.Write(k[:])
	mac.Write([]byte(release))
	id := mac.Sum(nil)[:16]
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}

// PruneVersionIDs deletes ids nobody listed or resolved for
// versionIDRetention, in batches.
func PruneVersionIDs(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	if db == nil {
		return 0, nil
	}
	return store.PruneBatched(ctx, db, `DELETE FROM sessions_version_ids WHERE handle IN (SELECT handle FROM sessions_version_ids WHERE seen_at<? ORDER BY seen_at LIMIT ?)`, now.Add(-versionIDRetention).Unix())
}

func (v *versionHandles) allowedOffers(ctx context.Context, scope frontend.VersionScope, item core.RatingKey) (*core.Viewer, map[string]bool, error) {
	if scope.Protocol != v.protocol || scope.Device.ID == "" || len(scope.Device.ID) > 512 || item == 0 || v.broker.deps.Variants == nil {
		return nil, nil, core.ErrNotFound
	}
	viewer, err := v.broker.viewer(ctx, scope.Viewer)
	if err != nil {
		return nil, nil, err
	}
	caps := scope.Caps
	caps.Protocol = v.protocol
	caps.ClientID = clientKey(v.protocol, scope.Device)
	viewer, err = v.broker.confirm(ctx, viewer, caps)
	if err != nil {
		return nil, nil, err
	}
	if caps.Remote && v.broker.deps.RemoteBitrateLimit != nil {
		if limit := v.broker.deps.RemoteBitrateLimit(); limit > 0 && (caps.MaxBitrateKbps == 0 || limit < caps.MaxBitrateKbps) {
			caps.MaxBitrateKbps = limit
		}
	}
	if limit := v.broker.deps.BitrateLimit(viewer); limit > 0 && (caps.MaxBitrateKbps == 0 || limit < caps.MaxBitrateKbps) {
		caps.MaxBitrateKbps = limit
	}
	title, err := v.broker.deps.Lookup(ctx, viewer, scope.Device, item)
	if err != nil || title == nil || title.Key != item || (!title.Released.IsZero() && title.Released.After(v.broker.deps.Now())) {
		return nil, nil, core.ErrNotFound
	}
	if title.Type == core.TypeShow || title.Type == core.TypeSeason {
		return nil, nil, core.ErrNotFound
	}
	offered, err := v.broker.deps.Variants(ctx, viewer, title, caps)
	if err != nil {
		return nil, nil, err
	}
	allowed := make(map[string]bool, len(offered))
	for _, candidate := range offered {
		allowed[candidate.ID] = true
	}
	if len(allowed) > 0 {
		v.broker.mu.Lock()
		for _, p := range v.broker.plans {
			if p.request.Viewer != nil && p.request.Viewer.ID == viewer.ID && p.request.Protocol == v.protocol && p.request.Device.ID == scope.Device.ID && p.request.Item == item {
				if p.exactRelease != "" {
					allowed[p.exactRelease] = true
				}
				if p.source != nil && p.source.Resolution != nil && p.source.Resolution.Release.ID != "" {
					allowed[p.source.Resolution.Release.ID] = true
				}
			}
		}
		for _, s := range v.broker.sessions {
			if s != nil && s.public.ViewerID == viewer.ID && s.public.Protocol == v.protocol && s.public.Device.ID == scope.Device.ID && s.public.Item == item {
				if s.exactRelease != "" {
					allowed[s.exactRelease] = true
				}
			}
		}
		v.broker.mu.Unlock()
	}
	return viewer, allowed, nil
}

func (v *versionHandles) IssueBatch(ctx context.Context, scope frontend.VersionScope, item core.RatingKey, releases []string) (map[string]string, error) {
	if len(releases) == 0 || len(releases) > 128 {
		return nil, core.ErrNotAvailable
	}
	_, offered, err := v.allowedOffers(ctx, scope, item)
	if err != nil {
		return nil, err
	}
	for _, release := range releases {
		if release == "" || len(release) > 512 || !offered[release] {
			return nil, core.ErrNotFound
		}
	}
	out := make(map[string]string, len(releases))
	for _, release := range releases {
		out[release] = versionID(v.key, v.protocol, item, release)
	}
	if err := v.remember(ctx, item, out); err != nil {
		return nil, err
	}
	return out, nil
}

// remember stores the ids a lookup must map back, writing only ids that are
// new or not seen for versionIDSeen, so a listing is normally one read.
func (v *versionHandles) remember(ctx context.Context, item core.RatingKey, ids map[string]string) error {
	now := v.broker.deps.Now()
	handles := make([]any, 0, len(ids))
	for _, h := range ids {
		handles = append(handles, h)
	}
	rows, err := v.db.QueryContext(ctx, `SELECT handle,seen_at FROM sessions_version_ids WHERE handle IN (`+store.Placeholders(len(handles))+`)`, handles...)
	if err != nil {
		return err
	}
	fresh := map[string]bool{}
	for rows.Next() {
		var h string
		var seen int64
		if err := rows.Scan(&h, &seen); err != nil {
			rows.Close()
			return err
		}
		fresh[h] = now.Sub(time.Unix(seen, 0)) < versionIDSeen
	}
	if err := rows.Close(); err != nil {
		return err
	}
	releases := make([]string, 0, len(ids))
	for release, h := range ids {
		if !fresh[h] {
			releases = append(releases, release)
		}
	}
	if len(releases) == 0 {
		return nil
	}
	slices.Sort(releases)
	tx, err := v.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, release := range releases {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sessions_version_ids(handle,surface,item_id,release_id,seen_at) VALUES(?,?,?,?,?) ON CONFLICT(handle) DO UPDATE SET seen_at=excluded.seen_at`, ids[release], string(v.protocol), int64(item), release, now.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (v *versionHandles) Issue(ctx context.Context, scope frontend.VersionScope, item core.RatingKey, release string) (string, error) {
	handles, err := v.IssueBatch(ctx, scope, item, []string{release})
	if err != nil {
		return "", err
	}
	return handles[release], nil
}

func (v *versionHandles) Resolve(ctx context.Context, scope frontend.VersionScope, handle string) (frontend.VersionTarget, error) {
	item, release, err := v.resolveRecord(ctx, scope, handle)
	return frontend.VersionTarget{Item: item, Release: release}, err
}

func (v *versionHandles) resolveRecord(ctx context.Context, scope frontend.VersionScope, handle string) (core.RatingKey, string, error) {
	item, release, err := v.lookupRow(ctx, scope, handle)
	if err != nil {
		return 0, "", err
	}
	viewer, offered, err := v.allowedOffers(ctx, scope, item)
	if err != nil {
		return 0, "", err
	}
	// A session this viewer and device planned with the id plays the
	// release it actually opened (a fallback when the named one failed);
	// the id names that release for as long as the session lives.
	if playing := v.sessionRelease(viewer.ID, scope.Device.ID, item, normalizeVersionHandle(handle)); playing != "" && offered[playing] {
		return item, playing, nil
	}
	if release == "" || len(release) > 512 || !offered[release] {
		return 0, "", core.ErrNotFound
	}
	return item, release, nil
}

// lookupRow reads the version an id names. It does not check that the
// viewer is offered the release; resolveRecord does that.
func (v *versionHandles) lookupRow(ctx context.Context, scope frontend.VersionScope, handle string) (core.RatingKey, string, error) {
	handle = normalizeVersionHandle(handle)
	if scope.Protocol != v.protocol || scope.Device.ID == "" || scope.Viewer == nil || handle == "" {
		return 0, "", core.ErrNotFound
	}
	if _, err := v.broker.viewer(ctx, scope.Viewer); err != nil {
		return 0, "", err
	}
	var item, seen int64
	var release string
	err := v.db.QueryRowContext(ctx, `SELECT item_id,release_id,seen_at FROM sessions_version_ids WHERE handle=? AND surface=?`, handle, string(v.protocol)).Scan(&item, &release, &seen)
	if err == nil && v.broker.deps.Now().Sub(time.Unix(seen, 0)) >= versionIDSeen {
		// A client resolving a cached id keeps it alive without a listing.
		_, _ = v.db.ExecContext(ctx, `UPDATE sessions_version_ids SET seen_at=? WHERE handle=? AND seen_at<?`, v.broker.deps.Now().Unix(), handle, v.broker.deps.Now().Add(-versionIDSeen).Unix())
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", core.ErrNotFound
	}
	if err != nil {
		return 0, "", err
	}
	return core.RatingKey(item), release, nil
}

// sessionRelease is the release a live session of this viewer and device
// opened for item through handle, or "".
func (v *versionHandles) sessionRelease(viewer int64, device string, item core.RatingKey, handle string) string {
	v.broker.mu.Lock()
	defer v.broker.mu.Unlock()
	for _, s := range v.broker.sessions {
		if s != nil && s.public.ViewerID == viewer && s.public.Protocol == v.protocol && s.public.Device.ID == device && s.public.Item == item && s.versionHandle == handle && s.exactRelease != "" {
			return s.exactRelease
		}
	}
	return ""
}

func (v *versionHandles) MatchSession(ctx context.Context, scope frontend.VersionScope, handle, sessionID string) error {
	handle = normalizeVersionHandle(handle)
	item, release, err := v.resolveRecord(ctx, scope, handle)
	if err != nil {
		return err
	}
	s, _, err := v.broker.access(ctx, frontend.SessionAccess{Viewer: scope.Viewer, Device: scope.Device, Protocol: scope.Protocol}, sessionID)
	if err != nil {
		return err
	}
	if s.public.Item != item || s.versionHandle != handle || s.exactRelease != release {
		return core.ErrNotFound
	}
	ps, ok := v.broker.deps.Playback.Session(s.playID)
	if !ok || ps == nil || ps.Resolution == nil || ps.Resolution.Release.ID != release {
		return core.ErrNotAvailable
	}
	return nil
}

func normalizeVersionHandle(handle string) string {
	if len(handle) == 36 {
		if handle[8] != '-' || handle[13] != '-' || handle[18] != '-' || handle[23] != '-' {
			return ""
		}
		handle = strings.ReplaceAll(handle, "-", "")
	}
	if len(handle) != 32 {
		return ""
	}
	id, err := hex.DecodeString(handle)
	if err != nil || id[6]>>4 != 4 || id[8]>>6 != 2 {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
}
