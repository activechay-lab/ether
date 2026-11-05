package subtitles

import (
	"encoding/json"

	"os"

	"path/filepath"

	"github.com/braedonsaunders/ether/internal/core"
)

// unzipSubtitle is retained for callers without candidate style metadata.
func unzipSubtitle(raw []byte, lang string) ([]byte, error) {
	return unzipSubtitleCandidate(raw, Candidate{Language: lang})
}
func (s *Service) saveSelection(title *core.Title, lang, release string, sel selection, prefs ...Preferences) {
	if s.dir == "" {
		return
	}
	p := s.indexPath(title, lang, release, prefs...)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		s.log.Debug("subtitle index dir", "err", err)
		return
	}
	b, _ := json.Marshal(sel)
	if err := writeFileAtomic(p, b); err != nil {
		s.log.Debug("subtitle index write", "err", err)
	}
}
