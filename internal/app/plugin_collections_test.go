package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPluginCollectionsWriteAndUndoIsolation(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cat, err := catalog.New(db, nil, config.Catalog{Language: "en-US", Region: "US"}, log)
	if err != nil {
		t.Fatal(err)
	}
	cols, err := collections.New(db, cat, log)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, catalog: cat, collections: cols}
	mgr, err := plugins.New(plugins.Deps{DB: db, Backends: plugins.Backends{CollectionsUpsert: a.pluginCollectionUpsert, CollectionsDelete: a.pluginCollectionDelete}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		manifest := fmt.Sprintf(`{"id":%q,"name":"Test","version":"1.0.0","ether":{"permissions":{"collections":"write"}}}`, id)
		_, err := db.Exec(`INSERT INTO plugins_installed(id,name,version,manifest,installed_at,updated_at) VALUES(?, 'Test','1.0.0',?,0,0)`, id, manifest)
		if err != nil {
			t.Fatal(err)
		}
	}
	title := core.Title{Key: core.MovieKey(101), Type: core.TypeMovie, Title: "Test title", Year: 2000, Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), IDs: core.IDs{TMDB: 101, IMDB: "tt0000101"}}
	if err := cat.UpsertDetailedTitle(ctx, title); err != nil {
		t.Fatal(err)
	}
	first := json.RawMessage(`{"id":"picks","name":"First","items":["tmdb:101"]}`)
	updated := json.RawMessage(`{"id":"picks","name":"Updated","itemIds":["tt0000101"]}`)
	write := func(id string, raw json.RawMessage) {
		t.Helper()
		if err := mgr.HostFor(id).CollectionsUpsert(ctx, raw); err != nil {
			t.Fatal(err)
		}
	}
	lookup := func(id string) string {
		t.Helper()
		var key string
		if err := db.QueryRow(`SELECT collection_id FROM plugin_collection_links WHERE plugin=? AND local_id='picks'`, id).Scan(&key); err != nil {
			t.Fatal(err)
		}
		return key
	}
	write("one", first)
	firstID := lookup("one")
	write("two", first)
	secondID := lookup("two")
	if firstID == secondID {
		t.Fatal("cross-plugin collision")
	}
	write("one", updated)
	if lookup("one") != firstID {
		t.Fatal("update changed server id")
	}
	var updateJournal int64
	if err := db.QueryRow(`SELECT MAX(id) FROM plugins_journal WHERE plugin='one'`).Scan(&updateJournal); err != nil {
		t.Fatal(err)
	}
	if n, err := mgr.UndoSince(ctx, "one", updateJournal); err != nil || n != 1 {
		t.Fatalf("undo update %d %v", n, err)
	}
	restored, err := cols.GetSpec(ctx, firstID)
	if err != nil || restored.Name != "First" {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	if n, err := mgr.UndoAll(ctx, "one"); err != nil || n != 1 {
		t.Fatalf("undo create %d %v", n, err)
	}
	if _, err := cols.GetSpec(ctx, firstID); err == nil {
		t.Fatal("created collection survived undo")
	}
	if _, err := cols.GetSpec(ctx, secondID); err != nil {
		t.Fatal("other plugin was affected", err)
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM plugin_collection_links WHERE plugin='one'`).Scan(&count)
	if count != 0 {
		t.Fatal("stale mapping after undo")
	}
	write("one", first)
	if lookup("one") == firstID {
		t.Fatal("recreate reused deleted id")
	}
	if err := mgr.HostFor("one").CollectionsUpsert(ctx, json.RawMessage(`{"id":"bad","name":"Bad","items":["tmdb:99999999"]}`)); err == nil {
		t.Fatal("unresolved title accepted")
	}
	// Explicit audiences survive the compact SDK form.
	write("one", json.RawMessage(`{"id":"picks","name":"Restricted","items":["tmdb:101"],"targets":{"collectionsTab":true,"users":[7]}}`))
	restricted, err := cols.GetSpec(ctx, lookup("one"))
	if err != nil || restricted.Targets.Users.All {
		t.Fatalf("audience widened: %+v %v", restricted, err)
	}
	// A manual deletion leaves a stale link; the next write safely recreates it.
	deletedID := lookup("one")
	if err := cols.DeleteSpec(ctx, deletedID); err != nil {
		t.Fatal(err)
	}
	write("one", first)
	if lookup("one") == deletedID {
		t.Fatal("stale mapping was not replaced")
	}

	if _, err := a.pluginCollectionUpsert(ctx, first); err == nil {
		t.Fatal("missing identity accepted")
	}
}
