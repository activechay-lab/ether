package pluginitems

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func openTestService(t *testing.T, opts Options) (*Service, *sql.DB) {
	t.Helper()
	db := store.OpenTest(t)
	s, err := New(context.Background(), db, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, db
}

func sampleBookDecl() plugins.MediaKindMeta {
	return plugins.MediaKindMeta{
		Key:     "book",
		Label:   "Books",
		Library: plugins.KindLibraryDecl{Name: "Books"},
		Levels: []plugins.KindLevel{
			{Key: "series", Label: "Series"},
			{Key: "book", Label: "Book", Playable: true, Container: true},
		},
		Fields: []plugins.KindField{
			{Key: "author", Type: "people", Label: "Author", Indexed: true},
			{Key: "format", Type: "enum", Label: "Format", Indexed: true, Enum: []string{"epub", "pdf", "cbz"}},
			{Key: "pages", Type: "int", Label: "Pages", Indexed: true},
			{Key: "tags", Type: "strings", Label: "Tags", Indexed: true},
			{Key: "duration", Type: "duration", Label: "Duration"},
		},
		Art: []plugins.KindArt{
			{Role: "spine", Label: "Spine"},
		},
		Browse: plugins.KindBrowseDecl{
			Sorts:        []string{"title", "added", "released", "year", "rating", "index"},
			Facets:       []string{"author", "format", "tags"},
			Search:       true,
			SearchFields: []string{"author", "tags"},
		},
		Sync: plugins.KindSyncDecl{
			Mode:    plugins.KindSyncPull,
			Refresh: "30m",
		},
	}
}

func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestPluginItemsSliceC runs the complete Slice C acceptance suite in a single
// top-level test so `go test -run TestPluginItemsSliceC ./internal/pluginitems`
// exercises every requirement in one focused invocation.
func TestPluginItemsSliceC(t *testing.T) {
	t.Run("KindInstallAndUpgrade", testKindInstallAndUpgrade)
	t.Run("ValidationAndLimits", testValidationAndLimits)
	t.Run("PluginKeyUniquenessAndNoReuse", testPluginKeyUniquenessAndNoReuse)
	t.Run("BrowseFacetsAndChildren", testBrowseFacetsAndChildren)
	t.Run("FTSScopedPerLibrary", testFTSScopedPerLibrary)
	t.Run("SyncWalkChangesEtagAndDeleteGuard", testSyncWalkChangesEtagAndDeleteGuard)
	t.Run("ProjectorMatchesAndResolutions", testProjectorMatchesAndResolutions)
	t.Run("KindBackendsAndPurge", testKindBackendsAndPurge)
	t.Run("KeysetBrowse250kEqualsNaiveReference", testKeysetBrowse250kEqualsNaiveReference)
}

func testKindInstallAndUpgrade(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})

	decl := sampleBookDecl()
	rec, err := s.UpsertKind(ctx, "community.books", 10, decl, false)
	if err != nil {
		t.Fatalf("UpsertKind initial: %v", err)
	}
	if rec.SchemaVersion != 1 || rec.LibraryID != 10 || rec.DeclHash == "" {
		t.Fatalf("initial KindRecord = %+v", rec)
	}

	// Idempotent re-upsert with identical decl keeps schema_version = 1.
	rec2, err := s.UpsertKind(ctx, "community.books", 10, decl, false)
	if err != nil || rec2.SchemaVersion != 1 {
		t.Fatalf("re-upsert identical: rec=%+v err=%v", rec2, err)
	}

	// Additive change (adding a field and an enum value) increments schema_version.
	declAdditive := sampleBookDecl()
	declAdditive.Fields[1].Enum = []string{"epub", "pdf", "cbz", "mobi"}
	declAdditive.Fields = append(declAdditive.Fields, plugins.KindField{
		Key: "publisher", Type: "string", Label: "Publisher", Indexed: true,
	})
	rec3, err := s.UpsertKind(ctx, "community.books", 10, declAdditive, false)
	if err != nil || rec3.SchemaVersion != 2 {
		t.Fatalf("additive upgrade: rec=%+v err=%v", rec3, err)
	}

	// Seed an item so we can verify replaceContents behavior on breaking upgrades.
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "series-1", Level: "series", Title: "Dune Chronicles"},
	}); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	// Breaking change (shrinking enum) without replaceContents must be rejected.
	declBreaking := sampleBookDecl()
	declBreaking.Fields[1].Enum = []string{"epub"}
	if _, err := s.UpsertKind(ctx, "community.books", 10, declBreaking, false); err == nil {
		t.Fatal("expected error on breaking upgrade without replaceContents")
	}

	// Breaking change WITH replaceContents=true purges items and succeeds.
	rec4, err := s.UpsertKind(ctx, "community.books", 10, declBreaking, true)
	if err != nil || rec4.SchemaVersion != 3 {
		t.Fatalf("breaking upgrade with replaceContents: rec=%+v err=%v", rec4, err)
	}
	tally, err := s.LibraryTally(ctx, 10)
	if err != nil || tally["series"] != 0 {
		t.Fatalf("expected purged tally after replaceContents, got %v (err=%v)", tally, err)
	}
}

