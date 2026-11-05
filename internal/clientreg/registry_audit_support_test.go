package clientreg

import (
	_ "embed"
)

// Version is the embedded registry generation.
func Version() int { return loaded.Version }

// Matches reports whether m matches id.
func Matches(m Match, id Identity) bool { return m.ok(id) }
