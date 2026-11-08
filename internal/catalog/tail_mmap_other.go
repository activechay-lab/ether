//go:build !unix && !windows

package catalog

func mapFile(path string) ([]byte, func() error, error) { return readFile(path) }
