package onair

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/braedonsaunders/ether/internal/onair/families"
	"github.com/braedonsaunders/ether/internal/onair/fonts"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"net/http"
	"sort"
)

// MCP exclusion: these owner-authenticated routes supply immutable rendering
// resources to the console. They do not expose owner records or mutations;
// fetching binary fonts and bundled scene data has no useful agent tool analogue.
func ServeFont(w http.ResponseWriter, r *http.Request) {
	data := fonts.Bytes(r.PathValue("id"))
	tag := fmt.Sprintf(`"%x"`, sha256.Sum256(data))
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Type", "font/ttf")
	if r.Header.Get("If-None-Match") == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(data)
}
func ServeFamilies(w http.ResponseWriter, r *http.Request) {
	type family struct {
		ID        string                    `json:"id"`
		Name      string                    `json:"name"`
		Documents map[string]scene.Document `json:"documents"`
		Bindings  scene.Bindings            `json:"bindings"`
	}
	names := families.Names()
	ids := make([]string, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]family, 0, len(ids))
	for _, id := range ids {
		f := family{ID: id, Name: id, Documents: map[string]scene.Document{}, Bindings: families.Defaults(id)}
		for _, moment := range names[id] {
			d, err := families.Get(id, moment)
			if err != nil {
				http.Error(w, "invalid built-in scene", 500)
				return
			}
			f.Documents[moment] = d
		}
		out = append(out, f)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
