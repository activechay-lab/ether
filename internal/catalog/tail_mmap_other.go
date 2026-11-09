//go:build !unix && !windows

package catalog

import "os"

func mapFile(path string) ([]byte, func() error, error) { return readFile(path) }

// readFile supports platforms without a mapping implementation. Unix and
// Windows never use this heap-backed fallback after a mapping failure.
func readFile(path string) ([]byte, func() error, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return data, func() error { return nil }, nil
}
