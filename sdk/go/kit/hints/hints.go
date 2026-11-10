// Package hints marshals typed resolver hints for KindItem.Hints.
// Hints are opaque to Ether, stored verbatim and echoed back at resolve
// time; put ids and locators here, never secrets.
package hints

import (
	"encoding/json"
	"fmt"
)

// MaxBytes is Ether's hints cap.
const MaxBytes = 16 << 10

// Marshal encodes a hints struct as the JSON object KindItem.Hints holds.
func Marshal(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxBytes {
		return nil, fmt.Errorf("hints: %d bytes exceeds the %d byte cap", len(b), MaxBytes)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("hints: value must encode as a JSON object")
	}
	return b, nil
}

// Unmarshal decodes hints into a typed struct; empty hints leave v alone.
func Unmarshal(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// Fields encodes a value map as KindItem.Fields (key -> raw JSON value).
func Fields(values map[string]any) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(values))
	for k, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("hints: field %q: %w", k, err)
		}
		out[k] = b
	}
	return out, nil
}
