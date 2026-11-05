package contentrating

import ()

// Rated reports whether the rating has an age level.
func (r Rating) Rated() bool { return r.Level != Unrated }
