package admin

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (h *Handler) posterStatus(w http.ResponseWriter, r *http.Request) {
	if !h.isOwner(r, tokenFromRequest(r)) {
		writeError(w, 403, "forbidden", "Owner access required")
		return
	}
	if h.deps.OwnerArtwork == nil {
		writeError(w, 503, "unavailable", "Artwork unavailable")
		return
	}
	count, last, limited, err := h.deps.OwnerArtwork.OwnerStatus()
	if err != nil {
		writeError(w, 500, "artwork_status", "Could not read poster status")
		return
	}
	folder := h.deps.Config.Get().Posters.AssetFolder
	found := 0
	folderLimited := false
	if folder != "" {
		if f, e := os.Open(folder); e == nil {
			entries, _ := f.ReadDir(10001)
			f.Close()
			folderLimited = len(entries) > 10000
			for _, entry := range entries {
				if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), ".jpg") {
					found++
				}
				if entry.IsDir() {
					if info, e := os.Lstat(filepath.Join(folder, entry.Name(), "poster.jpg")); e == nil && info.Mode().IsRegular() {
						found++
					}
				}
			}
		}
	}
	var stamp any
	if !last.IsZero() {
		stamp = last
	}
	writeJSON(w, 200, map[string]any{"uploaded": count, "lastUpload": stamp, "limited": limited, "folder": folder, "found": found, "folderLimited": folderLimited})
}

func (h *Handler) clearPosters(w http.ResponseWriter, r *http.Request) {
	if !h.isOwner(r, tokenFromRequest(r)) {
		writeError(w, 403, "forbidden", "Owner access required")
		return
	}
	if h.deps.OwnerArtwork == nil {
		writeError(w, 503, "unavailable", "Artwork unavailable")
		return
	}
	more, err := h.deps.OwnerArtwork.ClearOwner()
	if err != nil {
		writeError(w, 500, "artwork_clear", "Could not clear uploaded artwork")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true, "more": more})
}
