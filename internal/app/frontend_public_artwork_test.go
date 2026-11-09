package app

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type publicArtworkLister struct{ lists int }

func (l *publicArtworkLister) List(context.Context, frontend.ArtworkRequest) ([]frontend.Image, bool, error) {
	l.lists++
	return []frontend.Image{
		{Kind: frontend.ImagePoster, Path: "poster"},
		{Kind: frontend.ImagePerson, Path: "portrait", Subject: "tmdb:1"},
	}, true, nil
}
func (*publicArtworkLister) Read(context.Context, frontend.ArtworkRequest, frontend.Image, frontend.ArtworkTransform) ([]byte, string, error) {
	return []byte("image"), "image/jpeg", nil
}

// Public artwork is the intersection over every active viewer: one
// restricted profile withholds the title from anonymous requests, and
// removing that restriction (a new policy epoch) releases it without
// waiting for a cache TTL (#405).
func TestPublicArtworkIntersectionPerPolicyEpoch(t *testing.T) {
	title := &core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, ContentRating: "R"}
	get := func(_ context.Context, k core.RatingKey) (*core.Title, error) {
		if k == title.Key {
			return title, nil
		}
		return nil, core.ErrNotFound
	}
	svc, err := experience.NewService(experience.ServiceDeps{
		Active:    func(_ context.Context, id int64) (*core.User, error) { return &core.User{ID: id, IsOwner: true}, nil },
		Get:       func(ctx context.Context, _ *core.User, k core.RatingKey) (*core.Title, error) { return get(ctx, k) },
		CachedGet: get,
		Children:  func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { return nil, nil },
		State:     searchSummaryState{}, Rows: &experience.RowsService{}, Protocol: "jellyfin",
		Visibility: experience.Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }, GetTitle: get},
		Search:     experience.Search{Catalog: func(context.Context, string, []core.MediaType, int) ([]core.Title, error) { return nil, nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	owner := core.User{ID: 1, IsOwner: true}
	kid := core.User{ID: 2, MaxContentRating: "PG"}
	users := []core.User{owner, kid}
	epoch := uint64(1)
	lister := &publicArtworkLister{}
	s := &surfaceExperience{Service: svc, protocol: "jellyfin", artwork: lister,
		publicUsers: func(context.Context) ([]core.User, error) { return users, nil },
		publicEpoch: func() uint64 { return epoch }}
	ctx := context.Background()

	if _, _, ok, err := s.PublicArtwork(ctx, core.Device{}, title.Key); err != nil || ok {
		t.Fatalf("restricted profile leaked public artwork: ok=%v err=%v", ok, err)
	}
	// Same epoch: the verdict is cached even though policy inputs changed
	// underneath (the epoch is what licenses the cache).
	users = []core.User{owner}
	if _, _, ok, _ := s.PublicArtwork(ctx, core.Device{}, title.Key); ok {
		t.Fatal("same-epoch cache ignored")
	}
	epoch++
	images, reader, ok, err := s.PublicArtwork(ctx, core.Device{}, title.Key)
	if err != nil || !ok || reader == nil || reader.ID != owner.ID {
		t.Fatalf("unrestricted title not public: ok=%v reader=%v err=%v", ok, reader, err)
	}
	if len(images) != 1 || images[0].Kind != frontend.ImagePoster {
		t.Fatalf("non-title artwork leaked: %v", images)
	}
	lists := lister.lists
	if _, _, ok, _ := s.PublicArtwork(ctx, core.Device{}, title.Key); !ok || lister.lists != lists {
		t.Fatal("public verdict not cached within epoch")
	}
	if _, _, ok, _ := s.PublicArtwork(ctx, core.Device{}, core.CollectionKey(1)); ok {
		t.Fatal("collection became public")
	}
	if _, _, ok, _ := s.PublicArtwork(ctx, core.Device{}, core.MovieKey(404)); ok {
		t.Fatal("missing title became public")
	}
	users = nil
	epoch++
	if _, _, ok, _ := s.PublicArtwork(ctx, core.Device{}, title.Key); ok {
		t.Fatal("no-user server became public")
	}
}
