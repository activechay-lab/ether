package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Per-viewer data lifecycle. Every component that stores rows belonging to
// one viewer registers how those rows are purged (the viewer is removed)
// and merged (the viewer is folded into another), next to the migrations
// that create the tables. RemoveViewer and MergeViewers in internal/auth
// run every registration inside one transaction, so a viewer never leaves
// rows behind and a new per-viewer table cannot be forgotten: the coverage
// guard in internal/app fails until its table is registered here or
// explicitly exempted with a reason.

// ViewerMergeMode says how a table's rows move when one viewer is merged
// into another.
type ViewerMergeMode int

const (
	// ViewerMergeMove reassigns the source viewer's rows to the target.
	// Where a row would collide with one the target already has (the same
	// values in the registered conflict columns, or any row at all when no
	// conflict columns are registered because the viewer column alone is
	// the key), the target's row wins and the source row is discarded.
	ViewerMergeMove ViewerMergeMode = iota
	// ViewerMergeUnion reassigns every source row to the target. Use it
	// for history and play logs whose key does not involve the viewer.
	ViewerMergeUnion
	// ViewerMergeDrop deletes the source viewer's rows. Use it for
	// sessions, credentials-in-flight and other ephemeral state that must
	// not transfer to another person.
	ViewerMergeDrop
)

// ViewerPurgeFunc deletes one viewer's data inside tx.
type ViewerPurgeFunc func(ctx context.Context, tx *sql.Tx, viewerID int64) error

// ViewerMergeFunc moves one viewer's data to another inside tx.
type ViewerMergeFunc func(ctx context.Context, tx *sql.Tx, fromID, intoID int64) error

type viewerPurge struct {
	component string
	tables    []string
	early     bool
	fn        ViewerPurgeFunc
}

type viewerMerge struct {
	component string
	tables    []string
	early     bool
	fn        ViewerMergeFunc
}

var (
	viewerMu      sync.Mutex
	viewerPurges  []viewerPurge
	viewerMerges  []viewerMerge
	viewerTables  = map[string]string{} // table -> component
	viewerExempts = map[string]string{} // table -> reason
)

func claimViewerTables(component string, tables []string) {
	for _, table := range tables {
		if other, ok := viewerTables[table]; ok && other != component {
			panic(fmt.Sprintf("store: viewer table %s registered by both %s and %s", table, other, component))
		}
		viewerTables[table] = component
	}
}

// RegisterViewerPurge registers a custom purge for the named tables. Most
// components use RegisterViewerTable instead; a custom purge suits rows
// that must be transformed rather than deleted (for example detaching a
// shared collection from its creator).
func RegisterViewerPurge(component string, tables []string, fn ViewerPurgeFunc) {
	registerViewerPurge(component, tables, false, fn)
}

func registerViewerPurge(component string, tables []string, early bool, fn ViewerPurgeFunc) {
	if fn == nil {
		panic("store: RegisterViewerPurge requires a function")
	}
	viewerMu.Lock()
	defer viewerMu.Unlock()
	claimViewerTables(component, tables)
	viewerPurges = append(viewerPurges, viewerPurge{component: component, tables: tables, early: early, fn: fn})
}

// RegisterViewerMerge registers a custom merge for the named tables.
func RegisterViewerMerge(component string, tables []string, fn ViewerMergeFunc) {
	if fn == nil {
		panic("store: RegisterViewerMerge requires a function")
	}
	viewerMu.Lock()
	defer viewerMu.Unlock()
	claimViewerTables(component, tables)
	viewerMerges = append(viewerMerges, viewerMerge{component: component, tables: tables, fn: fn})
}

// RegisterViewerTable registers the standard purge and merge for one table
// keyed by a viewer column ("user_id", "viewer_id" or "viewer"). For
// ViewerMergeMove, conflictColumns names the remaining key columns that
// decide a collision with the target's rows; omit them when the viewer
// column alone is the table's key.
func RegisterViewerTable(component, table, column string, merge ViewerMergeMode, conflictColumns ...string) {
	registerViewerTable(component, table, column, merge, false, conflictColumns)
}

// RegisterViewerTableEarly is RegisterViewerTable for a table other
// tables reference with ON DELETE RESTRICT: its purge and merge run
// before all ordinary registrations.
func RegisterViewerTableEarly(component, table, column string, merge ViewerMergeMode, conflictColumns ...string) {
	registerViewerTable(component, table, column, merge, true, conflictColumns)
}

