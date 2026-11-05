package transcode

import (
	"time"
)

// mediaPlaylist renders the full-length VOD playlist for a span, so clients
// can seek anywhere before ffmpeg has produced a thing.
func mediaPlaylist(span, segDur time.Duration, container string, name func(n int) string) []byte {
	return mediaPlaylistWithIndependence(span, segDur, container, name, true)
}
