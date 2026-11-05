package stremio

import (
	"encoding/json"
	"errors"
)

// DecodeManifest parses a manifest document.
func DecodeManifest(body []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, errors.New("stremio: the add-on manifest is not valid JSON")
	}
	if !m.Valid() {
		return Manifest{}, errors.New("stremio: the link did not answer with an add-on manifest")
	}
	return m, nil
}
