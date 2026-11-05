package extend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/httpjson"
	"net/http"
	"strconv"
	"strings"
)

// TextResolver looks up a plugin string for a locale. It returns false when
// neither the locale nor English has the key.
type TextResolver func(plugin, key, locale string) (string, bool)

// Response is the body of GET /api/extend/registry.
type Response struct {
	Version     uint64       `json:"version"`
	Descriptors []Descriptor `json:"descriptors"`
}

// Handler serves the registry to the owner console. Query parameters:
// kinds (comma separated; all when absent) and locale (for plugin strings).
// The ETag hashes the resolved response. A process-local version alone can
// repeat after restart and wrongly retain old plugin labels or capabilities.
func Handler(r *Registry, resolve TextResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
			return
		}
		var kinds []Kind
		for _, raw := range strings.Split(req.URL.Query().Get("kinds"), ",") {
			if raw = strings.TrimSpace(raw); raw == "" {
				continue
			}
			k := Kind(raw)
			if !Known(k) {
				writeError(w, http.StatusBadRequest, "invalid", "unknown descriptor kind "+strconv.Quote(raw))
				return
			}
			kinds = append(kinds, k)
		}
		locale := strings.TrimSpace(req.URL.Query().Get("locale"))
		version := r.Version()
		ds := r.List(kinds...)
		for i := range ds {
			ds[i].Label = resolveText(ds[i].Label, locale, resolve)
			ds[i].Hint = resolveText(ds[i].Hint, locale, resolve)
			if ds[i].Owner != Builtin && len(ds[i].Meta) > 0 && resolve != nil {
				ds[i].Meta = resolveMeta(ds[i].Meta, ds[i].Owner, locale, resolve)
			}
		}
		body, err := json.Marshal(Response{Version: version, Descriptors: ds})
		if err != nil {
			writeError(w, 500, "invalid_registry", "The registry could not be encoded.")
			return
		}
		hash := sha256.Sum256(body)
		etag := `"` + hex.EncodeToString(hash[:]) + `"`
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", etag)
		if req.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(append(body, '\n'))
	})
}

func resolveText(t Text, locale string, resolve TextResolver) Text {
	if t.Plugin == "" || t.Key == "" || resolve == nil {
		return t
	}
	if s, ok := resolve(t.Plugin, t.Key, locale); ok {
		t.Resolved = s
	}
	return t
}

// resolveMeta resolves plugin copy anywhere inside a plugin descriptor's
// Meta: Text objects ({"plugin","key"}) gain "resolved", and "$t:key"
// strings are replaced by the plugin's string for the locale. Keys that do
// not resolve are left as they are, so a missing translation is visible.
func resolveMeta(raw json.RawMessage, plugin, locale string, resolve TextResolver) json.RawMessage {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(resolveValue(v, plugin, locale, resolve))
	if err != nil {
		return raw
	}
	return out
}

func resolveValue(v any, plugin, locale string, resolve TextResolver) any {
	switch t := v.(type) {
	case string:
		if key, ok := strings.CutPrefix(t, "$t:"); ok && key != "" {
			if s, ok := resolve(plugin, key, locale); ok {
				return s
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = resolveValue(t[i], plugin, locale, resolve)
		}
		return t
	case map[string]any:
		if p, ok := t["plugin"].(string); ok && p != "" {
			if key, ok := t["key"].(string); ok && key != "" && isTextObject(t) {
				if s, ok := resolve(p, key, locale); ok {
					t["resolved"] = s
				}
				return t
			}
		}
		for k, x := range t {
			t[k] = resolveValue(x, plugin, locale, resolve)
		}
		return t
	default:
		return v
	}
}

// isTextObject reports whether m has only Text fields.
func isTextObject(m map[string]any) bool {
	for k := range m {
		switch k {
		case "source", "plugin", "key", "resolved":
		default:
			return false
		}
	}
	return true
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	httpjson.Write(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}
