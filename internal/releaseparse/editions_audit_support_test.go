package releaseparse

// NormalizeEdition maps an edition label found in a release name (or an
// already-normalized one) to the canonical cut name, "" when there is no
// explicit cut. Two releases belong to the same cut when their normalized
// editions match.
func NormalizeEdition(name string) string { return ParseEdition(name) }
