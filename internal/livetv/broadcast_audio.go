package livetv

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
	"github.com/braedonsaunders/ether/internal/trackprefs"
)

// programmeAudioTrack picks which audio track a channel airs for a
// programme, counted among the source's audio streams: the title's original
// language first, then the server's catalogue language, then the file's
// default track, then the first. A Blu-ray remux often lists a dub first, and
// a channel has no viewer to choose a track, so "first audio" put foreign
// dubs on air. Without probed streams it keeps the first track.
func (s *Service) programmeAudioTrack(ctx context.Context, info core.MediaInfo, key core.RatingKey) int {
	original := ""
	if s.o.Catalog != nil && key != 0 {
		if t, err := s.o.Catalog.Get(ctx, key); err == nil && t != nil {
			original = t.Language
		}
	}
	server := ""
	if s.o.AudioLanguage != nil {
		server = s.o.AudioLanguage()
	}
	return audioTrackFor(info, trackprefs.AudioOrder(nil, server, original, true))
}

func audioTrackFor(info core.MediaInfo, want []string) int {
	best, bestScore, n := 0, -1, 0
	for _, st := range info.Streams {
		if st.Type != "audio" {
			continue
		}
		score := 0
		for i, w := range want {
			if rank := langtag.BestStreamMatchRank(w, st.Language, st.Title); rank > 0 {
				score = (len(want)-i)*300 + rank*40
				break
			}
		}
		if st.Default {
			score += 5
		}
		if score > bestScore {
			best, bestScore = n, score
		}
		n++
	}
	return best
}
