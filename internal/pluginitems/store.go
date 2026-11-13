package pluginitems

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

// KindRecord is one installed (plugin, kind) row in plugin_kinds (spec §2.3).
type KindRecord struct {
	PluginID      string
	KindKey       string
	LibraryID     core.LibraryID
	Decl          plugins.MediaKindMeta
	DeclHash      string
	SchemaVersion int64
}

// SyncState is the persisted sync status for one (plugin, kind) (spec §2.3).
type SyncState struct {
	PluginID    string    `json:"pluginId"`
	KindKey     string    `json:"kindKey"`
	Cursor      string    `json:"cursor,omitempty"`
	Generation  uint64    `json:"generation"`
	LastStarted time.Time `json:"lastStarted,omitzero"`
	LastOK      time.Time `json:"lastOk,omitzero"`
	LastError   string    `json:"lastError,omitempty"`
	Items       int64     `json:"items"`
}

const itemCols = `item_ref, seq, plugin_id, kind_key, library_id, external_id, parent_ref, level, idx, title, sort_title, summary, year, released, rating, playable, child_count, leaf_count, hints, fields, etag, unavailable`

func prefixedItemCols(prefix string) string {
	parts := strings.Split(itemCols, ", ")
	for i, p := range parts {
		parts[i] = prefix + p
	}
	return strings.Join(parts, ", ")
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanItemRow(sc rowScanner) (core.KindItem, core.LibraryID, int64, error) {
	var (
		it             core.KindItem
		refStr         string
		seq            int64
		pluginID       string
		kindKey        string
		libID          int64
		parentRef      string
		idx            int64
		year           int64
		playable       int
		childCount     int64
		leafCount      int64
		hintsRaw       string
		fieldsRaw      string
		unavailableRaw string
	)
	if err := sc.Scan(
		&refStr, &seq, &pluginID, &kindKey, &libID, &it.ID, &parentRef, &it.Level, &idx,
		&it.Title, &it.SortTitle, &it.Summary, &year, &it.Released, &it.Rating,
		&playable, &childCount, &leafCount, &hintsRaw, &fieldsRaw, &it.ETag, &unavailableRaw,
	); err != nil {
		return core.KindItem{}, 0, 0, err
	}
	it.Ref = core.ItemRef(refStr)
	it.Key = core.PluginKey(seq)
	if parentRef != "" {
		it.ParentID = core.ItemRef(parentRef).External()
	}
	it.Index = int(idx)
	it.Year = int(year)
	if unavailableRaw != "" {
		if err := json.Unmarshal([]byte(unavailableRaw), &it.Unavailable); err != nil {
			return core.KindItem{}, 0, 0, err
		}
	}
	it.Playable = playable != 0 && it.Unavailable == nil
	it.ChildCount = int(childCount)
	it.LeafCount = int(leafCount)
	if hintsRaw != "" && hintsRaw != "{}" {
		it.Hints = json.RawMessage(hintsRaw)
	}
	if fieldsRaw != "" && fieldsRaw != "{}" {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(fieldsRaw), &m); err == nil && len(m) > 0 {
			it.Fields = m
		}
	}
	return it, core.LibraryID(libID), seq, nil
}

