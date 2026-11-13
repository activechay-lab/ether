package plex

import "github.com/braedonsaunders/ether/internal/frontend"

// SessionPolicy preserves adapter admission and source ownership.
func SessionPolicy() frontend.SessionPolicy { return frontend.SessionPolicy{ExternalAdmission: true} }
