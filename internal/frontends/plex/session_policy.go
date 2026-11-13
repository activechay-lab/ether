package plex

import "github.com/braedonsaunders/ether/internal/core"

// SessionPolicy preserves adapter admission and source ownership.
func SessionPolicy() core.SessionPolicy { return core.SessionPolicy{ExternalAdmission: true} }
