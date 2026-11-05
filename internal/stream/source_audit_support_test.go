package stream

import ()

// schedule starts read-ahead fetches for [from, to) without blocking and
// returns how far it got (the start of the first slot it could not start).
func (s *source) schedule(from, to int64) int64 { return s.scheduleCursor(from, to, nil) }
