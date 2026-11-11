package livetv

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// programmeAudioTrack picks which audio track a channel airs for a programme,
// counted among the source's audio streams. It follows the same stream
// preferences as normal playback for the user the channel runs as (the
// viewer of a personal channel, otherwise the owner): their audio languages,
// "prefer original", remembered per-show choices, the shared defaults and
// their plex.tv account languages. Without that service, or when nothing
// matches, the file's default audio track airs.
func (s *Service) programmeAudioTrack(ctx context.Context, user *core.User, info core.MediaInfo, key core.RatingKey) int {
	pos := -1
	if s.o.AudioTrack != nil && user != nil {
		original := ""
		if s.o.Catalog != nil && key != 0 {
			if t, err := s.o.Catalog.Get(ctx, key); err == nil && t != nil {
				original = t.Language
			}
		}
		pos = s.o.AudioTrack(ctx, user, key, info, original)
	}
	return audioIndexFor(info, pos)
}

// audioIndexFor converts a position in info.Streams to the index among the
// audio streams that ffmpeg's 0:a:N expects. An invalid position falls back
// to the default audio track, then the first.
func audioIndexFor(info core.MediaInfo, pos int) int {
	n, def := 0, -1
	for i, st := range info.Streams {
		if st.Type != "audio" {
			continue
		}
		if i == pos {
			return n
		}
		if def < 0 && st.Default {
			def = n
		}
		n++
	}
	return max(def, 0)
}
