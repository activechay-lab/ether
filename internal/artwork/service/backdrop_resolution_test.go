package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/discovery"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/overlays"
	"github.com/braedonsaunders/ether/internal/store"
)

// Higher resolution must retain the owner's selected backdrop identity.
func TestSelectedBackdropResolution(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	over := overlays.New(db, overlays.Options{})
	if err := over.SaveSettings(ctx, overlays.Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	key := core.MovieKey(9)
	if err := over.SaveArtwork(ctx, key, overlays.Artwork{PinnedBackdrop: "/selected.jpg"}); err != nil {
		t.Fatal(err)
	}
	title := &core.Title{Key: key, Type: core.TypeMovie, BackdropPath: "/default.jpg", Released: time.Unix(1, 0)}
	var fetched string
	images := NewRemoteCache(1024, &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
		fetched = r.URL.Path
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/jpeg"}}, Body: io.NopCloser(strings.NewReader("art"))}, nil
	})})
	src := Source{Catalog: storedTitle{title: title}, Discovery: (&discovery.Features{Overlays: over}).Frontend(), Images: images, ImageURL: func(path, size string) string { return "https://fixture.invalid/" + size + path }}
	req := frontend.ArtworkRequest{Viewer: &core.User{ID: 1}, Item: key}
	list, _, err := src.List(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, img := range list {
		if img.Kind != frontend.ImageBackdrop {
			continue
		}
		if _, _, err := src.Read(ctx, req, img, frontend.ArtworkTransform{Width: 2688}); err != nil {
			t.Fatal(err)
		}
		if fetched != "/original/selected.jpg" {
			t.Fatal("undersized or wrong selected source", fetched)
		}
		return
	}
	t.Fatal("missing selected backdrop")
}
