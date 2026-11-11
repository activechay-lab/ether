package plugins

import "strings"

// pluginText resolves a manifest label: plain text is returned as is, and a
// "$t:key" reference is looked up in the plugin's own strings (English
// first, then any locale that has it). An unknown key falls back to the key
// so a raw "$t:" never reaches the console.
func pluginText(strs map[string]map[string]string, val string) string {
	key, ok := strings.CutPrefix(strings.TrimSpace(val), "$t:")
	if !ok {
		return val
	}
	if v := strings.TrimSpace(strs["en"][key]); v != "" {
		return v
	}
	for _, doc := range strs {
		if v := strings.TrimSpace(doc[key]); v != "" {
			return v
		}
	}
	return key
}
