package admin

import (
	"github.com/braedonsaunders/ether/internal/artwork"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (h *Handler) posterFolders(w http.ResponseWriter, r *http.Request) {
	if !h.isOwner(r, tokenFromRequest(r)) {
		writeError(w, 403, "forbidden", "Owner access required")
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		cwd, _ := os.Getwd()
		path = filepath.VolumeName(cwd) + string(filepath.Separator)
	}
	path, err := artwork.AssetFolder(path)
	if err != nil {
		writeError(w, 400, "invalid_folder", "Folder unavailable")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, 400, "invalid_folder", "Folder unavailable")
		return
	}
	defer f.Close()
	entries, err := f.ReadDir(10001)
	if err != nil && err != io.EOF && len(entries) == 0 {
		writeError(w, 400, "invalid_folder", "Folder unavailable")
		return
	}
	dirs := []string{}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
			if st, e := os.Lstat(filepath.Join(path, entry.Name(), "poster.jpg")); e == nil && st.Mode().IsRegular() {
				count++
			}
		}
		if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".jpg") {
			count++
		}
	}
	crumbs := []map[string]string{}
	for p := path; ; p = filepath.Dir(p) {
		crumbs = append([]map[string]string{{"name": filepath.Base(p), "path": p}}, crumbs...)
		if filepath.Dir(p) == p {
			break
		}
	}
	sort.Strings(dirs)
	writeJSON(w, 200, map[string]any{"breadcrumbs": crumbs, "path": path, "parent": filepath.Dir(path), "directories": dirs, "posters": count, "limited": len(entries) > 10000})
}