func (s *Service) queryItems(ctx context.Context, query string, args []any, hydrateArt bool) ([]core.KindItem, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []core.KindItem
	for rows.Next() {
		it, _, _, err := scanItemRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if hydrateArt && len(items) > 0 {
		if err := s.hydrateArtSlice(ctx, items); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Service) hydrateArtSlice(ctx context.Context, items []core.KindItem) error {
	if len(items) == 0 {
		return nil
	}
	byRef := make(map[core.ItemRef][]int, len(items))
	placeholders := make([]string, 0, len(items))
	args := make([]any, 0, len(items))
	for i := range items {
		ref := items[i].Ref
		if _, seen := byRef[ref]; !seen {
			placeholders = append(placeholders, "?")
			args = append(args, string(ref))
		}
		byRef[ref] = append(byRef[ref], i)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT item_ref, role, url, width, height FROM plugin_item_art WHERE item_ref IN (`+strings.Join(placeholders, ",")+`) ORDER BY item_ref, role`,
		args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var refStr, role, url string
		var w, h int64
		if err := rows.Scan(&refStr, &role, &url, &w, &h); err != nil {
			return err
		}
		art := core.KindArtRef{Role: role, URL: url, Width: int(w), Height: int(h)}
		for _, idx := range byRef[core.ItemRef(refStr)] {
			items[idx].Art = append(items[idx].Art, art)
		}
	}
	return rows.Err()
}

// UpsertKind installs or upgrades one (plugin, kind) declaration in
// plugin_kinds (spec §2.3, §2.5). On upgrade, decl_hash is compared against
// the stored declaration: additive changes bump schema_version; non-additive
// changes are refused with a ValidationError unless replaceContents is true,
// in which case existing items for that kind are purged and sync state reset.
func (s *Service) UpsertKind(ctx context.Context, pluginID string, libraryID core.LibraryID, decl plugins.MediaKindMeta, replaceContents bool) (KindRecord, error) {
	if strings.TrimSpace(pluginID) == "" || strings.TrimSpace(decl.Key) == "" || libraryID <= 0 {
		return KindRecord{}, errors.New("pluginitems: pluginID, kindKey and libraryID are required")
	}
	mu := s.kindLock(pluginID, decl.Key)
	mu.Lock()
	defer mu.Unlock()

	declJSON, err := json.Marshal(decl)
	if err != nil {
		return KindRecord{}, err
	}
	declHash := decl.DeclHash()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KindRecord{}, err
	}
	defer tx.Rollback()

	var (
		oldDeclJSON string
		oldHash     string
		schemaVer   int64
	)
	err = tx.QueryRowContext(ctx,
		`SELECT decl, decl_hash, schema_version FROM plugin_kinds WHERE plugin_id = ? AND kind_key = ?`,
		pluginID, decl.Key).Scan(&oldDeclJSON, &oldHash, &schemaVer)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		schemaVer = 1
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO plugin_kinds(plugin_id, kind_key, library_id, decl, decl_hash, schema_version) VALUES(?, ?, ?, ?, ?, ?)`,
			pluginID, decl.Key, int64(libraryID), string(declJSON), declHash, schemaVer); err != nil {
			return KindRecord{}, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO plugin_sync_state(plugin_id, kind_key) VALUES(?, ?) ON CONFLICT(plugin_id, kind_key) DO NOTHING`,
			pluginID, decl.Key); err != nil {
			return KindRecord{}, err
		}
	case err != nil:
		return KindRecord{}, err
	default:
		if oldHash != declHash {
			var oldDecl plugins.MediaKindMeta
			if err := json.Unmarshal([]byte(oldDeclJSON), &oldDecl); err != nil {
				return KindRecord{}, err
			}
			if upgErr := plugins.ValidateMediaKindsUpgrade([]plugins.MediaKindMeta{oldDecl}, []plugins.MediaKindMeta{decl}); upgErr != nil {
				if !replaceContents {
					return KindRecord{}, upgErr
				}
				if err := s.purgeKindTx(ctx, tx, pluginID, decl.Key, libraryID, false); err != nil {
					return KindRecord{}, err
				}
			}
			schemaVer++
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE plugin_kinds SET library_id = ?, decl = ?, decl_hash = ?, schema_version = ? WHERE plugin_id = ? AND kind_key = ?`,
			int64(libraryID), string(declJSON), declHash, schemaVer, pluginID, decl.Key); err != nil {
			return KindRecord{}, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO plugin_sync_state(plugin_id, kind_key) VALUES(?, ?) ON CONFLICT(plugin_id, kind_key) DO NOTHING`,
			pluginID, decl.Key); err != nil {
			return KindRecord{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return KindRecord{}, err
	}
	return KindRecord{
		PluginID:      pluginID,
		KindKey:       decl.Key,
		LibraryID:     libraryID,
		Decl:          decl,
		DeclHash:      declHash,
		SchemaVersion: schemaVer,
	}, nil
}

// GetKind returns the installed KindRecord for (pluginID, kindKey).
func (s *Service) GetKind(ctx context.Context, pluginID, kindKey string) (KindRecord, error) {
	return s.scanKind(s.db.QueryRowContext(ctx,
		`SELECT plugin_id, kind_key, library_id, decl, decl_hash, schema_version FROM plugin_kinds WHERE plugin_id = ? AND kind_key = ?`,
		pluginID, kindKey))
}

// GetKindByLibrary returns the installed KindRecord for libraryID.
func (s *Service) GetKindByLibrary(ctx context.Context, libraryID core.LibraryID) (KindRecord, error) {
	return s.scanKind(s.db.QueryRowContext(ctx,
		`SELECT plugin_id, kind_key, library_id, decl, decl_hash, schema_version FROM plugin_kinds WHERE library_id = ?`,
		int64(libraryID)))
}

// ListKinds returns all installed kinds for pluginID (or all plugins when pluginID == "").
func (s *Service) ListKinds(ctx context.Context, pluginID string) ([]KindRecord, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if pluginID == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT plugin_id, kind_key, library_id, decl, decl_hash, schema_version FROM plugin_kinds ORDER BY plugin_id, kind_key`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT plugin_id, kind_key, library_id, decl, decl_hash, schema_version FROM plugin_kinds WHERE plugin_id = ? ORDER BY kind_key`,
			pluginID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KindRecord
	for rows.Next() {
		rec, err := s.scanKind(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Service) scanKind(sc rowScanner) (KindRecord, error) {
	var (
		rec      KindRecord
		libID    int64
		declJSON string
	)
	if err := sc.Scan(&rec.PluginID, &rec.KindKey, &libID, &declJSON, &rec.DeclHash, &rec.SchemaVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return KindRecord{}, core.ErrNotFound
		}
		return KindRecord{}, err
	}
	rec.LibraryID = core.LibraryID(libID)
	if err := json.Unmarshal([]byte(declJSON), &rec.Decl); err != nil {
		return KindRecord{}, err
	}
	return rec, nil
}

// KindUpsert implements the store side of plugins.KindBackends.KindUpsert
// (spec §3.6): validates and upserts items into the kind's library, updates
// tallies and FTS, and bumps the sync generation.
func (s *Service) KindUpsert(ctx context.Context, pluginID, kindKey string, items []core.KindItem) (int, error) {
	return s.UpsertItems(ctx, pluginID, kindKey, items)
}

// UpsertItems validates and writes a batch of KindItems for (pluginID, kindKey)
// in one transaction, maintaining tallies, typed EAV fields, art and FTS.
func (s *Service) UpsertItems(ctx context.Context, pluginID, kindKey string, items []core.KindItem) (int, error) {
	mu := s.kindLock(pluginID, kindKey)
	mu.Lock()
	defer mu.Unlock()
	return s.upsertItemsLocked(ctx, pluginID, kindKey, items, s.now().Unix(), true)
}

func (s *Service) upsertItemsLocked(ctx context.Context, pluginID, kindKey string, items []core.KindItem, seenAt int64, bumpGen bool) (int, error) {
	kind, err := s.GetKind(ctx, pluginID, kindKey)
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		return 0, nil
	}

	// Track levels in the incoming batch so parent levels within the same
	// batch are verified even before DB insertion.
	batchLevels := make(map[string]string, len(items))
	for _, it := range items {
		if it.ID != "" && it.Level != "" {
			batchLevels[it.ID] = it.Level
		}
	}

	// Look up any referenced parents already in the database.
	parentIDs := make([]string, 0)
	seenParents := map[string]bool{}
	for _, it := range items {
		if it.ParentID != "" && !seenParents[it.ParentID] {
			if _, inBatch := batchLevels[it.ParentID]; !inBatch {
				seenParents[it.ParentID] = true
				parentIDs = append(parentIDs, it.ParentID)
			}
		}
	}
	dbParentLevels, err := s.lookupLevelsByExternalIDs(ctx, pluginID, kindKey, parentIDs)
	if err != nil {
		return 0, err
	}

	// Validate every item and deduplicate by ID (last occurrence in batch wins).
	validated := make([]ValidatedItem, 0, len(items))
	posByID := make(map[string]int, len(items))
	totalDroppedFields := 0
	for _, it := range items {
		parentLevel := ""
		if it.ParentID != "" {
			if lv, ok := batchLevels[it.ParentID]; ok {
				parentLevel = lv
			} else if lv, ok := dbParentLevels[it.ParentID]; ok {
				parentLevel = lv
			}
		}
		v, err := ValidateItem(kind.Decl, it, parentLevel)
		if err != nil {
			return 0, err
		}
		v.Item.Ref = core.NewItemRef(pluginID, kindKey, v.Item.ID)
		totalDroppedFields += v.DroppedFields
		if idx, dup := posByID[v.Item.ID]; dup {
			validated[idx] = v
		} else {
			posByID[v.Item.ID] = len(validated)
			validated = append(validated, v)
		}
	}
	if totalDroppedFields > 0 {
		s.noteAttention(ctx, pluginID, fmt.Sprintf("dropped %d undeclared field(s) on kind %s", totalDroppedFields, kindKey))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	existing, err := lookupExistingItemsTx(ctx, tx, validated)
	if err != nil {
		return 0, err
	}

	newCount := 0
	for _, v := range validated {
		if _, ok := existing[v.Item.Ref]; !ok {
			newCount++
		}
	}
	if newCount > 0 {
		var pluginTotal int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_items WHERE plugin_id = ?`, pluginID).Scan(&pluginTotal); err != nil {
			return 0, err
		}
		if pluginTotal+int64(newCount) > int64(s.maxPluginItems) {
			s.noteAttention(ctx, pluginID, fmt.Sprintf("plugin exceeded max item cap (%d)", s.maxPluginItems))
			return 0, ErrMaxItemsExceeded
		}
	}

	var nextSeq int64
	if newCount > 0 {
		nextSeq, err = allocateSeqRangeTx(ctx, tx, int64(newCount))
		if err != nil {
			return 0, err
		}
	}

	nowUnix := s.now().Unix()
	if seenAt <= 0 {
		seenAt = nowUnix
	}
	isPG := store.DialectOf(s.db) == store.Postgres
	tallyDelta := map[string]int64{}

	for i := range validated {
		v := &validated[i]
		it := &v.Item
		ex, isExisting := existing[it.Ref]
		var seq int64
		var addedAt int64
		if isExisting {
			seq = ex.seq
			addedAt = ex.addedAt
			if ex.level != it.Level {
				tallyDelta[ex.level]--
				tallyDelta[it.Level]++
			}
		} else {
			seq = nextSeq
			nextSeq++
			addedAt = nowUnix
			tallyDelta[it.Level]++
		}
		it.Key = core.PluginKey(seq)

		parentRef := ""
		if it.ParentID != "" {
			parentRef = string(core.NewItemRef(pluginID, kindKey, it.ParentID))
		}
		hintsStr := "{}"
		if len(it.Hints) > 0 {
			hintsStr = string(it.Hints)
		}
		fieldsStr := "{}"
		if len(it.Fields) > 0 {
			if b, err := json.Marshal(it.Fields); err == nil {
				fieldsStr = string(b)
			}
		}
		unavailableStr := ""
		if it.Unavailable != nil {
			b, err := json.Marshal(it.Unavailable)
			if err != nil {
				return 0, err
			}
			unavailableStr = string(b)
		}
		playableInt := 0
		if it.Playable {
			playableInt = 1
		}

		if isPG {
			searchTitle := ""
			searchFields := ""
			if kind.Decl.Browse.Search {
				searchTitle = it.Title
				searchFields = v.SearchFields
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_items(
	item_ref, seq, plugin_id, kind_key, library_id, external_id, parent_ref, level, idx,
	title, sort_title, summary, year, released, rating, playable, child_count, leaf_count,
	unavailable, hints, fields, etag, added_at, updated_at, seen_at, missed_walks, search_tsv
) VALUES(
	?, ?, ?, ?, ?, ?, ?, ?, ?,
	?, ?, ?, ?, ?, ?, ?, ?, ?,
	?, ?, ?, ?, ?, ?, ?, 0,
	setweight(to_tsvector('simple', ?), 'A') || setweight(to_tsvector('simple', ?), 'B')
) ON CONFLICT(item_ref) DO UPDATE SET
	parent_ref   = excluded.parent_ref,
	level        = excluded.level,
	idx          = excluded.idx,
	title        = excluded.title,
	sort_title   = excluded.sort_title,
	summary      = excluded.summary,
	year         = excluded.year,
	released     = excluded.released,
	rating       = excluded.rating,
	playable     = excluded.playable,
	child_count  = excluded.child_count,
	leaf_count   = excluded.leaf_count,
	unavailable = excluded.unavailable,
	hints        = excluded.hints,
	fields       = excluded.fields,
	etag         = excluded.etag,
	updated_at   = excluded.updated_at,
	seen_at      = excluded.seen_at,
	missed_walks = 0,
	search_tsv   = excluded.search_tsv`,
				string(it.Ref), seq, pluginID, kindKey, int64(kind.LibraryID), it.ID, parentRef, it.Level, it.Index,
				it.Title, it.SortTitle, it.Summary, it.Year, it.Released, it.Rating, playableInt, it.ChildCount, it.LeafCount,
				unavailableStr, hintsStr, fieldsStr, it.ETag, addedAt, nowUnix, seenAt,
				searchTitle, searchFields,
			); err != nil {
				return 0, err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_items(
	item_ref, seq, plugin_id, kind_key, library_id, external_id, parent_ref, level, idx,
	title, sort_title, summary, year, released, rating, playable, child_count, leaf_count,
	unavailable, hints, fields, etag, added_at, updated_at, seen_at, missed_walks
) VALUES(
	?, ?, ?, ?, ?, ?, ?, ?, ?,
	?, ?, ?, ?, ?, ?, ?, ?, ?,
	?, ?, ?, ?, ?, ?, ?, 0
) ON CONFLICT(item_ref) DO UPDATE SET
	parent_ref   = excluded.parent_ref,
	level        = excluded.level,
	idx          = excluded.idx,
	title        = excluded.title,
	sort_title   = excluded.sort_title,
	summary      = excluded.summary,
	year         = excluded.year,
	released     = excluded.released,
	rating       = excluded.rating,
	playable     = excluded.playable,
	child_count  = excluded.child_count,
	leaf_count   = excluded.leaf_count,
	unavailable = excluded.unavailable,
	hints        = excluded.hints,
	fields       = excluded.fields,
	etag         = excluded.etag,
	updated_at   = excluded.updated_at,
	seen_at      = excluded.seen_at,
	missed_walks = 0`,
				string(it.Ref), seq, pluginID, kindKey, int64(kind.LibraryID), it.ID, parentRef, it.Level, it.Index,
				it.Title, it.SortTitle, it.Summary, it.Year, it.Released, it.Rating, playableInt, it.ChildCount, it.LeafCount,
				unavailableStr, hintsStr, fieldsStr, it.ETag, addedAt, nowUnix, seenAt,
			); err != nil {
				return 0, err
			}
			if kind.Decl.Browse.Search {
				if err := sqliteIndexFTS(ctx, tx, seq, it.Ref, kind.LibraryID, it.Title, v.SearchFields); err != nil {
					return 0, err
				}
			} else {
				if err := sqliteDeleteFTS(ctx, tx, seq); err != nil {
					return 0, err
				}
			}
		}

		// Replace indexed fields for this item.
		if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_item_fields WHERE item_ref = ?`, string(it.Ref)); err != nil {
			return 0, err
		}
		for _, fv := range v.IndexedFields {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO plugin_item_fields(item_ref, field, v_text, v_int, v_real) VALUES(?, ?, ?, ?, ?)
				 ON CONFLICT(item_ref, field, v_text, v_int) DO UPDATE SET v_real = excluded.v_real`,
				string(it.Ref), fv.Field, fv.Text, fv.Int, fv.Real); err != nil {
				return 0, err
			}
		}

		// Replace artwork rows for this item.
		if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_item_art WHERE item_ref = ?`, string(it.Ref)); err != nil {
			return 0, err
		}
		for _, a := range it.Art {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO plugin_item_art(item_ref, role, url, width, height) VALUES(?, ?, ?, ?, ?)`,
				string(it.Ref), a.Role, a.URL, a.Width, a.Height); err != nil {
				return 0, err
			}
		}
	}

	if err := applyTallyDeltaTx(ctx, tx, kind.LibraryID, tallyDelta); err != nil {
		return 0, err
	}

	if bumpGen {
		if err := s.bumpKindSyncStateTx(ctx, tx, pluginID, kindKey, nowUnix); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(items), nil
}

