package backup

import ()

// UpdateReport changes the saved report, if there is one.
func UpdateReport(dataDir string, fn func(*Report)) error {
	r, err := ReadReport(dataDir)
	if err != nil || r == nil {
		return err
	}
	fn(r)
	return saveReport(dataDir, r)
}
