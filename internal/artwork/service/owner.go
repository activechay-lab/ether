package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Owner uploads are durable source images, separate from disposable transforms.
func (a Source) ownerPath(key core.RatingKey, kind string) string {
	if a.OwnerDir == "" {
		return ""
	}
	return filepath.Join(a.OwnerDir, key.String()+"-"+kind)
}
func (a Source) ownerImage(ctx context.Context, key core.RatingKey, kind string) ([]byte, string) {
	p := a.selectedOwnerPath(ctx, key, kind)
	if p == "" {
		return nil, ""
	}
	info, err := os.Stat(p)
	if err != nil || info.Size() > MaxImageBytes {
		return nil, ""
	}
	return nil, fmt.Sprintf("owner:%d:%d", info.ModTime().UnixNano(), info.Size())
}
func (a Source) Upload(ctx context.Context, req frontend.ArtworkRequest, kind string, data []byte, ct string) error {
	if req.Viewer == nil || !req.Viewer.IsOwner || req.Viewer.Delegated {
		return core.ErrUnauthorized
	}
	if kind != frontend.ImagePoster && kind != frontend.ImageBackdrop {
		return core.ErrNotAvailable
	}
	t, err := a.title(ctx, req.Item)
	if err != nil || t == nil || !a.visible(ctx, req.Viewer, t) {
		return core.ErrNotFound
	}
	if len(data) == 0 || len(data) > MaxImageBytes || (ct != "image/jpeg" && ct != "image/png" && ct != "image/webp") || http.DetectContentType(data) != ct {
		return core.ErrNotAvailable
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
		return core.ErrNotAvailable
	}
	p := a.ownerPath(req.Item, kind)
	if p == "" {
		return core.ErrNotAvailable
	}
	if err := os.MkdirAll(a.OwnerDir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(a.OwnerDir, ".upload-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

// UploadRequest shares bounded decoding and URL fetching between adapters.
func (a Source) UploadRequest(w http.ResponseWriter, r *http.Request, req frontend.ArtworkRequest, kind string, encoded bool) error {
	if req.Viewer == nil || !req.Viewer.IsOwner || req.Viewer.Delegated {
		return core.ErrUnauthorized
	}
	var data []byte
	var err error
	ct := strings.Split(r.Header.Get("Content-Type"), ";")[0]
	if u := r.URL.Query().Get("url"); u != "" {
		if a.URLImages == nil {
			return core.ErrNotAvailable
		}
		data, ct, err = a.URLImages.GetUntrustedURL(r.Context(), u)
	} else {
		var body io.Reader = http.MaxBytesReader(w, r.Body, MaxImageBytes*2)
		if encoded {
			body = base64.NewDecoder(base64.StdEncoding, body)
		}
		data, err = io.ReadAll(io.LimitReader(body, MaxImageBytes+1))
	}
	if err != nil {
		return err
	}
	return a.Upload(r.Context(), req, kind, data, ct)
}

func (a Source) OwnerArtwork(ctx context.Context, req frontend.ArtworkRequest, kind string) ([]byte, string, error) {
	t, err := a.title(ctx, req.Item)
	if err != nil || req.Viewer == nil || t == nil || !t.IsReleased(a.now()) || !a.visible(ctx, req.Viewer, t) {
		return nil, "", core.ErrNotFound
	}
	if kind != frontend.ImagePoster && kind != frontend.ImageBackdrop {
		return nil, "", core.ErrNotFound
	}
	p := a.selectedOwnerPath(ctx, req.Item, kind)
	if p == "" {
		return nil, "", core.ErrNotFound
	}
	var f *os.File
	if p != a.ownerPath(req.Item, kind) && a.AssetFolder != nil {
		folder := a.AssetFolder()
		root, e := os.OpenRoot(folder)
		if e != nil {
			return nil, "", e
		}
		defer root.Close()
		relative, e := filepath.Rel(folder, p)
		if e != nil {
			return nil, "", e
		}
		f, err = root.Open(relative)
	} else {
		f, err = os.Open(p)
	}
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if err != nil || len(data) > MaxImageBytes {
		return nil, "", core.ErrNotAvailable
	}
	ct := http.DetectContentType(data)
	cfg, _, e := image.DecodeConfig(bytes.NewReader(data))
	if e != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40000000 || (ct != "image/jpeg" && ct != "image/png" && ct != "image/webp") {
		return nil, "", core.ErrNotAvailable
	}
	return data, ct, nil
}

func (a Source) selectedOwnerPath(ctx context.Context, key core.RatingKey, kind string) string {
	p := a.ownerPath(key, kind)
	if p != "" {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p
		}
	}
	if kind == frontend.ImagePoster && a.Assets != nil && a.AssetFolder != nil {
		if t, err := a.title(ctx, key); err == nil {
			return a.Assets.Path(a.AssetFolder(), t)
		}
	}
	return ""
}

// OwnerArtworkVersion uses file metadata, never reading image bytes on a list.
func (a Source) OwnerArtworkVersion(t *core.Title, kind string) string {
	if t == nil {
		return ""
	}
	_, stamp := a.ownerImage(frontend.WithArtworkTitle(context.Background(), t), t.Key, kind)
	if stamp == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(stamp))
	return fmt.Sprintf("%x", sum[:8])
}
