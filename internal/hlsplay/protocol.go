package hlsplay

// Resolve resolves a frontend-authored media URL against the measured origin.
func Resolve(base, raw string) (string, error) { return resolve(base, raw) }
