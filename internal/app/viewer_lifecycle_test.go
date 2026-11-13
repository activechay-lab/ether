package app

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/store"
)

// TestViewerMergeAndPurgeAcrossComponents exercises the whole binary's
// per-viewer registry: merging two local viewers moves their watch
// state (target wins), unions history, keeps playlists with their
// items, reassigns shared collections, and removing a viewer leaves no
// rows behind in any component's tables.
func TestViewerMergeAndPurgeAcrossComponents(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mk := func(username string) int64 {
		t.Helper()
		exec(`INSERT INTO auth_users(username,title,active,origin,created_at,updated_at) VALUES(?,?,1,'local',1,1)`, username, username)
		var id int64
		if err := db.QueryRowContext(ctx, `SELECT id FROM auth_users WHERE username=?`, username).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	src, dst := mk("src"), mk("dst")
	if err := users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	// Watch state: both have key 10 (target wins), only source has 11.
	exec(`INSERT INTO state_watch(user_id,rating_key,view_offset_ns) VALUES(?,10,200),(?,10,100),(?,11,110)`, dst, src, src)
	// History unions: 2 source rows + 1 target row.
	exec(`INSERT INTO state_history(user_id,rating_key,viewed_at) VALUES(?,10,1),(?,11,2),(?,10,3)`, src, src, dst)
	exec(`INSERT INTO stats_plays(id,user_id,rating_key,username,title,platform,device,decision,started,updated) VALUES('p1',?,10,'src','T','tv','d','direct',1,1)`, src)
	exec(`INSERT INTO personalize_plays(user_id,item_key,taste_key,started_at,updated_at) VALUES(?,10,10,1,1)`, src)
	// A playlist with items follows its owner.
	exec(`INSERT INTO state_playlists(id,user_id,title,updated_at) VALUES(77,?,'Mix',1)`, src)
	exec(`INSERT INTO state_playlist_items(playlist_id,position,rating_key) VALUES(77,1,10)`)
	exec(`INSERT INTO playback_resume(user_id,rating_key,release_id,release_json,nzb_hash,updated_at) VALUES(?,10,'r','{}','h',1)`, src)
	exec(`INSERT INTO watchlist_items(user_id,item_key,source,title,added) VALUES(?,10,'tmdb','T',1),(?,10,'tmdb','T',2)`, src, dst)
	exec(`INSERT INTO favorites_items(viewer_id,kind,subject_id,added_at) VALUES(?,'title','10',1)`, src)
	exec(`INSERT INTO trackprefs_rules(user_id,rules) VALUES(?,?)`, src, `{}`)
	exec(`INSERT INTO trackprefs_rules(user_id,rules) VALUES(?,?)`, dst, `{"keep":1}`)
	exec(`INSERT INTO collections_specs(id,owner_user_id,spec_json) VALUES(5,?,'{}')`, src)
	exec(`INSERT INTO identity_links(viewer_id,provider,subject,display,verified_at,password_hash) VALUES(?,'local','src','src',1,'h'),(?,'local','dst','dst',1,'h')`, src, dst)
	exec(`INSERT INTO identity_grants(hash,viewer_id,surface,device_id,client,product,version,created,last_used,expires) VALUES('g1',?,'jellyfin','d','c','p','1',1,1,9999999999999)`, src)
	exec(`INSERT INTO plex_sessions(id,node_id,user_id,session_key,data,updated_at) VALUES('s1','n',?,1,'{}',1)`, src)

	if err := users.MergeViewers(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var offset int64
	if err := db.QueryRowContext(ctx, `SELECT view_offset_ns FROM state_watch WHERE user_id=? AND rating_key=10`, dst).Scan(&offset); err != nil || offset != 200 {
		t.Fatalf("target watch offset=%d err=%v, want 200", offset, err)
	}
	if n := count(`SELECT COUNT(*) FROM state_watch WHERE user_id=?`, dst); n != 2 {
		t.Fatalf("target watch rows=%d, want 2", n)
	}
	if n := count(`SELECT COUNT(*) FROM state_history WHERE user_id=?`, dst); n != 3 {
		t.Fatalf("target history rows=%d, want 3 (union)", n)
	}
	if n := count(`SELECT COUNT(*) FROM state_playlists WHERE user_id=?`, dst); n != 1 {
		t.Fatalf("target playlists=%d, want 1", n)
	}
	if n := count(`SELECT COUNT(*) FROM state_playlist_items WHERE playlist_id=77`); n != 1 {
		t.Fatalf("playlist items=%d, want 1", n)
	}
	var rules string
	if err := db.QueryRowContext(ctx, `SELECT rules FROM trackprefs_rules WHERE user_id=?`, dst).Scan(&rules); err != nil || rules != `{"keep":1}` {
		t.Fatalf("target trackprefs=%q err=%v, want target's own", rules, err)
	}
	var collOwner int64
	if err := db.QueryRowContext(ctx, `SELECT owner_user_id FROM collections_specs WHERE id=5`).Scan(&collOwner); err != nil || collOwner != dst {
		t.Fatalf("collection owner=%d err=%v, want %d", collOwner, err, dst)
	}
	if n := count(`SELECT COUNT(*) FROM identity_grants WHERE viewer_id=?`, dst); n != 1 {
		t.Fatalf("target grants=%d, want 1 (moved)", n)
	}
	if n := count(`SELECT COUNT(*) FROM plex_sessions WHERE user_id IN (?,?)`, src, dst); n != 0 {
		t.Fatalf("live sessions survived merge: %d (must drop)", n)
	}
	// Nothing anywhere still belongs to the source.
	left := map[string]string{
		"state_watch": "user_id", "state_history": "user_id", "state_playlists": "user_id",
		"stats_plays": "user_id", "personalize_plays": "user_id", "playback_resume": "user_id",
		"watchlist_items": "user_id", "watchlist_community_jobs": "user_id", "watchlist_community_locks": "user_id", "favorites_items": "viewer_id", "trackprefs_rules": "user_id",
		"identity_links": "viewer_id", "identity_grants": "viewer_id", "collections_specs": "owner_user_id",
	}
	for table, col := range left {
		if n := count(`SELECT COUNT(*) FROM `+table+` WHERE `+col+`=?`, src); n != 0 {
			t.Fatalf("%s still holds %d source rows after merge", table, n)
		}
	}
	// Removing the target now purges everything, detaching (not
	// deleting) the shared collection.
	if err := users.RemoveViewer(ctx, dst); err != nil {
		t.Fatal(err)
	}
	for table, col := range left {
		if table == "collections_specs" {
			continue
		}
		if n := count(`SELECT COUNT(*) FROM `+table+` WHERE `+col+`=?`, dst); n != 0 {
			t.Fatalf("%s still holds %d rows after removal", table, n)
		}
	}
	if n := count(`SELECT COUNT(*) FROM state_playlist_items WHERE playlist_id=77`); n != 0 {
		t.Fatalf("playlist items survived removal: %d", n)
	}
	if err := db.QueryRowContext(ctx, `SELECT owner_user_id FROM collections_specs WHERE id=5`).Scan(&collOwner); err != nil || collOwner != 0 {
		t.Fatalf("collection after removal owner=%d err=%v, want 0 (detached, kept)", collOwner, err)
	}
}
