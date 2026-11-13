package mediabrowser

import (
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"net/http"
	"strings"
)

func (i *instance) uploadImage(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil || !v.IsOwner || v.Delegated {
		http.Error(w, "Forbidden", 403)
		return
	}
	key, err := ids.ParseRatingKeyGUID(r.PathValue("itemId"))
	uploader, ok := i.host.Artwork.(frontend.ArtworkUploadHandler)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	kind := frontend.ImagePoster
	switch strings.ToLower(r.PathValue("imageType")) {
	case "primary":
	case "backdrop":
		kind = frontend.ImageBackdrop
	default:
		http.Error(w, "Unsupported image type", 400)
		return
	}
	if err := uploader.UploadRequest(w, r, frontend.ArtworkRequest{Viewer: v, Item: key, Device: device(r)}, kind, true); err != nil {
		http.Error(w, "Invalid image", 400)
		return
	}
	w.WriteHeader(204)
}
