package resolver

import ()

func (a qualityRank) better(b qualityRank) bool {
	if a.height != b.height {
		return a.height > b.height
	}
	if a.source != b.source {
		return a.source > b.source
	}
	return a.hdr > b.hdr
}
