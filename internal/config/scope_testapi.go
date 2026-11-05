// These helpers support offline acceptance tests across package boundaries.
// Runtime callers use the production APIs in the other files.
package config

import (
	"encoding/json"
)

// LocalJSON is the node-local part of c as JSON (unsealed).
func LocalJSON(c Config) []byte {
	_, local := splitDoc(toDoc(c))
	b, _ := json.Marshal(local)
	return b
}