type existingItemMeta struct {
	seq     int64
	level   string
	addedAt int64
}

func lookupExistingItemsTx(ctx context.Context, tx *sql.Tx, items []ValidatedItem) (map[core.ItemRef]existingItemMeta, error) {
	out := make(map[core.ItemRef]existingItemMeta, len(items))
	const chunkSize = 250
	for start := 0; start < len(items); start += chunkSize {
		end := min(start+chunkSize, len(items))
		chunk := items[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, v := range chunk {
			placeholders[i] = "?"
			args[i] = string(v.Item.Ref)
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT item_ref, seq, level, added_at FROM plugin_items WHERE item_ref IN (`+strings.Join(placeholders, ",")+`)`,
			args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var refStr, lv string
			var seq, added int64
			if err := rows.Scan(&refStr, &seq, &lv, &added); err != nil {
				rows.Close()
				return nil, err
			}
			out[core.ItemRef(refStr)] = existingItemMeta{seq: seq, level: lv, addedAt: added}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

func (s *Service) lookupLevelsByExternalIDs(ctx context.Context, pluginID, kindKey string, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(ids))
	const chunkSize = 250
	for start := 0; start < len(ids); start += chunkSize {
		end := min(start+chunkSize, len(ids))
		chunk := ids[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, 2+len(chunk))
		args = append(args, pluginID, kindKey)
		for i, id := range chunk {
			placeholders[i] = "?"
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT external_id, level FROM plugin_items WHERE plugin_id = ? AND kind_key = ? AND external_id IN (`+strings.Join(placeholders, ",")+`)`,
			args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var extID, lv string
			if err := rows.Scan(&extID, &lv); err != nil {
				rows.Close()
				return nil, err
			}
			out[extID] = lv
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// allocateSeqRangeTx reserves count sequence numbers in plugin_items_seq
// and returns the first reserved sequence number. Sequence numbers are
// never decremented or reused across deletes or plugin removals (spec §2.2).
func allocateSeqRangeTx(ctx context.Context, tx *sql.Tx, count int64) (int64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE plugin_items_seq SET next = next + ? WHERE id = 1`, count); err != nil {
		return 0, err
	}
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT next FROM plugin_items_seq WHERE id = 1`).Scan(&next); err != nil {
		return 0, err
	}
	first := next - count
	if first+count >= core.PluginKeyMaxSeq {
		return 0, errors.New("pluginitems: PluginKey sequence space exhausted")
	}
	return first, nil
}

func applyTallyDeltaTx(ctx context.Context, tx *sql.Tx, libraryID core.LibraryID, delta map[string]int64) error {
	for level, d := range delta {
		if d == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO plugin_item_tally(library_id, level, n) VALUES(?, ?, ?)
ON CONFLICT(library_id, level) DO UPDATE SET n = CASE
	WHEN plugin_item_tally.n + excluded.n < 0 THEN 0
	ELSE plugin_item_tally.n + excluded.n
END`, int64(libraryID), level, d); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) bumpKindSyncStateTx(ctx context.Context, tx *sql.Tx, pluginID, kindKey string, nowUnix int64) error {
	var totalItems int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM plugin_items WHERE plugin_id = ? AND kind_key = ?`,
		pluginID, kindKey).Scan(&totalItems); err != nil {
		return err
	}
	gen := int64(s.syncGen.Add(1))
	_, err := tx.ExecContext(ctx, `
INSERT INTO plugin_sync_state(plugin_id, kind_key, generation, last_ok, last_error, items)
VALUES(?, ?, ?, ?, '', ?)
ON CONFLICT(plugin_id, kind_key) DO UPDATE SET
	generation = excluded.generation,
	last_ok    = excluded.last_ok,
	last_error = '',
	items      = excluded.items`,
		pluginID, kindKey, gen, nowUnix, totalItems)
	return err
}

// KindDelete implements the store side of plugins.KindBackends.KindDelete
// (spec §3.6).
func (s *Service) KindDelete(ctx context.Context, pluginID, kindKey string, externalIDs []string) error {
	return s.DeleteItems(ctx, pluginID, kindKey, externalIDs)
}

// DeleteItems deletes the named externalIDs (and any descendant items) from
// (pluginID, kindKey), maintaining tallies, FTS, matches and resolutions.
func (s *Service) DeleteItems(ctx context.Context, pluginID, kindKey string, externalIDs []string) error {
	mu := s.kindLock(pluginID, kindKey)
	mu.Lock()
	defer mu.Unlock()
	return s.deleteItemsLocked(ctx, pluginID, kindKey, externalIDs, true)
}

func (s *Service) deleteItemsLocked(ctx context.Context, pluginID, kindKey string, externalIDs []string, bumpGen bool) error {
	kind, err := s.GetKind(ctx, pluginID, kindKey)
	if err != nil {
		return err
	}
	if len(externalIDs) == 0 {
		return nil
	}
	refs := make([]core.ItemRef, 0, len(externalIDs))
	seen := make(map[core.ItemRef]bool, len(externalIDs))
	for _, id := range externalIDs {
		ref := core.NewItemRef(pluginID, kindKey, id)
		if !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.deleteRefsAndDescendantsTx(ctx, tx, kind.LibraryID, refs); err != nil {
		return err
	}
	if bumpGen {
		if err := s.bumpKindSyncStateTx(ctx, tx, pluginID, kindKey, s.now().Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) deleteRefsAndDescendantsTx(ctx context.Context, tx *sql.Tx, libraryID core.LibraryID, rootRefs []core.ItemRef) error {
	if len(rootRefs) == 0 {
		return nil
	}
	type target struct {
		ref   core.ItemRef
		seq   int64
		level string
	}
	allTargets := make(map[core.ItemRef]target, len(rootRefs))
	frontier := append([]core.ItemRef(nil), rootRefs...)

	// Walk up to 5 hierarchy levels to collect all targets and their descendants.
	for depth := 0; depth < 6 && len(frontier) > 0; depth++ {
		var nextFrontier []core.ItemRef
		const chunkSize = 250
		for start := 0; start < len(frontier); start += chunkSize {
			end := min(start+chunkSize, len(frontier))
			chunk := frontier[start:end]
			placeholders := make([]string, len(chunk))
			args := make([]any, len(chunk))
			for i, r := range chunk {
				placeholders[i] = "?"
				args[i] = string(r)
			}
			inClause := strings.Join(placeholders, ",")
			if depth == 0 {
				rows, err := tx.QueryContext(ctx,
					`SELECT item_ref, seq, level FROM plugin_items WHERE item_ref IN (`+inClause+`)`, args...)
				if err != nil {
					return err
				}
				for rows.Next() {
					var refStr, lv string
					var seq int64
					if err := rows.Scan(&refStr, &seq, &lv); err != nil {
						rows.Close()
						return err
					}
					r := core.ItemRef(refStr)
					allTargets[r] = target{ref: r, seq: seq, level: lv}
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					return err
				}
				rows.Close()
			}
			// Find direct children of this chunk.
			crows, err := tx.QueryContext(ctx,
				`SELECT item_ref, seq, level FROM plugin_items WHERE parent_ref IN (`+inClause+`)`, args...)
			if err != nil {
				return err
			}
			for crows.Next() {
				var refStr, lv string
				var seq int64
				if err := crows.Scan(&refStr, &seq, &lv); err != nil {
					crows.Close()
					return err
				}
				r := core.ItemRef(refStr)
				if _, already := allTargets[r]; !already {
					allTargets[r] = target{ref: r, seq: seq, level: lv}
					nextFrontier = append(nextFrontier, r)
				}
			}
			if err := crows.Err(); err != nil {
				crows.Close()
				return err
			}
			crows.Close()
		}
		frontier = nextFrontier
	}

	if len(allTargets) == 0 {
		return nil
	}

	isSQLite := store.DialectOf(s.db) == store.SQLite
	tallyDelta := map[string]int64{}
	for _, t := range allTargets {
		tallyDelta[t.level]--
		if isSQLite {
			if err := sqliteDeleteFTS(ctx, tx, t.seq); err != nil {
				return err
			}
		}
		refStr := string(t.ref)
		for _, tbl := range []string{
			"plugin_item_fields",
			"plugin_item_art",
			"plugin_matches",
			"plugin_resolutions",
			"plugin_items",
		} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+tbl+` WHERE item_ref = ?`, refStr); err != nil {
				return err
			}
		}
	}
	return applyTallyDeltaTx(ctx, tx, libraryID, tallyDelta)
}

// KindItems implements the store side of plugins.KindBackends.KindItems
// (spec §3.6): reads the plugin's own library back, keyset-paged across all
// levels by external_id.
func (s *Service) KindItems(ctx context.Context, pluginID, kindKey, cursor string, limit int) (core.KindPage, error) {
	if _, err := s.GetKind(ctx, pluginID, kindKey); err != nil {
		return core.KindPage{}, err
	}
	if limit <= 0 {
		limit = DefaultBrowseLimit
	}
	if limit > MaxBrowseLimit {
		limit = MaxBrowseLimit
	}
	items, err := s.queryItems(ctx, `
SELECT `+itemCols+`
FROM plugin_items
WHERE plugin_id = ? AND kind_key = ? AND external_id > ?
ORDER BY external_id ASC
LIMIT ?`,
		[]any{pluginID, kindKey, cursor, limit + 1}, true)
	if err != nil {
		return core.KindPage{}, err
	}
	var nextCursor string
	if len(items) > limit {
		items = items[:limit]
		nextCursor = items[len(items)-1].ID
	}
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM plugin_items WHERE plugin_id = ? AND kind_key = ?`,
		pluginID, kindKey).Scan(&total); err != nil {
		return core.KindPage{}, err
	}
	return core.KindPage{
		Items:      items,
		NextCursor: nextCursor,
		Total:      &total,
	}, nil
}

// GetByRef returns one stored KindItem and its LibraryID by ItemRef.
func (s *Service) GetByRef(ctx context.Context, ref core.ItemRef) (core.KindItem, core.LibraryID, error) {
	return s.getOne(ctx, `SELECT `+itemCols+` FROM plugin_items WHERE item_ref = ?`, string(ref))
}

// GetByKey returns one stored KindItem and its LibraryID by its PluginKey RatingKey.
func (s *Service) GetByKey(ctx context.Context, key core.RatingKey) (core.KindItem, core.LibraryID, error) {
	seq := key.PluginSeq()
	if seq <= 0 {
		return core.KindItem{}, 0, core.ErrNotFound
	}
	return s.getOne(ctx, `SELECT `+itemCols+` FROM plugin_items WHERE seq = ?`, seq)
}

// GetByExternal returns one stored KindItem and its LibraryID by (pluginID, kindKey, externalID).
func (s *Service) GetByExternal(ctx context.Context, pluginID, kindKey, externalID string) (core.KindItem, core.LibraryID, error) {
	return s.GetByRef(ctx, core.NewItemRef(pluginID, kindKey, externalID))
}

func (s *Service) getOne(ctx context.Context, query string, arg any) (core.KindItem, core.LibraryID, error) {
	it, libID, _, err := scanItemRow(s.db.QueryRowContext(ctx, query, arg))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.KindItem{}, 0, core.ErrNotFound
		}
		return core.KindItem{}, 0, err
	}
	slice := []core.KindItem{it}
	if err := s.hydrateArtSlice(ctx, slice); err != nil {
		return core.KindItem{}, 0, err
	}
	return slice[0], libID, nil
}

// Children returns the direct children of parentRef ordered by (idx ASC, sort_title ASC, item_ref ASC).
func (s *Service) Children(ctx context.Context, parentRef core.ItemRef, cursor string, limit int) (BrowsePage, error) {
	_, libID, err := s.GetByRef(ctx, parentRef)
	if err != nil {
		return BrowsePage{}, err
	}
	return s.Browse(ctx, BrowseQuery{
		LibraryID: libID,
		ParentRef: parentRef,
		Sort:      "index",
		Cursor:    cursor,
		Limit:     limit,
	})
}

// LibraryTally returns the maintained per-level item counts for libraryID
// from plugin_item_tally (spec §2.3, §3.4).
func (s *Service) LibraryTally(ctx context.Context, libraryID core.LibraryID) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT level, n FROM plugin_item_tally WHERE library_id = ?`, int64(libraryID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var lv string
		var n int64
		if err := rows.Scan(&lv, &n); err != nil {
			return nil, err
		}
		out[lv] = int(n)
	}
	return out, rows.Err()
}

// SyncStateOf returns the current SyncState row for (pluginID, kindKey).
func (s *Service) SyncStateOf(ctx context.Context, pluginID, kindKey string) (SyncState, error) {
	var (
		st          SyncState
		gen         int64
		lastStarted int64
		lastOK      int64
	)
	err := s.db.QueryRowContext(ctx, `
SELECT plugin_id, kind_key, cursor, generation, last_started, last_ok, last_error, items
FROM plugin_sync_state
WHERE plugin_id = ? AND kind_key = ?`, pluginID, kindKey).Scan(
		&st.PluginID, &st.KindKey, &st.Cursor, &gen, &lastStarted, &lastOK, &st.LastError, &st.Items)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SyncState{}, core.ErrNotFound
		}
		return SyncState{}, err
	}
	st.Generation = uint64(gen)
	if lastStarted > 0 {
		st.LastStarted = time.Unix(lastStarted, 0).UTC()
	}
	if lastOK > 0 {
		st.LastOK = time.Unix(lastOK, 0).UTC()
	}
	return st, nil
}

// PurgeKind removes all items, fields, art, tallies, matches, resolutions,
// sync state and kind registration for one (pluginID, kindKey) (spec §2.5).
func (s *Service) PurgeKind(ctx context.Context, pluginID, kindKey string) error {
	s.StopKindSync(pluginID, kindKey)
	mu := s.kindLock(pluginID, kindKey)
	mu.Lock()
	defer mu.Unlock()

	var libID int64
	_ = s.db.QueryRowContext(ctx,
		`SELECT library_id FROM plugin_kinds WHERE plugin_id = ? AND kind_key = ?`,
		pluginID, kindKey).Scan(&libID)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.purgeKindTx(ctx, tx, pluginID, kindKey, core.LibraryID(libID), true); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.syncGen.Add(1)
	return nil
}

// PurgePlugin is the idempotent removal sequence for all kinds owned by
// pluginID (spec §2.5): stops sync goroutines, deletes watch-state rows for
// the plugin's RatingKeys, and purges items, fields, art, FTS, tallies,
// matches, resolutions, sync state and plugin_kinds rows.
func (s *Service) PurgePlugin(ctx context.Context, pluginID string) error {
	s.StopPluginSync(pluginID)

	kinds, err := s.ListKinds(ctx, pluginID)
	if err != nil {
		return err
	}
	for _, k := range kinds {
		mu := s.kindLock(pluginID, k.KindKey)
		mu.Lock()
		defer mu.Unlock()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, k := range kinds {
		if err := s.purgeKindTx(ctx, tx, pluginID, k.KindKey, k.LibraryID, true); err != nil {
			return err
		}
	}
	// Catch any orphan rows for pluginID even if plugin_kinds was already empty.
	if err := s.purgeKindTx(ctx, tx, pluginID, "", 0, true); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	s.syncGen.Add(1)
	return nil
}

func (s *Service) purgeKindTx(ctx context.Context, tx *sql.Tx, pluginID, kindKey string, libraryID core.LibraryID, dropKindRow bool) error {
	whereItems := `plugin_id = ? AND kind_key = ?`
	args := []any{pluginID, kindKey}
	if kindKey == "" {
		whereItems = `plugin_id = ?`
		args = []any{pluginID}
	}

	// Delete watch-state rows for the plugin's RatingKeys by seq range when
	// state_watch is present in the schema (spec §2.5).
	var minSeq, maxSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MIN(seq), MAX(seq) FROM plugin_items WHERE `+whereItems, args...).Scan(&minSeq, &maxSeq); err != nil {
		return err
	}
	if minSeq.Valid && maxSeq.Valid && minSeq.Int64 > 0 {
		minKey := int64(core.PluginKey(minSeq.Int64))
		maxKey := int64(core.PluginKey(maxSeq.Int64))
		offset := int64(core.PluginKey(1)) - 1
		if hasTableTx(ctx, tx, store.DialectOf(s.db), "state_watch") {
			delArgs := append([]any{minKey, maxKey, offset}, args...)
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM state_watch WHERE rating_key BETWEEN ? AND ? AND rating_key IN (SELECT ? + seq FROM plugin_items WHERE `+whereItems+`)`,
				delArgs...); err != nil {
				return err
			}
		}
	}

	if store.DialectOf(s.db) == store.SQLite {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM plugin_item_fts WHERE rowid IN (SELECT seq FROM plugin_items WHERE `+whereItems+`)`,
			args...); err != nil {
			return err
		}
	}
	for _, tbl := range []string{
		"plugin_item_fields",
		"plugin_item_art",
		"plugin_matches",
		"plugin_resolutions",
	} {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM `+tbl+` WHERE item_ref IN (SELECT item_ref FROM plugin_items WHERE `+whereItems+`)`,
			args...); err != nil {
			return err
		}
	}
	if libraryID > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_item_tally WHERE library_id = ?`, int64(libraryID)); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM plugin_item_tally WHERE library_id IN (SELECT DISTINCT library_id FROM plugin_items WHERE `+whereItems+`)`,
			args...); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_items WHERE `+whereItems, args...); err != nil {
		return err
	}
	if dropKindRow {
		if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_sync_state WHERE `+whereItems, args...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM plugin_kinds WHERE `+whereItems, args...); err != nil {
			return err
		}
	} else if kindKey != "" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE plugin_sync_state SET cursor = '', last_started = 0, last_ok = 0, last_error = '', items = 0 WHERE plugin_id = ? AND kind_key = ?`,
			pluginID, kindKey); err != nil {
			return err
		}
	}
	return nil
}

