package kit

import (
	"github.com/braedonsaunders/ether/internal/plugins"
)

// FromFiles builds an in-memory Package from raw manifest JSON and a file map.
func FromFiles(manifestRaw []byte, files map[string][]byte) (*Package, error) {
	outFiles := make(map[string][]byte, len(files)+1)
	for k, v := range files {
		outFiles[k] = v
	}
	outFiles["manifest.json"] = manifestRaw
	man, err := plugins.ParseManifest(manifestRaw)
	if err != nil {
		return &Package{ManifestRaw: manifestRaw, Files: outFiles}, err
	}
	return &Package{ManifestRaw: manifestRaw, Manifest: man, Files: outFiles}, nil
}
