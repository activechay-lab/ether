package transcode

import ()

// segPath is the file of segment n of the first track.
func (s *Session) segPath(n int) string { return s.trackSegPath(0, n) }