func testValidationAndLimits(t *testing.T) {
	ctx := context.Background()
	var attentions []string
	s, _ := openTestService(t, Options{
		MaxPluginItems: 3,
		OnAttention: func(_ context.Context, pluginID, reason string) {
			attentions = append(attentions, pluginID+": "+reason)
		},
	})

	decl := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}

	// 1. Unknown field is dropped and flagged via OnAttention, while valid fields pass.
	n, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{
			ID:    "s1",
			Level: "series",
			Title: "The Foundation",
			Fields: map[string]json.RawMessage{
				"author":        rawJSON(t, []string{"Isaac Asimov"}),
				"unknown_field": rawJSON(t, "ignored"),
			},
			Art: []core.KindArtRef{
				{Role: "poster", URL: "https://example.com/poster.jpg"},
				{Role: "spine", URL: "https://example.com/spine.jpg"},
			},
		},
	})
	if err != nil || n != 1 {
		t.Fatalf("UpsertItems valid with unknown field: n=%d err=%v", n, err)
	}
	if len(attentions) == 0 || !strings.Contains(attentions[0], "dropped 1 undeclared field") {
		t.Fatalf("expected attention for dropped field, got %v", attentions)
	}
	stored, libID, err := s.GetByExternal(ctx, "community.books", "book", "s1")
	if err != nil || libID != 10 {
		t.Fatalf("GetByExternal: item=%+v lib=%d err=%v", stored, libID, err)
	}
	if stored.SortTitle != "foundation" {
		t.Fatalf("SortTitle = %q, want 'foundation'", stored.SortTitle)
	}
	if _, hasUnknown := stored.Fields["unknown_field"]; hasUnknown {
		t.Fatal("unknown_field should have been stripped")
	}

	// 2. Reject blob: URLs on catalog items (§3.3).
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{
			ID:    "s2",
			Level: "series",
			Title: "Bad Art",
			Art:   []core.KindArtRef{{Role: "poster", URL: "blob:sha256:deadbeef"}},
		},
	}); !errors.Is(err, ErrInvalidItem) {
		t.Fatalf("blob: art URL err = %v, want ErrInvalidItem", err)
	}

	// 3. Reject parent level mismatch (a 'book' requires a 'series' parent, not another 'book').
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "b1", Level: "book", ParentID: "s1", Title: "Foundation"},
		{ID: "b2", Level: "book", ParentID: "b1", Title: "Child of Book"},
	}); !errors.Is(err, ErrInvalidItem) {
		t.Fatalf("invalid parent level err = %v, want ErrInvalidItem", err)
	}

	// 4. Reject invalid enum value.
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{
			ID:       "b1",
			Level:    "book",
			ParentID: "s1",
			Title:    "Foundation",
			Fields:   map[string]json.RawMessage{"format": rawJSON(t, "docx")},
		},
	}); !errors.Is(err, ErrInvalidItem) {
		t.Fatalf("invalid enum err = %v, want ErrInvalidItem", err)
	}

	// 5. Enforce MaxPluginItems cap (3 items max in this test service).
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "b1", Level: "book", ParentID: "s1", Title: "Book 1"},
		{ID: "b2", Level: "book", ParentID: "s1", Title: "Book 2"},
	}); err != nil {
		t.Fatalf("upserting up to cap: %v", err)
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "b3", Level: "book", ParentID: "s1", Title: "Book 3 over cap"},
	}); !errors.Is(err, ErrMaxItemsExceeded) {
		t.Fatalf("over cap err = %v, want ErrMaxItemsExceeded", err)
	}
}

