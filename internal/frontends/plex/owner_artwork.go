package plex

import (
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"net/http"
	"strings"
)

func (s *Server) handleOwnerArtwork(w http.ResponseWriter, r *http.Request) {
	key, err := keyParam(r)
	if err == nil && key.Kind() == frontend.KindPlaylist {
		s.handlePlaylistMetadataRefusal(w, r)
		return
	}
	uploader, ok := s.getHost().Artwork.(frontend.ArtworkUploadHandler)
	kind := frontend.ImagePoster
	leaf := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if leaf == "art" || leaf == "arts" {
		kind = frontend.ImageBackdrop
	} else if leaf != "poster" && leaf != "posters" {
		writeHTTPError(w, 400)
		return
	}
	if err != nil || !ok {
		writeHTTPError(w, 404)
		return
	}
	req := frontend.ArtworkRequest{Viewer: FromContext(r.Context()).User, Item: key}
	if r.Method == http.MethodPut && r.URL.Query().Get("url") != "" {
		element := "thumb"
		if kind == frontend.ImageBackdrop {
			element = "art"
		}
		if r.URL.Query().Get("url") == "/library/metadata/"+key.String()+"/"+element {
			if _, _, e := s.getHost().Artwork.List(r.Context(), req); e != nil {
				writeHTTPError(w, 404)
				return
			}
			s.writeEmpty(w, r)
			return
		}
	}
	if err := uploader.UploadRequest(w, r, req, kind, false); err != nil {
		writeHTTPError(w, 400)
		return
	}
	s.writeEmpty(w, r)
}

func (s *Server) ownerArtworkCandidates(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/posters") && !strings.HasSuffix(r.URL.Path, "/arts") {
		writeHTTPError(w, 404)
		return
	}
	key, err := keyParam(r)
	art := s.getHost().Artwork
	if err != nil || art == nil {
		writeHTTPError(w, 404)
		return
	}
	images, _, err := art.List(r.Context(), frontend.ArtworkRequest{Viewer: FromContext(r.Context()).User, Item: key})
	if err != nil {
		writeHTTPError(w, 404)
		return
	}
	kind, element := frontend.ImagePoster, "thumb"
	if strings.HasSuffix(r.URL.Path, "/arts") {
		kind, element = frontend.ImageBackdrop, "art"
	}
	mc := libraryContainer()
	for _, img := range images {
		if img.Kind != kind {
			continue
		}
		path := "/library/metadata/" + key.String() + "/" + element
		mc.Metadata = append(mc.Metadata, model.Metadata{RatingKey: img.Path, Key: path, Thumb: path, Extra: []model.Attr{model.A("selected", true)}})
		break
	}
	mc.Size = len(mc.Metadata)
	s.write(w, r, mc)
}
