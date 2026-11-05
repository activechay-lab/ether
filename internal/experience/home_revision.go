package experience

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
)

// HomeRevision stamps a viewer's cached Home. Viewer changes the Home must
// show on the very next request: the latest watch-state write (Continue
// Watching, watched badges, ratings), playlists, watchlist, favorites and
// "not interested" exclusions. Policy is the shared viewer-policy epoch:
// it also moves on catalogue and guide changes, so only restricted viewers
// treat it as a hard input. One statement of indexed point reads, whatever
// the library or history size.
type HomeRevision struct {
	Viewer string
	Policy int64
}

func ReadHomeRevision(ctx context.Context, db *sql.DB, user *core.User) (HomeRevision, error) {
	if db == nil || user == nil {
		return HomeRevision{}, sql.ErrNoRows
	}
	id := user.ID
	var watch, playlists, playlistAt, watchlist, watchlistAt, favorites, exclusions, policy int64
	err := db.QueryRowContext(ctx, `SELECT
COALESCE((SELECT MAX(updated_at) FROM state_watch WHERE user_id=?),0),
(SELECT COUNT(*) FROM state_playlists WHERE user_id=?),
COALESCE((SELECT MAX(updated_at) FROM state_playlists WHERE user_id=?),0),
(SELECT COUNT(*) FROM watchlist_items WHERE user_id=?),
COALESCE((SELECT MAX(added) FROM watchlist_items WHERE user_id=?),0),
COALESCE((SELECT updated_at FROM favorites_mirror WHERE viewer_id=?),0),
COALESCE((SELECT revision FROM notinterested_revisions WHERE viewer_id=?),0),
COALESCE((SELECT generation FROM viewer_policy_epoch WHERE id=1),0)`, id, id, id, id, id, id, id).
		Scan(&watch, &playlists, &playlistAt, &watchlist, &watchlistAt, &favorites, &exclusions, &policy)
	if err != nil {
		return HomeRevision{}, err
	}
	rules, err := catalog.ReadViewerRulesRevision(ctx, db, id)
	if err != nil {
		return HomeRevision{}, err
	}
	b := make([]byte, 0, 96)
	for i, n := range []int64{watch, playlists, playlistAt, watchlist, watchlistAt, favorites, exclusions, rules} {
		if i > 0 {
			b = append(b, '.')
		}
		b = strconv.AppendInt(b, n, 36)
	}
	return HomeRevision{Viewer: string(b), Policy: policy}, nil
}
