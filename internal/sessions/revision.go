package sessions

import "github.com/braedonsaunders/ether/internal/core"

func sourceRevision(s *core.PlaySession) string {
	if s != nil && s.Source != nil {
		if remote, ok := s.Source.(interface{ SourceRevision() string }); ok {
			return remote.SourceRevision()
		}
	}
	return core.SourceRevision(s)
}
