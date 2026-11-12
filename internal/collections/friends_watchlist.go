package collections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/store"
)

// FriendsWatchlistTitle is the shared collection and Home/section row title
// produced from the unattributed Plex Friends' Watchlist RSS feed.
const FriendsWatchlistTitle = "Your friends want to watch"

const (
	friendsWatchlistKindID  = "friendsWatchlist"
	friendsWatchlistMetaKey = "friends-watchlist-spec-id"
)

type friendsWatchlistKind struct{}

func (friendsWatchlistKind) ID() string { return friendsWatchlistKindID }

func (friendsWatchlistKind) Describe() KindDescribe {
	return KindDescribe{
		Label:    extend.T("Friends' watchlists"),
		Hint:     extend.T("Titles from your configured Plex Friends' Watchlist RSS feed"),
		Icon:     "users",
		Order:    35,
		Editor:   "none",
		Defaults: `{}`,
	}
}

func (friendsWatchlistKind) Validate(*ValidationError, string, core.CollectionSource) {}

func (friendsWatchlistKind) Gather(g *Gather) {
	titles, err := loadFriendsWatchlistTitles(g.Ctx, g.Svc.db)
	if err != nil {
		g.Warn("friends' watchlists: %v", err)
		return
	}
	if len(titles) == 0 {
		g.Warn("%s is empty", sourceName(g.Src))
		return
	}
	g.Res.titles = append(g.Res.titles, titles...)
}

func loadFriendsWatchlistTitles(ctx context.Context, db *sql.DB) ([]core.Title, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT title FROM watchlist_items WHERE user_id=0 AND (source='plex-friends-rss' OR source LIKE 'plex-community:%') ORDER BY added DESC, item_key`)
	if err != nil {
		if isMissingTableErr(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	var out []core.Title
	seen := map[core.RatingKey]bool{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var t core.Title
		if json.Unmarshal([]byte(raw), &t) != nil || t.Key == 0 || t.Adult || seen[t.Key] {
			continue
		}
		if t.Type != core.TypeMovie && t.Type != core.TypeShow {
			continue
		}
		seen[t.Key] = true
		out = append(out, t)
	}
	return out, rows.Err()
}

func countFriendsWatchlistItems(ctx context.Context, db *sql.DB) (int, error) {
	if db == nil {
		return 0, nil
	}
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM watchlist_items WHERE user_id=0 AND (source='plex-friends-rss' OR source LIKE 'plex-community:%')`).Scan(&n)
	if err != nil {
		if isMissingTableErr(err) {
			return 0, nil
		}
		return 0, err
	}
	return n, nil
}

func isMissingTableErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "does not exist")
}

func friendsWatchlistDefaultSpec() core.CollectionSpec {
	sp := core.CollectionSpec{
		Name:        FriendsWatchlistTitle,
		Description: "Movies and shows from your Plex friends' watchlists.",
		MediaType:   MediaMixed,
		Sources:     []core.CollectionSource{{Kind: friendsWatchlistKindID}},
		Match:       MatchAll,
		Sort:        "source",
		Limit:       100,
		Refresh:     RefreshHourly,
		Enabled:     true,
		Artwork:     core.CollectionArtwork{Mode: "auto"},
		Targets: core.CollectionTargets{
			CollectionsTab: true,
			HomeRow:        true,
			SectionRow:     true,
			Users:          core.AllUsers(),
		},
	}
	Normalize(&sp)
	return sp
}

// EnsureFriendsWatchlist creates and builds the "Your friends want to watch"
// collection on startup when household watchlist rows already exist without a
// managed collection row.
func (s *Service) EnsureFriendsWatchlist(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	if s.meta(ctx, friendsWatchlistMetaKey) != "" {
		return nil
	}
	n, err := countFriendsWatchlistItems(ctx, s.db)
	if err != nil || n == 0 {
		return err
	}
	return s.SyncFriendsWatchlist(ctx)
}

// SyncFriendsWatchlist ensures the "Your friends want to watch" collection
// exists once the household Friends' Watchlist RSS set has items, and rebuilds
// its membership when the set changes or is cleared.
func (s *Service) SyncFriendsWatchlist(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	count, err := countFriendsWatchlistItems(ctx, s.db)
	if err != nil {
		return err
	}

	specID, enabled, err := s.ensureFriendsWatchlistRow(ctx, count > 0)
	if err != nil || specID == "" || !enabled {
		return err
	}
	_, err = s.Rebuild(ctx, specID)
	return err
}

func (s *Service) ensureFriendsWatchlistRow(ctx context.Context, createIfMissing bool) (string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()

	if err := store.LockKey(ctx, store.DialectOf(s.db), tx, "collections:friends-watchlist"); err != nil {
		return "", false, err
	}

	var specID string
	err = tx.QueryRowContext(ctx, `SELECT v FROM collections_meta WHERE k=?`, friendsWatchlistMetaKey).Scan(&specID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}

	if specID != "" {
		if id, parseErr := strconv.ParseInt(specID, 10, 64); parseErr == nil {
			var enabledInt int
			rowErr := tx.QueryRowContext(ctx, `SELECT enabled FROM collections_specs WHERE id=?`, id).Scan(&enabledInt)
			if rowErr == nil {
				if err := tx.Commit(); err != nil {
					return "", false, err
				}
				return specID, enabledInt != 0, nil
			}
			if !errors.Is(rowErr, sql.ErrNoRows) {
				return "", false, rowErr
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM collections_meta WHERE k=?`, friendsWatchlistMetaKey); err != nil {
			return "", false, err
		}
		specID = ""
	}

	if !createIfMissing {
		if err := tx.Commit(); err != nil {
			return "", false, err
		}
		return "", false, nil
	}

	id, err := s.insertSpecTx(ctx, tx, friendsWatchlistDefaultSpec(), 0)
	if err != nil {
		return "", false, err
	}
	specID = strconv.FormatInt(id, 10)
	if _, err := tx.ExecContext(ctx, `INSERT INTO collections_meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, friendsWatchlistMetaKey, specID); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return specID, true, nil
}
