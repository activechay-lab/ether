package core

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// TrickplayCacheKey binds preview work to the selected item, release and
// opened source member. An expired local revision is not safe to serve.
func TrickplayCacheKey(s *PlaySession) string {
	if s == nil || s.Resolution == nil {
		return ""
	}
	release := s.Resolution.Release.ID
	if release == "" {
		return ""
	}
	// Older embedded playback adapters expose only a logical release descriptor.
	if s.Source == nil {
		return release
	}
	revision := SourceRevision(s)
	if remote, ok := s.Source.(interface{ SourceRevision() string }); ok {
		revision = remote.SourceRevision()
	}
	if revision == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strconv.FormatInt(int64(s.Key), 10) + "\x00" + release + "\x00" + revision))
	return "preview-" + hex.EncodeToString(sum[:16])
}
