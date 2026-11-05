// These helpers support offline acceptance tests across package boundaries.
// Runtime callers use the production APIs in the other files.
package stremio

import (
	"encoding/json"
)

// ParseResources reads a resources JSON array (tests and fixtures).
func ParseResources(raw string) Resources {
	var rs Resources
	_ = json.Unmarshal([]byte(raw), &rs)
	return rs
}
