package service

import (
	"bytes"
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"image"
	"image/png"
	"testing"
	"time"
)

func TestOwnerArtworkPriorityAndAuthorization(t *testing.T) {
	key := core.MovieKey(550)
	owner := &core.User{ID: 1, IsOwner: true}
	policy := &artworkPolicy{viewer: owner}
	source := Source{OwnerDir: t.TempDir(), Catalog: storedArtwork{key: {Key: key, Type: core.TypeMovie, PosterPath: "/remote.jpg", Released: time.Unix(1, 0)}}, Visibility: experience.Visibility{Policy: policy}}
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	req := frontend.ArtworkRequest{Viewer: owner, Item: key}
	viewer := *owner
	viewer.IsOwner = false
	denied := req
	denied.Viewer = &viewer
	if err := source.Upload(context.Background(), denied, frontend.ImagePoster, b.Bytes(), "image/png"); err == nil {
		t.Fatal("viewer upload accepted")
	}
	if err := source.Upload(context.Background(), req, frontend.ImagePoster, b.Bytes(), "image/jpeg"); err == nil {
		t.Fatal("mismatched type accepted")
	}
	if err := source.Upload(context.Background(), req, frontend.ImagePoster, b.Bytes(), "image/png"); err != nil {
		t.Fatal(err)
	}
	entries, _, err := source.List(context.Background(), req)
	if err != nil || len(entries) == 0 {
		t.Fatalf("list: %v", err)
	}
	data, ct, err := source.Read(context.Background(), req, entries[0], frontend.ArtworkTransform{})
	if err != nil || ct != "image/png" || !bytes.Equal(data, b.Bytes()) {
		t.Fatalf("override not selected: %s %v", ct, err)
	}
	policy.viewer = nil
	if _, _, err = source.Read(context.Background(), req, entries[0], frontend.ArtworkTransform{}); err == nil {
		t.Fatal("revoked viewer read accepted")
	}
}
