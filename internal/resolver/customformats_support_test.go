package resolver

import (
	"bytes"

	"encoding/json"

	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
)

// ImportCustomFormats accepts a native single definition or an array, validating
// before returning. Callers persist the result in config.Resolver.CustomFormats.
func ImportCustomFormats(data []byte) ([]core.CustomFormat, error) {
	if len(data) > 8<<20 {
		return nil, fmt.Errorf("custom formats: input exceeds 8 MiB")
	}
	data = bytes.TrimSpace(data)
	var defs []core.CustomFormat
	if len(data) > 0 && data[0] == '[' {
		if err := json.Unmarshal(data, &defs); err != nil {
			return nil, err
		}
	} else {
		var d core.CustomFormat
		if err := json.Unmarshal(data, &d); err != nil {
			return nil, err
		}
		defs = []core.CustomFormat{d}
	}
	_, err := NewFormatScorer(defs, nil)
	return defs, err
}