func hasTableTx(ctx context.Context, tx *sql.Tx, d store.Dialect, table string) bool {
	var n int
	if d == store.Postgres {
		_ = tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?`,
			table).Scan(&n)
		return n > 0
	}
	_ = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`,
		table).Scan(&n)
	return n > 0
}

// ResolutionCacheMaxTTL is the maximum positive cache window for a resolved
// PlayableSource (spec §4.4: min(expiresAt, now+15m)).
const (
	ResolutionCacheMaxTTL      = 15 * time.Minute
	ResolutionNegativeCacheTTL = 60 * time.Second
)

// SaveResolution caches a successful PlayableSource for ref until
// min(src.ExpiresAt, now+15m) (spec §4.4).
func (s *Service) SaveResolution(ctx context.Context, ref core.ItemRef, src core.PlayableSource) error {
	now := s.now()
	exp := now.Add(ResolutionCacheMaxTTL).Unix()
	if src.ExpiresAt > 0 && src.ExpiresAt < exp {
		exp = src.ExpiresAt
	}
	raw, err := json.Marshal(src)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO plugin_resolutions(item_ref, pattern, source, negative, expires_at)
VALUES(?, ?, ?, 0, ?)
ON CONFLICT(item_ref) DO UPDATE SET
	pattern    = excluded.pattern,
	source     = excluded.source,
	negative   = 0,
	expires_at = excluded.expires_at`,
		string(ref), src.Pattern, string(raw), exp)
	return err
}

// SaveNegativeResolution caches a not_available resolution result for 60s so
// a dead kind cannot stampede the plugin (spec §4.4).
func (s *Service) SaveNegativeResolution(ctx context.Context, ref core.ItemRef) error {
	exp := s.now().Add(ResolutionNegativeCacheTTL).Unix()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO plugin_resolutions(item_ref, pattern, source, negative, expires_at)
VALUES(?, '', '{}', 1, ?)
ON CONFLICT(item_ref) DO UPDATE SET
	pattern    = '',
	source     = '{}',
	negative   = 1,
	expires_at = excluded.expires_at`,
		string(ref), exp)
	return err
}