func testPluginKeyUniquenessAndNoReuse(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	decl := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "s1", Level: "series", Title: "Series One"},
		{ID: "s2", Level: "series", Title: "Series Two"},
	}); err != nil {
		t.Fatal(err)
	}
	it1, _, err := s.GetByExternal(ctx, "community.books", "book", "s1")
	if err != nil {
		t.Fatal(err)
	}
	it2, _, err := s.GetByExternal(ctx, "community.books", "book", "s2")
	if err != nil {
		t.Fatal(err)
	}
	if !it1.Key.IsPluginKey() || !it2.Key.IsPluginKey() || it1.Key == it2.Key {
		t.Fatalf("keys must be distinct plugin keys: %d, %d", it1.Key, it2.Key)
	}

	// Updating s1 preserves its RatingKey.
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "s1", Level: "series", Title: "Series One Renamed"},
	}); err != nil {
		t.Fatal(err)
	}
	it1AfterUpdate, _, err := s.GetByKey(ctx, it1.Key)
	if err != nil || it1AfterUpdate.Title != "Series One Renamed" {
		t.Fatalf("GetByKey after update: %+v, err=%v", it1AfterUpdate, err)
	}

	// Deleting s2 and re-inserting s2 (or inserting s3) NEVER reuses s2's sequence number (§2.2).
	oldKey2 := it2.Key
	if err := s.DeleteItems(ctx, "community.books", "book", []string{"s2"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetByKey(ctx, oldKey2); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetByKey(deleted) = %v, want ErrNotFound", err)
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "s2", Level: "series", Title: "Series Two Reborn"},
	}); err != nil {
		t.Fatal(err)
	}
	it2Reborn, _, err := s.GetByExternal(ctx, "community.books", "book", "s2")
	if err != nil {
		t.Fatal(err)
	}
	if it2Reborn.Key <= oldKey2 {
		t.Fatalf("re-inserted item reused or rewound key: got %d, old was %d", it2Reborn.Key, oldKey2)
	}

	// Purging the entire plugin and re-installing still never reuses sequence numbers.
	if err := s.PurgePlugin(ctx, "community.books"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{ID: "s1", Level: "series", Title: "Series One Fresh"},
	}); err != nil {
		t.Fatal(err)
	}
	it1Fresh, _, err := s.GetByExternal(ctx, "community.books", "book", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if it1Fresh.Key <= it2Reborn.Key {
		t.Fatalf("sequence reused after PurgePlugin: got %d <= %d", it1Fresh.Key, it2Reborn.Key)
	}
}

