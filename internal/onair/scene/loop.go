package scene

import "encoding/json"

// ShortLoop fits an authored timeline into the outward half of a 16-second
// ping-pong loop. Mirrored sampling joins the end to the start continuously.
// Timeline metadata is retimed too, keeping bounded visibility ranges valid.
func ShortLoop(d Document) Document {
	scale := 8 / d.Duration
	data, _ := json.Marshal(d)
	var tree map[string]any
	if json.Unmarshal(data, &tree) != nil {
		return d
	}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if keys, ok := x["keys"].([]any); ok {
				for _, key := range keys {
					if a, ok := key.([]any); ok && len(a) > 0 {
						if n, ok := a[0].(float64); ok {
							a[0] = n * scale
						}
					}
				}
			}
			for k, v := range x {
				switch k {
				case "start", "end", "from", "to", "poster":
					if n, ok := v.(float64); ok {
						x[k] = n * scale
					}
				}
				walk(v)
			}
		case []any:
			for _, v := range x {
				walk(v)
			}
		}
	}
	walk(tree)
	tree["duration"] = 16
	data, _ = json.Marshal(tree)
	var out Document
	if json.Unmarshal(data, &out) != nil {
		return d
	}
	return out
}