// GetResolution looks up a non-expired cached resolution for ref (spec §4.4).
// If a negative cache entry is active, it returns (zero, true, core.ErrNotAvailable).
func (s *Service) GetResolution(ctx context.Context, ref core.ItemRef) (core.PlayableSource, bool, error) {
	nowUnix := s.now().Unix()
	var (
		sourceJSON string
		negative   int
		expiresAt  int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT source, negative, expires_at FROM plugin_resolutions WHERE item_ref = ?`,
		string(ref)).Scan(&sourceJSON, &negative, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return core.PlayableSource{}, false, nil
		}
		return core.PlayableSource{}, false, err
	}
	if expiresAt <= nowUnix {
		return core.PlayableSource{}, false, nil
	}
	if negative != 0 {
		return core.PlayableSource{}, true, core.ErrNotAvailable
	}
	var src core.PlayableSource
	if err := json.Unmarshal([]byte(sourceJSON), &src); err != nil {
		return core.PlayableSource{}, false, err
	}
	return src, true, nil
}

// InvalidateResolution deletes the cached resolution for ref, e.g. on a
// mid-stream upstream failure (spec §4.4).
func (s *Service) InvalidateResolution(ctx context.Context, ref core.ItemRef) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM plugin_resolutions WHERE item_ref = ?`, string(ref))
	return err
}
