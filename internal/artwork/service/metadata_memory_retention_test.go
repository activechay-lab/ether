package service

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/lru"
	"github.com/braedonsaunders/ether/internal/store"
)

// Row counts cannot detect a retention UPDATE issued for a nonexistent row.
// Run the real hashing worker, but leave the touch channel undrained so every
// attempted retention write is observable without timing a background UPDATE.
func TestUnpersistedArtworkNeverQueuesRetentionTouch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	db := store.OpenTest(t)
	wantDialect := store.SQLite
	if os.Getenv(store.TestPostgresEnv) != "" {
		wantDialect = store.Postgres
	}
	if got := store.DialectOf(db); got != wantDialect {
		t.Fatalf("database dialect = %s, want %s", got, wantDialect)
	}
	t.Logf("database dialect: %s", store.DialectOf(db))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 8, 6))); err != nil {
		t.Fatal(err)
	}
	q := &MetadataQueue{
		ctx: ctx, db: db,
		active: func(_ context.Context, id int64) (*core.User, error) { return &core.User{ID: id}, nil },
		read: func(context.Context, frontend.ArtworkRequest, frontend.Image, frontend.ArtworkTransform) ([]byte, string, error) {
			return encoded.Bytes(), "image/png", nil
		},
		jobs: make(chan artworkMetadataJob, 128), touches: make(chan string, 256),
		cache: map[string]artworkMetadata{}, pending: map[string]bool{}, retry: map[string]time.Time{},
		memory: lru.New(lru.Options[string, artworkMetadata]{MaxEntries: memoryMetadataEntries}),
	}
	q.PersistFor(func(_ context.Context, key core.RatingKey) bool { return key == core.MovieKey(44) })
	q.workers.Add(1)
	go q.run()
	t.Cleanup(func() { cancel(); q.Wait() })
	req := frontend.ArtworkRequest{Viewer: &core.User{ID: 7}, Item: core.MovieKey(43)}
	ref := frontend.Image{Kind: frontend.ImagePoster, Path: "generated", Tag: "v1"}
	q.Get(ctx, req, ref)
	wait, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	if err := q.WaitIdle(wait); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got := q.Get(ctx, req, ref)
		if got.BlurHash == "" || got.ThumbHash == "" || got.Width != 8 || got.Height != 6 {
			t.Fatalf("memory metadata unavailable on read %d: %+v", i, got)
		}
	}
	select {
	case key := <-q.touches:
		t.Fatalf("unpersisted artwork queued a used_at UPDATE for %s", key)
	default:
	}
	for _, table := range []string{"artwork_image_metadata", "artwork_image_metadata_claims"} {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s: count=%d err=%v", table, count, err)
		}
	}
	// Positive control: an old persisted row must still enqueue exactly one
	// retention refresh, rather than disabling retention to satisfy the test.
	persisted := req
	persisted.Item = core.MovieKey(44)
	key := artworkMetadataKey(persisted, ref)
	if _, err := db.ExecContext(ctx, `INSERT INTO artwork_image_metadata(image_key,width,height,blurhash,thumbhash,used_at) VALUES(?,?,?,?,?,?)`, key, 8, 6, "hash", "thumb", 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if got := q.Get(ctx, persisted, ref); got.ThumbHash != "thumb" {
			t.Fatalf("persisted metadata unavailable: %+v", got)
		}
	}
	if len(q.touches) != 1 {
		t.Fatalf("persisted row queued %d touches, want one", len(q.touches))
	}
	if got := <-q.touches; got != key {
		t.Fatalf("touched %s, want persisted key %s", got, key)
	}
	// After tier cleanup a search-only title can still have old metadata.
	// Neither loading that row nor hitting a formerly persisted cache entry
	// may enqueue retention writes for a title PersistFor now rejects.
	stale := ref
	stale.Tag = "pre-upgrade"
	staleKey := artworkMetadataKey(req, stale)
	if _, err := db.ExecContext(ctx, `INSERT INTO artwork_image_metadata(image_key,width,height,blurhash,thumbhash,used_at) VALUES(?,?,?,?,?,?)`, staleKey, 8, 6, "hash", "old-thumb", 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if got := q.Get(ctx, req, stale); got.ThumbHash != "old-thumb" {
			t.Fatalf("old metadata unavailable: %+v", got)
		}
	}
	select {
	case key := <-q.touches:
		t.Fatalf("search-only title with old metadata queued a used_at UPDATE for %s", key)
	default:
	}
	// The same case can arrive through q.cache on a long-running node.
	q.mu.Lock()
	q.memory.Remove(staleKey)
	q.cache[staleKey] = artworkMetadata{width: 8, height: 6, blurhash: "hash", thumbhash: "old-thumb", usedAt: 1}
	q.mu.Unlock()
	if got := q.Get(ctx, req, stale); got.ThumbHash != "old-thumb" {
		t.Fatalf("previously cached metadata unavailable: %+v", got)
	}
	select {
	case key := <-q.touches:
		t.Fatalf("search-only title with cached metadata queued a used_at UPDATE for %s", key)
	default:
	}
}