func registerViewerTable(component, table, column string, merge ViewerMergeMode, early bool, conflictColumns []string) {
	t, c := quoteIdent(table), quoteIdent(column)
	registerViewerPurge(component, []string{table}, early, func(ctx context.Context, tx *sql.Tx, viewerID int64) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE `+c+`=?`, viewerID)
		if err != nil {
			return fmt.Errorf("purge %s: %w", table, err)
		}
		return nil
	})
	fn := func(ctx context.Context, tx *sql.Tx, fromID, intoID int64) error {
		switch merge {
		case ViewerMergeDrop:
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE `+c+`=?`, fromID); err != nil {
				return fmt.Errorf("merge %s: %w", table, err)
			}
			return nil
		case ViewerMergeUnion:
			if _, err := tx.ExecContext(ctx, `UPDATE `+t+` SET `+c+`=? WHERE `+c+`=?`, intoID, fromID); err != nil {
				return fmt.Errorf("merge %s: %w", table, err)
			}
			return nil
		default: // ViewerMergeMove: the target's colliding rows win.
			var del string
			if len(conflictColumns) == 0 {
				del = `DELETE FROM ` + t + ` WHERE ` + c + `=? AND EXISTS (SELECT 1 FROM ` + t + ` WHERE ` + c + `=?)`
			} else {
				var cond strings.Builder
				for _, col := range conflictColumns {
					q := quoteIdent(col)
					cond.WriteString(` AND (o.` + q + `=` + t + `.` + q + ` OR (o.` + q + ` IS NULL AND ` + t + `.` + q + ` IS NULL))`)
				}
				del = `DELETE FROM ` + t + ` WHERE ` + c + `=? AND EXISTS (SELECT 1 FROM ` + t + ` AS o WHERE o.` + c + `=?` + cond.String() + `)`
			}
			if _, err := tx.ExecContext(ctx, del, fromID, intoID); err != nil {
				return fmt.Errorf("merge %s: %w", table, err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE `+t+` SET `+c+`=? WHERE `+c+`=?`, intoID, fromID); err != nil {
				return fmt.Errorf("merge %s: %w", table, err)
			}
			return nil
		}
	}
	viewerMu.Lock()
	viewerMerges = append(viewerMerges, viewerMerge{component: component, tables: []string{table}, early: early, fn: fn})
	viewerMu.Unlock()
}

func sortedPurges() []viewerPurge {
	viewerMu.Lock()
	out := append([]viewerPurge(nil), viewerPurges...)
	viewerMu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].early != out[j].early {
			return out[i].early
		}
		if out[i].component != out[j].component {
			return out[i].component < out[j].component
		}
		return strings.Join(out[i].tables, "\x00") < strings.Join(out[j].tables, "\x00")
	})
	return out
}

func sortedMerges() []viewerMerge {
	viewerMu.Lock()
	out := append([]viewerMerge(nil), viewerMerges...)
	viewerMu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].early != out[j].early {
			return out[i].early
		}
		if out[i].component != out[j].component {
			return out[i].component < out[j].component
		}
		return strings.Join(out[i].tables, "\x00") < strings.Join(out[j].tables, "\x00")
	})
	return out
}

// PurgeViewer runs every registered purge for viewerID inside tx, early
// registrations first. The caller deletes the auth_users row itself,
// after PurgeViewer, so ON DELETE CASCADE remains a backstop.
func PurgeViewer(ctx context.Context, tx *sql.Tx, viewerID int64) error {
	for _, p := range sortedPurges() {
		if err := p.fn(ctx, tx, viewerID); err != nil {
			return fmt.Errorf("store: purge viewer (%s): %w", p.component, err)
		}
	}
	return nil
}

// MergeViewer runs every registered merge from one viewer into another
// inside tx, early registrations first.
func MergeViewer(ctx context.Context, tx *sql.Tx, fromID, intoID int64) error {
	for _, m := range sortedMerges() {
		if err := m.fn(ctx, tx, fromID, intoID); err != nil {
			return fmt.Errorf("store: merge viewer (%s): %w", m.component, err)
		}
	}
	return nil
}
