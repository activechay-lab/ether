package transcode

import ()

// prepareRunEdits reads run r's init once and, when retained is set and
// differs, rewrites the init's edits onto it and records the decode time
// corrections. With no retained clock yet the run is left as written: its
// init, once published, becomes the retained clock.
func prepareRunEdits(r *run, retained map[string]trackEdit) error {
	return prepareRunEditsForPath(r, retained, "")
}
