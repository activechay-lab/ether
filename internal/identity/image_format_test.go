package identity

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"testing"

	artworkservice "github.com/braedonsaunders/ether/internal/artwork/service"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type formatArtwork struct {
	frontendtest.Artwork
	data []byte
}

func (a formatArtwork) Read(_ context.Context, _ frontend.ArtworkRequest, _ frontend.Image, transform frontend.ArtworkTransform) ([]byte, string, error) {
	return artworkservice.Transform(a.data, "image/png", transform)
}

func TestMediaBrowserImageFormats(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	src.SetNRGBA(4, 2, color.NRGBA{R: 255, A: 128})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatal(err)
	}
	for _, surface := range []frontend.Protocol{"jellyfin", "emby"} {
		for _, kind := range []string{frontend.ImageLogo, frontend.ImagePoster} {
			for _, format := range []string{"Jpg", "jpg", "jpeg", "Png", "png", "Webp", "webp", "Gif", "gif", "Bmp", "bmp", "Svg", "svg", "unknown", ""} {
				t.Run(string(surface)+"/"+string(kind)+"/"+format, func(t *testing.T) {
					s := ImageGrantService{surface: surface, artwork: formatArtwork{data: encoded.Bytes()}}
					r := httptest.NewRequest("GET", "/?format="+format+"&maxWidth=4&quality=80&tag=v1", nil)
					w := httptest.NewRecorder()
					s.writeGrantedImage(w, r, nil, frontend.ArtworkRequest{}.Device, 0, "", frontend.Image{Kind: kind, Path: "/logo.png"}, "", "", "")
					if w.Code != 200 {
						t.Fatalf("status %d: %s", w.Code, w.Body.String())
					}
					wantType := "image/png"
					if format == "Jpg" || format == "jpg" || format == "jpeg" {
						wantType = "image/jpeg"
					}
					if got := w.Header().Get("Content-Type"); got != wantType {
						t.Fatalf("content type %s, want %s", got, wantType)
					}
					decoded, _, err := image.Decode(bytes.NewReader(w.Body.Bytes()))
					if err != nil {
						t.Fatal(err)
					}
					if decoded.Bounds().Dx() != 4 || decoded.Bounds().Dy() != 2 {
						t.Fatalf("bounds %v", decoded.Bounds())
					}
					if wantType == "image/png" {
						_, _, _, alpha := decoded.At(0, 0).RGBA()
						if alpha != 0 {
							t.Fatalf("lost logo transparency: alpha=%d", alpha)
						}
					}
				})
			}
		}
	}
}
