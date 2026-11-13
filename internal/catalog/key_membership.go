package catalog

import (
	"encoding/binary"
	"encoding/hex"
	"hash/fnv"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// inlineKeyLimit is the largest key list bound one placeholder per key.
const inlineKeyLimit = 256

// keyMembership renders col [NOT] IN keys. A short list binds one
// placeholder per key; a larger set (step aside excludes every title a
// Plex Media Server holds, often tens of thousands) binds a single JSON
// array, so the statement, its plan cache entry and the bind cost stay
// small and the database builds one lookup index for the whole set.
func (c *Catalog) keyMembership(col string, not bool, keys []core.RatingKey) (string, []any) {
	op := " IN "
	if not {
		op = " NOT IN "
	}
	if len(keys) <= inlineKeyLimit {
		args := make([]any, len(keys))
		for i, k := range keys {
			args[i] = k
		}
		return col + op + "(" + placeholders(len(keys)) + ")", args
	}
	b := make([]byte, 0, len(keys)*18+2)
	b = append(b, '[')
	for i, k := range keys {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendInt(b, int64(k), 10)
	}
	b = append(b, ']')
	if store.DialectOf(c.db) == store.Postgres {
		return col + op + "(SELECT CAST(value AS BIGINT) FROM json_array_elements_text(CAST(? AS JSON)))", []any{string(b)}
	}
	return col + op + "(SELECT value FROM json_each(?))", []any{string(b)}
}

// keySetDigest identifies a key set for cache keys without formatting it.
func keySetDigest(keys []core.RatingKey) string {
	h := fnv.New128a()
	var buf [8]byte
	for _, k := range keys {
		binary.LittleEndian.PutUint64(buf[:], uint64(k))
		h.Write(buf[:])
	}
	return strconv.Itoa(len(keys)) + ":" + hex.EncodeToString(h.Sum(nil))
}

// digestExcluded drops a large exclusion set from a filter about to be
// formatted into a cache key; excludedKey supplies its digest instead.
func digestExcluded(f core.Filter) core.Filter {
	if len(f.ExcludeKeys) > inlineKeyLimit {
		f.ExcludeKeys = nil
	}
	return f
}

// excludedKey is the cache-key suffix for a large exclusion set.
func excludedKey(f core.Filter) string {
	if len(f.ExcludeKeys) > inlineKeyLimit {
		return "|excluded:" + keySetDigest(f.ExcludeKeys)
	}
	return ""
}

