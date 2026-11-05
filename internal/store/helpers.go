package store

import (
	"iter"
	"slices"
	"strings"
)

// Placeholders builds an IN-list. Empty lists use NULL; callers should guard them.
func Placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	return strings.Repeat("?,", n-1) + "?"
}

// Chunks iterates bounded batches without copying the source slice.
func Chunks[T any](items []T, size int) iter.Seq[[]T] { return slices.Chunk(items, size) }

// ForUpdate is the row locking suffix for the selected database.
func ForUpdate(d Dialect) string {
	if d == Postgres {
		return " FOR UPDATE"
	}
	return ""
}

// BoolInt is the portable SQL boolean representation.
func BoolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
