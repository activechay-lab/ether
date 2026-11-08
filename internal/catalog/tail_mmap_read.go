package catalog

import "os"

// readFile is the fallback when a file cannot be mapped: the index lives
// in the heap instead (about 110 MB for the full export).
func readFile(path string) ([]byte, func() error, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return data, func() error { return nil }, nil
}
