package libraries

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
)

const libraryCols = `id, key, name, source, media_type, plugin_id, kind_key, filter, present, sort, hidden, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanLibrary(sc scanner) (core.Library, error) {
	var lib core.Library
	var id, sort, created, updated int64
	var source, mediaType, filterJSON, presentJSON string
	var hidden int
	if err := sc.Scan(&id, &lib.Key, &lib.Name, &source, &mediaType, &lib.PluginID, &lib.KindKey,
		&filterJSON, &presentJSON, &sort, &hidden, &created, &updated); err != nil {
		return core.Library{}, err
	}
	lib.ID = core.LibraryID(id)
	lib.Source = core.LibrarySource(source)
	lib.MediaType = core.MediaType(mediaType)
	lib.Sort = int(sort)
	lib.Hidden = hidden != 0
	if created > 0 {
		lib.CreatedAt = time.Unix(created, 0).UTC()
	}
	if updated > 0 {
		lib.UpdatedAt = time.Unix(updated, 0).UTC()
	}
	if filterJSON != "" {
		var f core.Filter
		if err := json.Unmarshal([]byte(filterJSON), &f); err != nil {
			return core.Library{}, err
		}
		lib.Filter = &f
	}
	if presentJSON != "" {
		if err := json.Unmarshal([]byte(presentJSON), &lib.Present); err != nil {
			return core.Library{}, err
		}
	}
	return lib, nil
}

func (s *Service) get(ctx context.Context, where string, arg any) (core.Library, error) {
	lib, err := scanLibrary(s.db.QueryRowContext(ctx, `SELECT `+libraryCols+` FROM libraries WHERE `+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return core.Library{}, core.ErrNotFound
	}
	return lib, err
}

// insert writes lib (ID already allocated) inside tx and returns it
// with its timestamps set.
func (s *Service) insert(ctx context.Context, tx *sql.Tx, lib core.Library) (core.Library, error) {
	now := s.now()
	lib.CreatedAt, lib.UpdatedAt = now.UTC(), now.UTC()
	filterJSON := ""
	if lib.Filter != nil {
		b, err := json.Marshal(lib.Filter)
		if err != nil {
			return core.Library{}, err
		}
		filterJSON = string(b)
	}
	presentJSON, err := json.Marshal(lib.Present)
	if err != nil {
		return core.Library{}, err
	}
	hidden := 0
	if lib.Hidden {
		hidden = 1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO libraries(`+libraryCols+`) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		int64(lib.ID), lib.Key, lib.Name, string(lib.Source), string(lib.MediaType), lib.PluginID, lib.KindKey,
		filterJSON, string(presentJSON), lib.Sort, hidden, lib.CreatedAt.Unix(), lib.UpdatedAt.Unix())
	if err != nil {
		return core.Library{}, err
	}
	return lib, nil
}

// setColumn is the one-column owner edit shared by Rename, SetSort,
// SetHidden and SetPresent. col is a literal from this package, never
// caller input.
func (s *Service) setColumn(ctx context.Context, id core.LibraryID, col string, value any) error {
	res, err := s.db.ExecContext(ctx, `UPDATE libraries SET `+col+`=?, updated_at=? WHERE id=?`, value, s.now().Unix(), int64(id))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return core.ErrNotFound
	}
	s.gen.Add(1)
	return nil
}

// keyTaken reports whether slug key already names a library in tx.
func keyTaken(ctx context.Context, tx *sql.Tx, key string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE key = ?`, key).Scan(&n)
	return n > 0, err
}

// freeKey returns base, or base-2, base-3, …: the first slug no
// library uses yet.
func freeKey(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	key := base
	for i := 2; ; i++ {
		taken, err := keyTaken(ctx, tx, key)
		if err != nil {
			return "", err
		}
		if !taken {
			return key, nil
		}
		key = base + "-" + itoa(i)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// slugify turns a display name into an immutable library key:
// lower-case letters and digits joined by single dashes. An empty
// result (a name with no letters or digits at all) is invalid.
func slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	return b.String()
}