func testBrowseFacetsAndChildren(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	decl := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}

	items := []core.KindItem{
		{
			ID: "s1", Level: "series", Title: "The Expanse", Year: 2011, Rating: 9.2, Released: 1307000000,
			Fields: map[string]json.RawMessage{
				"author": rawJSON(t, []string{"James S. A. Corey"}),
				"tags":   rawJSON(t, []string{"scifi", "space"}),
			},
		},
		{
			ID: "s2", Level: "series", Title: "A Song of Ice and Fire", Year: 1996, Rating: 9.5, Released: 838860800,
			Fields: map[string]json.RawMessage{
				"author": rawJSON(t, []string{"George R. R. Martin"}),
				"tags":   rawJSON(t, []string{"fantasy"}),
			},
		},
		{
			ID: "b1", Level: "book", ParentID: "s1", Index: 1, Title: "Leviathan Wakes", Year: 2011, Rating: 9.0, Playable: true,
			Fields: map[string]json.RawMessage{
				"author": rawJSON(t, []string{"James S. A. Corey"}),
				"format": rawJSON(t, "epub"),
				"pages":  rawJSON(t, 592),
				"tags":   rawJSON(t, []string{"scifi", "space"}),
			},
		},
		{
			ID: "b2", Level: "book", ParentID: "s1", Index: 2, Title: "Caliban's War", Year: 2012, Rating: 8.8, Playable: true,
			Fields: map[string]json.RawMessage{
				"author": rawJSON(t, []string{"James S. A. Corey"}),
				"format": rawJSON(t, "pdf"),
				"pages":  rawJSON(t, 605),
				"tags":   rawJSON(t, []string{"scifi"}),
			},
		},
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", items); err != nil {
		t.Fatal(err)
	}

	// Root browse by title ASC ("The Expanse" -> "expanse", "A Song..." -> "song...").
	rootPage, err := s.Browse(ctx, BrowseQuery{LibraryID: 10, Sort: "title"})
	if err != nil {
		t.Fatal(err)
	}
	if rootPage.Total != 2 || len(rootPage.Items) != 2 || rootPage.Items[0].ID != "s1" || rootPage.Items[1].ID != "s2" {
		t.Fatalf("root browse = %+v", rootPage)
	}

	// Children of s1 ordered by index ASC.
	children, err := s.Children(ctx, core.NewItemRef("community.books", "book", "s1"), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if children.Total != 2 || len(children.Items) != 2 || children.Items[0].ID != "b1" || children.Items[1].ID != "b2" {
		t.Fatalf("children = %+v", children)
	}

	// Facet filtering on level="book".
	filtered, err := s.Browse(ctx, BrowseQuery{
		LibraryID: 10,
		Level:     "book",
		Facets:    map[string][]string{"format": {"epub"}, "tags": {"space"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || len(filtered.Items) != 1 || filtered.Items[0].ID != "b1" {
		t.Fatalf("filtered browse = %+v", filtered)
	}

	// Integer range facet filter on pages.
	rangeFiltered, err := s.Browse(ctx, BrowseQuery{
		LibraryID: 10,
		Level:     "book",
		Facets:    map[string][]string{"pages": {"600..700"}},
	})
	if err != nil || rangeFiltered.Total != 1 || rangeFiltered.Items[0].ID != "b2" {
		t.Fatalf("range filtered browse = %+v, err=%v", rangeFiltered, err)
	}

	// Facets aggregation.
	tagFacets, err := s.Facets(ctx, 10, "book", "tags", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tagFacets) != 2 || tagFacets[0].Value != "scifi" || tagFacets[0].Count != 2 {
		t.Fatalf("tagFacets = %+v", tagFacets)
	}

	// Deleting parent s1 cascades to children b1 and b2 and updates tallies.
	if err := s.DeleteItems(ctx, "community.books", "book", []string{"s1"}); err != nil {
		t.Fatal(err)
	}
	tally, err := s.LibraryTally(ctx, 10)
	if err != nil || tally["series"] != 1 || tally["book"] != 0 {
		t.Fatalf("tally after cascade delete = %v, err=%v", tally, err)
	}
}

func testFTSScopedPerLibrary(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})

	decl1 := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl1, false); err != nil {
		t.Fatal(err)
	}
	decl2 := sampleBookDecl()
	decl2.Key = "audiobook"
	if _, err := s.UpsertKind(ctx, "community.audio", 20, decl2, false); err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpsertItems(ctx, "community.books", "book", []core.KindItem{
		{
			ID: "s1", Level: "series", Title: "Hyperion Cantos",
			Fields: map[string]json.RawMessage{"author": rawJSON(t, []string{"Dan Simmons"})},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertItems(ctx, "community.audio", "audiobook", []core.KindItem{
		{
			ID: "a1", Level: "series", Title: "Hyperion Unabridged Audio",
			Fields: map[string]json.RawMessage{"author": rawJSON(t, []string{"Dan Simmons"})},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Search in library 10 only returns library 10 items.
	res10, err := s.Search(ctx, SearchQuery{LibraryID: 10, Query: "Simmons Hyperion"})
	if err != nil {
		t.Fatal(err)
	}
	if res10.Total != 1 || len(res10.Items) != 1 || res10.Items[0].ID != "s1" {
		t.Fatalf("Search library 10 = %+v", res10)
	}

	// Search in library 20 only returns library 20 items.
	res20, err := s.Search(ctx, SearchQuery{LibraryID: 20, Query: "Hyperion"})
	if err != nil {
		t.Fatal(err)
	}
	if res20.Total != 1 || len(res20.Items) != 1 || res20.Items[0].ID != "a1" {
		t.Fatalf("Search library 20 = %+v", res20)
	}

	// Deleting s1 removes it from FTS immediately.
	if err := s.DeleteItems(ctx, "community.books", "book", []string{"s1"}); err != nil {
		t.Fatal(err)
	}
	resAfterDel, err := s.Search(ctx, SearchQuery{LibraryID: 10, Query: "Hyperion"})
	if err != nil || resAfterDel.Total != 0 {
		t.Fatalf("Search after delete = %+v, err=%v", resAfterDel, err)
	}
}

type fakeSyncCaller struct {
	mu           sync.Mutex
	rootPages    map[string]core.KindPage
	childPages   map[string]core.KindPage
	changesPages map[string]core.KindChangePage
	changesSupported bool
	browseCalls  int
}

func (f *fakeSyncCaller) Call(_ context.Context, _ string, req plugins.Request) (plugins.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if strings.HasSuffix(req.Path, "/browse") {
		f.browseCalls++
		var br core.KindBrowseRequest
		_ = json.Unmarshal(req.Body, &br)
		var page core.KindPage
		if br.ParentID == "" {
			page = f.rootPages[br.Cursor]
		} else {
			page = f.childPages[br.ParentID+"|"+br.Cursor]
		}
		b, _ := json.Marshal(page)
		return plugins.Response{Status: http.StatusOK, Body: b}, nil
	}
	if strings.HasSuffix(req.Path, "/changes") {
		if !f.changesSupported {
			return plugins.Response{Status: http.StatusNotFound}, nil
		}
		var cr struct {
			Cursor string `json:"cursor"`
		}
		_ = json.Unmarshal(req.Body, &cr)
		page, ok := f.changesPages[cr.Cursor]
		if !ok {
			page = core.KindChangePage{Complete: true, NextCursor: "tok-1"}
		}
		b, _ := json.Marshal(page)
		return plugins.Response{Status: http.StatusOK, Body: b}, nil
	}
	return plugins.Response{Status: http.StatusNotFound}, nil
}

func testSyncWalkChangesEtagAndDeleteGuard(t *testing.T) {
	ctx := context.Background()
	fc := &fakeSyncCaller{
		changesSupported: true,
		rootPages: map[string]core.KindPage{
			"": {
				Items: []core.KindItem{
					{ID: "s1", Level: "series", Title: "Series 1", ETag: "v1"},
					{ID: "s2", Level: "series", Title: "Series 2", ETag: "v1"},
				},
				NextCursor: "p2",
			},
			"p2": {
				Items: []core.KindItem{
					{ID: "s3", Level: "series", Title: "Series 3", ETag: "v1"},
					{ID: "s4", Level: "series", Title: "Series 4", ETag: "v1"},
				},
			},
		},
		childPages: map[string]core.KindPage{
			"s1|": {Items: []core.KindItem{{ID: "b1", Level: "book", ParentID: "s1", Title: "Book 1", ETag: "v1", Playable: true}}},
			"s2|": {Items: []core.KindItem{{ID: "b2", Level: "book", ParentID: "s2", Title: "Book 2", ETag: "v1", Playable: true}}},
		},
		changesPages: map[string]core.KindChangePage{
			"": {NextCursor: "tok-1", Complete: true},
			"tok-1": {
				Upserts:    []core.KindItem{{ID: "s1", Level: "series", Title: "Series 1 Updated", ETag: "v2"}},
				Deletes:    []string{"s4"},
				NextCursor: "tok-2",
				Complete:   false,
			},
			"tok-2": {
				Upserts:    []core.KindItem{{ID: "s5", Level: "series", Title: "Series 5", ETag: "v1"}},
				NextCursor: "tok-3",
				Complete:   true,
			},
		},
	}

	s, _ := openTestService(t, Options{Caller: fc})
	decl := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}

	// 1. Initial sync walks root pages + child pages and records baseline cursor "tok-1".
	res1, err := s.SyncKind(ctx, "community.books", "book", SyncOptions{})
	if err != nil {
		t.Fatalf("Initial SyncKind: %v", err)
	}
	if res1.Upserted != 6 || res1.NextCursor != "tok-1" {
		t.Fatalf("initial SyncResult = %+v, want Upserted=6 NextCursor=tok-1", res1)
	}

	// 2. Delta sync chains tok-1 -> tok-2 -> tok-3, upserting 2 items and deleting 1.
	res2, err := s.SyncKind(ctx, "community.books", "book", SyncOptions{})
	if err != nil {
		t.Fatalf("Delta SyncKind: %v", err)
	}
	if res2.Upserted != 2 || res2.Deleted != 1 || res2.NextCursor != "tok-3" {
		t.Fatalf("delta SyncResult = %+v", res2)
	}

	// 3. 50%-delete guard on /changes when >50% of stored items would be deleted.
	fc.mu.Lock()
	fc.changesPages["tok-3"] = core.KindChangePage{
		Deletes:    []string{"s1", "s2", "s3", "s5"},
		NextCursor: "tok-4",
		Complete:   true,
	}
	fc.mu.Unlock()
	if _, err := s.SyncKind(ctx, "community.books", "book", SyncOptions{}); !errors.Is(err, ErrDeleteGuard) {
		t.Fatalf("expected ErrDeleteGuard on >50%% delete, got %v", err)
	}
	st, err := s.SyncStateOf(ctx, "community.books", "book")
	if err != nil || !strings.Contains(st.LastError, "50% delete guard") {
		t.Fatalf("SyncState after delete guard = %+v, err=%v", st, err)
	}

	// 4. Fallback re-walk diff by ETag and 2-consecutive-walks stale deletion.
	fc.mu.Lock()
	fc.changesSupported = false
	// Walk returns s1 (etag v2 unchanged), s2 (etag v2 changed), s3 (etag v1 unchanged), b1, b2; s5 is missing!
	fc.rootPages = map[string]core.KindPage{
		"": {
			Items: []core.KindItem{
				{ID: "s1", Level: "series", Title: "Series 1 Updated", ETag: "v2"},
				{ID: "s2", Level: "series", Title: "Series 2 v2", ETag: "v2"},
				{ID: "s3", Level: "series", Title: "Series 3", ETag: "v1"},
			},
		},
	}
	fc.mu.Unlock()

	// First walk missing s5: increments missed_walks to 1, does NOT delete s5 yet.
	resWalk1, err := s.SyncKind(ctx, "community.books", "book", SyncOptions{FullWalk: true})
	if err != nil {
		t.Fatalf("Rewalk 1: %v", err)
	}
	if resWalk1.Deleted != 0 || resWalk1.Skipped != 4 || resWalk1.Upserted != 1 {
		t.Fatalf("Rewalk 1 result = %+v (want Upserted=1 Skipped=4 Deleted=0)", resWalk1)
	}
	if _, _, err := s.GetByExternal(ctx, "community.books", "book", "s5"); err != nil {
		t.Fatalf("s5 should still exist after 1 missed walk: %v", err)
	}

	// Second consecutive walk missing s5: missed_walks hits 2 -> s5 is deleted!
	resWalk2, err := s.SyncKind(ctx, "community.books", "book", SyncOptions{FullWalk: true})
	if err != nil {
		t.Fatalf("Rewalk 2: %v", err)
	}
	if resWalk2.Deleted != 1 {
		t.Fatalf("Rewalk 2 result = %+v, want Deleted=1", resWalk2)
	}
	if _, _, err := s.GetByExternal(ctx, "community.books", "book", "s5"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("s5 should be deleted after 2 missed walks, got err=%v", err)
	}
}

func testProjectorMatchesAndResolutions(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	s, _ := openTestService(t, Options{Now: func() time.Time { return now }})
	decl := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}

	item := core.KindItem{
		Ref:      core.NewItemRef("community.books", "book", "b1"),
		Key:      core.PluginKey(42),
		ID:       "b1",
		Level:    "book",
		ParentID: "s1",
		Index:    3,
		Title:    "The Winds of Winter",
		Summary:  "Sixth novel.",
		Year:     2026,
		Released: 1767225600,
		Rating:   9.1,
		Playable: true,
		Art: []core.KindArtRef{
			{Role: "poster", URL: "https://example.com/p.jpg"},
			{Role: "thumb", URL: "https://example.com/t.jpg"},
		},
		Fields: map[string]json.RawMessage{
			"author":   rawJSON(t, []string{"George R. R. Martin"}),
			"format":   rawJSON(t, "epub"),
			"duration": rawJSON(t, 3600),
			"tags":     rawJSON(t, []string{"fantasy", "epic"}),
		},
	}

	title := Project(item, decl, core.Presentations{})
	if title.Key != core.PluginKey(42) || title.Type != core.TypeEpisode || title.Season != 1 || title.Episode != 3 {
		t.Fatalf("Project hierarchy/type = %+v", title)
	}
	if title.SortTitle != "winds of winter" || title.Runtime != time.Hour {
		t.Fatalf("Project sortTitle/runtime = %q / %v", title.SortTitle, title.Runtime)
	}
	if !strings.Contains(title.Summary, "Sixth novel.") || !strings.Contains(title.Summary, "Author: George R. R. Martin") {
		t.Fatalf("Project summary = %q", title.Summary)
	}
	if title.PosterPath != "https://example.com/p.jpg" || title.StillPath != "https://example.com/t.jpg" {
		t.Fatalf("Project art = poster:%q still:%q", title.PosterPath, title.StillPath)
	}

	// Matches: auto save + manual pin preservation (§7.2).
	ref := item.Ref
	if err := s.SaveMatches(ctx, ref, "matcher.plugin", []core.KindMatchScore{
		{CandidateRef: "cand-low", Score: 0.50, Reasons: []string{"partial title"}},
		{CandidateRef: "cand-high", Score: 0.92, Reasons: []string{"exact isbn"}},
	}, 0.85); err != nil {
		t.Fatal(err)
	}
	best, ok, err := s.BestMatch(ctx, ref, 0.85)
	if err != nil || !ok || best.CandidateRef != "cand-high" || !best.Chosen {
		t.Fatalf("BestMatch auto = %+v, ok=%v, err=%v", best, ok, err)
	}

	// Pinning cand-low overrides cand-high and survives subsequent auto SaveMatches.
	if err := s.PinMatch(ctx, ref, "cand-low", []string{"owner pinned"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMatches(ctx, ref, "matcher.plugin", []core.KindMatchScore{
		{CandidateRef: "cand-low", Score: 0.10},
		{CandidateRef: "cand-high", Score: 0.99},
	}, 0.85); err != nil {
		t.Fatal(err)
	}
	bestPinned, ok, err := s.BestMatch(ctx, ref, 0.85)
	if err != nil || !ok || bestPinned.CandidateRef != "cand-low" || bestPinned.Source != "manual" || bestPinned.Score != 1.0 {
		t.Fatalf("BestMatch after pin = %+v, ok=%v, err=%v", bestPinned, ok, err)
	}

	// Resolutions: positive TTL clamped to 15m, negative TTL 60s, invalidation.
	src := core.PlayableSource{
		ID:        "src-1",
		Pattern:   "direct",
		MIME:      "application/epub+zip",
		URL:       "https://example.com/book.epub",
		ExpiresAt: now.Add(2 * time.Hour).Unix(),
	}
	if err := s.SaveResolution(ctx, ref, src); err != nil {
		t.Fatal(err)
	}
	gotSrc, hit, err := s.GetResolution(ctx, ref)
	if err != nil || !hit || gotSrc.URL != src.URL {
		t.Fatalf("GetResolution positive = %+v, hit=%v, err=%v", gotSrc, hit, err)
	}
	// Advance clock by 16m -> positive cache (clamped to 15m) expires.
	now = now.Add(16 * time.Minute)
	if _, hit, err := s.GetResolution(ctx, ref); err != nil || hit {
		t.Fatalf("expected expired resolution after 16m, got hit=%v err=%v", hit, err)
	}

	// Negative resolution cache returns core.ErrNotAvailable for 60s.
	if err := s.SaveNegativeResolution(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := s.GetResolution(ctx, ref); !hit || !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("negative resolution: hit=%v err=%v", hit, err)
	}
	if err := s.InvalidateResolution(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := s.GetResolution(ctx, ref); err != nil || hit {
		t.Fatalf("after InvalidateResolution: hit=%v err=%v", hit, err)
	}
}

func testKindBackendsAndPurge(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	decl := sampleBookDecl()
	decl.Sync.Mode = plugins.KindSyncPush
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}

	kb := s.KindBackends()
	gen0 := s.Generation()
	accepted, err := kb.KindUpsert(ctx, "community.books", "book", []core.KindItem{
		{ID: "s1", Level: "series", Title: "Series 1"},
		{ID: "b1", Level: "book", ParentID: "s1", Title: "Book 1", Playable: true},
	})
	if err != nil || accepted != 2 {
		t.Fatalf("KindUpsert: accepted=%d err=%v", accepted, err)
	}
	if s.Generation() <= gen0 {
		t.Fatal("expected Generation() to advance after KindUpsert")
	}

	page, err := kb.KindItems(ctx, "community.books", "book", "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" || page.Total == nil || *page.Total != 2 {
		t.Fatalf("KindItems page 1 = %+v, err=%v", page, err)
	}
	page2, err := kb.KindItems(ctx, "community.books", "book", page.NextCursor, 10)
	if err != nil || len(page2.Items) != 1 || page2.NextCursor != "" {
		t.Fatalf("KindItems page 2 = %+v, err=%v", page2, err)
	}

	// Verify state_watch cleanup during PurgePlugin (§2.5).
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS state_watch (user_id INTEGER NOT NULL, rating_key INTEGER NOT NULL, PRIMARY KEY (user_id, rating_key))`); err != nil {
		t.Fatal(err)
	}
	b1Item, _, err := s.GetByExternal(ctx, "community.books", "book", "b1")
	if err != nil {
		t.Fatal(err)
	}
	movieKey := core.MovieKey(550)
	if _, err := db.ExecContext(ctx, `INSERT INTO state_watch(user_id, rating_key) VALUES (1, ?), (1, ?)`, int64(b1Item.Key), int64(movieKey)); err != nil {
		t.Fatal(err)
	}

	if err := s.PurgePlugin(ctx, "community.books"); err != nil {
		t.Fatal(err)
	}
	var watchRemaining int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_watch WHERE rating_key = ?`, int64(b1Item.Key)).Scan(&watchRemaining); err != nil || watchRemaining != 0 {
		t.Fatalf("plugin watch state not purged: remaining=%d err=%v", watchRemaining, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_watch WHERE rating_key = ?`, int64(movieKey)).Scan(&watchRemaining); err != nil || watchRemaining != 1 {
		t.Fatalf("movie watch state was touched: remaining=%d err=%v", watchRemaining, err)
	}
}

// testKeysetBrowse250kEqualsNaiveReference seeds 250,000 items into a plugin
// library and verifies that keyset pagination across pages produces the exact
// same item sequence as a naive in-memory sort reference (§12 row C).
func testKeysetBrowse250kEqualsNaiveReference(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	decl := plugins.MediaKindMeta{
		Key:     "photo",
		Label:   "Photos",
		Library: plugins.KindLibraryDecl{Name: "Photos"},
		Levels:  []plugins.KindLevel{{Key: "photo", Label: "Photo", Playable: true}},
		Browse:  plugins.KindBrowseDecl{Sorts: []string{"title", "year"}},
		Sync:    plugins.KindSyncDecl{Mode: plugins.KindSyncPull},
	}
	if _, err := s.UpsertKind(ctx, "community.photos", 30, decl, false); err != nil {
		t.Fatal(err)
	}

	const totalItems = 250_000
	type refEntry struct {
		id        string
		ref       string
		sortTitle string
		year      int
	}
	expected := make([]refEntry, totalItems)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO plugin_items(
	item_ref, seq, plugin_id, kind_key, library_id, external_id, parent_ref, level, idx,
	title, sort_title, year, added_at, updated_at, seen_at
) VALUES (?, ?, 'community.photos', 'photo', 30, ?, '', 'photo', ?, ?, ?, ?, 1700000000, 1700000000, 1700000000)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()

	// Seed 250k items with deliberate duplicate sort_titles so the item_ref tie-breaker is exercised.
	for i := 0; i < totalItems; i++ {
		seq := int64(i + 1)
		extID := fmt.Sprintf("img-%06d", i)
		ref := "pk:community.photos:photo:" + extID
		bucket := (i * 7919) % 25000 // 10x collision rate on sort_title
		title := fmt.Sprintf("Photo Group %05d", bucket)
		sortTitle := fmt.Sprintf("photo group %05d", bucket)
		year := 1970 + (i % 55)

		if _, err := stmt.ExecContext(ctx, ref, seq, extID, i, title, sortTitle, year); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
		expected[i] = refEntry{id: extID, ref: ref, sortTitle: sortTitle, year: year}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_items_seq SET next = ? WHERE id = 1`, totalItems+1); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO plugin_item_tally(library_id, level, n) VALUES (30, 'photo', ?)`, totalItems); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Naive in-memory reference sort: (sort_title ASC, item_ref ASC).
	slices.SortFunc(expected, func(a, b refEntry) int {
		if c := strings.Compare(a.sortTitle, b.sortTitle); c != 0 {
			return c
		}
		return strings.Compare(a.ref, b.ref)
	})

	// Walk the first 5,000 items (5 pages of 1,000) via keyset pagination and verify exact match,
	// then jump to a cursor near the middle and near the end of the 250k dataset.
	cursor := ""
	offset := 0
	for pageNum := 0; pageNum < 5; pageNum++ {
		page, err := s.Browse(ctx, BrowseQuery{
			LibraryID: 30,
			Level:     "photo",
			Sort:      "title",
			Cursor:    cursor,
			Limit:     1000,
		})
		if err != nil {
			t.Fatalf("Browse page %d: %v", pageNum, err)
		}
		if page.Total != totalItems {
			t.Fatalf("page.Total = %d, want %d", page.Total, totalItems)
		}
		if len(page.Items) != 1000 {
			t.Fatalf("len(page.Items) = %d, want 1000", len(page.Items))
		}
		for i, got := range page.Items {
			want := expected[offset+i]
			if got.ID != want.id || string(got.Ref) != want.ref {
				t.Fatalf("mismatch at index %d: got %s (%s), want %s (%s)", offset+i, got.ID, got.Ref, want.id, want.ref)
			}
		}
		offset += len(page.Items)
		cursor = page.NextCursor
	}

	// Also verify cursor seek at index 125,000 and 249,500 (to the very end of the 250k table).
	for _, probeIdx := range []int{125_000, 249_500} {
		anchor := expected[probeIdx-1]
		cur := encodeBrowseCursor(browseCursor{
			Sort:    "sort_title",
			Desc:    false,
			StrVal:  anchor.sortTitle,
			ItemRef: anchor.ref,
		})
		page, err := s.Browse(ctx, BrowseQuery{
			LibraryID: 30,
			Level:     "photo",
			Sort:      "title",
			Cursor:    cur,
			Limit:     500,
		})
		if err != nil {
			t.Fatalf("Browse at %d: %v", probeIdx, err)
		}
		if len(page.Items) != 500 {
			t.Fatalf("at %d: len(page.Items) = %d, want 500", probeIdx, len(page.Items))
		}
		for i, got := range page.Items {
			want := expected[probeIdx+i]
			if got.ID != want.id {
				t.Fatalf("mismatch at %d+%d: got %s, want %s", probeIdx, i, got.ID, want.id)
			}
		}
		if probeIdx == 249_500 && page.NextCursor != "" {
			t.Fatalf("expected empty NextCursor at end of 250k items, got %q", page.NextCursor)
		}
	}
}
