package libraries

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	const migration = `ALTER TABLE libraries ADD COLUMN sources TEXT NOT NULL DEFAULT '[]';
CREATE TABLE libraries_catalogue_members (source_id TEXT NOT NULL, key BIGINT NOT NULL, PRIMARY KEY(source_id,key));
CREATE TABLE libraries_catalogue_state (source_id TEXT PRIMARY KEY, refreshed BIGINT NOT NULL);`
	store.Register("libraries", 5, migration)
	store.RegisterDialect("libraries", 5, store.Postgres, migration)
	store.Register("libraries", 6, `ALTER TABLE libraries_catalogue_state ADD COLUMN item_count BIGINT NOT NULL DEFAULT 0;`)
	store.RegisterDialect("libraries", 6, store.Postgres, `ALTER TABLE libraries_catalogue_state ADD COLUMN item_count BIGINT NOT NULL DEFAULT 0;`)
}

// SetSources records validated registry references. Validation against enabled
// declarations belongs to the application registry, outside the library store.
func (s *Service) SetSources(ctx context.Context, id core.LibraryID, sources []string) error {
	if len(sources) == 0 {
		return ErrInvalid
	}
	data, err := json.Marshal(sources)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE libraries SET sources=?, updated_at=? WHERE id=?`, string(data), s.now().Unix(), int64(id))
	if err == nil {
		s.gen.Add(1)
	}
	return err
}

func defaultSources(lib core.Library) []string {
	if len(lib.Sources) > 0 {
		return lib.Sources
	}
	if lib.Source == core.LibraryPlugin {
		return []string{"plugin:" + lib.PluginID + ":" + lib.KindKey}
	}
	typ := lib.MediaType
	if lib.Filter != nil {
		typ = lib.Filter.Type
	}
	if typ == core.TypeShow {
		return []string{"tmdb-tv"}
	}
	return []string{"tmdb-movies"}
}

// SourceMember checks persisted membership without network work in playback.
func (s *Service) SourceMember(ctx context.Context, lib core.Library, key core.RatingKey) (bool, error) {
	for _, id := range lib.Sources {
		if id == "tmdb-movies" || id == "tmdb-tv" {
			return true, nil
		}
	}
	for _, id := range lib.Sources {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries_catalogue_members WHERE source_id=? AND key=?`, id, int64(key)).Scan(&n); err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return len(lib.Sources) == 0, nil
}
