package libraries

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

// List returns every library in owner order: sort, then id (§1.3).
func (s *Service) List(ctx context.Context) ([]core.Library, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+libraryCols+` FROM libraries ORDER BY sort, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Library
	for rows.Next() {
		lib, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lib)
	}
	return out, rows.Err()
}

// Get returns one library by id, or core.ErrNotFound.
func (s *Service) Get(ctx context.Context, id core.LibraryID) (core.Library, error) {
	return s.get(ctx, `id = ?`, int64(id))
}

// ByKey returns one library by its immutable slug, or core.ErrNotFound.
func (s *Service) ByKey(ctx context.Context, key string) (core.Library, error) {
	return s.get(ctx, `key = ?`, key)
}

// CreateFilter saves an owner filter-library (§1.5): a named
// core.Filter over the TMDB catalogue. Only the filter's
// JSON-serialised fields persist; its runtime fields are injected at
// query time. The caller passes the zero Presentations for the
// console-only default (§15.3) and the owner opts in per protocol
// afterwards with SetPresent.
func (s *Service) CreateFilter(ctx context.Context, name string, f core.Filter, p core.Presentations) (core.Library, error) {
	name = strings.TrimSpace(name)
	slug := slugify(name)
	if name == "" || slug == "" {
		return core.Library{}, fmt.Errorf("%w: filter library needs a name with letters or digits", ErrInvalid)
	}
	if f.Type != "" && f.Type != core.TypeMovie && f.Type != core.TypeShow {
		return core.Library{}, fmt.Errorf("%w: filter library type must be movie or show", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Library{}, err
	}
	defer tx.Rollback()
	id, err := nextID(ctx, tx)
	if err != nil {
		return core.Library{}, err
	}
	key, err := freeKey(ctx, tx, slug)
	if err != nil {
		return core.Library{}, err
	}
	filter := f // stored verbatim, never the caller's pointer target
	lib, err := s.insert(ctx, tx, core.Library{
		ID: id, Key: key, Name: name, Source: core.LibraryFilter,
		MediaType: f.Type, Filter: &filter, Present: p, Sort: int(id),
	})
	if err != nil {
		return core.Library{}, err
	}
	if err := tx.Commit(); err != nil {
		return core.Library{}, err
	}
	s.gen.Add(1)
	return lib, nil
}

// UpsertPluginLibrary returns the library for one plugin media kind,
// creating it on first install (§2.5). An existing library is
// returned untouched: the stored name and presentation are
// owner-owned after install and a later manifest never overwrites
// them.
func (s *Service) UpsertPluginLibrary(ctx context.Context, pluginID, kindKey string, decl PluginLibraryDecl) (core.Library, error) {
	pluginID, kindKey = strings.TrimSpace(pluginID), strings.TrimSpace(kindKey)
	name := strings.TrimSpace(decl.Name)
	if pluginID == "" || kindKey == "" || name == "" || slugify(pluginID) == "" || slugify(kindKey) == "" {
		return core.Library{}, fmt.Errorf("%w: plugin library needs plugin, kind and name", ErrInvalid)
	}
	existing, err := s.libraryForPluginKind(ctx, pluginID, kindKey)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, core.ErrNotFound) {
		return core.Library{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return core.Library{}, err
	}
	defer tx.Rollback()
	id, err := nextID(ctx, tx)
	if err != nil {
		return core.Library{}, err
	}
	key, err := freeKey(ctx, tx, "pk-"+slugify(pluginID)+"-"+slugify(kindKey))
	if err != nil {
		return core.Library{}, err
	}
	lib, err := s.insert(ctx, tx, core.Library{
		ID: id, Key: key, Name: name, Source: core.LibraryPlugin,
		PluginID: pluginID, KindKey: kindKey, Present: decl.Present, Sort: int(id),
	})
	if err != nil {
		// A concurrent install won the (plugin, kind) unique index:
		// return its row rather than failing the install.
		if again, gerr := s.libraryForPluginKind(ctx, pluginID, kindKey); gerr == nil {
			return again, nil
		}
		return core.Library{}, err
	}
	if err := tx.Commit(); err != nil {
		if again, gerr := s.libraryForPluginKind(ctx, pluginID, kindKey); gerr == nil {
			return again, nil
		}
		return core.Library{}, err
	}
	s.gen.Add(1)
	return lib, nil
}

func (s *Service) libraryForPluginKind(ctx context.Context, pluginID, kindKey string) (core.Library, error) {
	lib, err := scanLibrary(s.db.QueryRowContext(ctx,
		`SELECT `+libraryCols+` FROM libraries WHERE source = 'plugin' AND plugin_id = ? AND kind_key = ?`, pluginID, kindKey))
	if errors.Is(err, sql.ErrNoRows) {
		return core.Library{}, core.ErrNotFound
	}
	return lib, err
}

// Rename changes a library's display name. The key never changes.
func (s *Service) Rename(ctx context.Context, id core.LibraryID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: library name must not be empty", ErrInvalid)
	}
	return s.setColumn(ctx, id, "name", name)
}

// SetSort sets a library's owner order in every client.
func (s *Service) SetSort(ctx context.Context, id core.LibraryID, sort int) error {
	return s.setColumn(ctx, id, "sort", sort)
}

// SetHidden hides or shows a library in all clients; it stays in the
// console either way.
func (s *Service) SetHidden(ctx context.Context, id core.LibraryID, hidden bool) error {
	v := 0
	if hidden {
		v = 1
	}
	return s.setColumn(ctx, id, "hidden", v)
}

// SetPresent sets how protocols expose a library (§6.2).
func (s *Service) SetPresent(ctx context.Context, id core.LibraryID, p core.Presentations) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.setColumn(ctx, id, "present", string(b))
}

// Delete removes a filter or plugin library. Built-ins are refused:
// Movies and TV Shows exist forever. Plugin libraries are deleted
// only through plugin removal (§2.5), which calls this after purging
// the plugin's items.
func (s *Service) Delete(ctx context.Context, id core.LibraryID) error {
	lib, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if lib.Source == core.LibraryBuiltin {
		return ErrBuiltinLibrary
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, int64(id)); err != nil {
		return err
	}
	s.gen.Add(1)
	return nil
}
