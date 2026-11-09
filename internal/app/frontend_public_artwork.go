package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Public title artwork (frontend.PublicArtwork) backs unauthenticated
// Jellyfin/Emby image requests that carry neither a tag nor a credential
// — Jellyfin documents the tag as optional, and clients such as Moonfin
// build image URLs with neither (#405). A title's artwork is public only
// when EVERY active viewer may see the title under current policy, so an
// anonymous fetch can never return art for a title hidden from anyone
// (a kids' profile, a restricted section, an owner rule). The user
// snapshot and per-item verdicts are cached per policy epoch: the epoch
// mixes the account, owner-policy (safety/config) and catalogue
// generations, so tightening any restriction drops the cache at once
// instead of waiting out a TTL.

type publicArtworkKey struct {
	item     core.RatingKey
	language string
}

type publicArtworkEntry struct {
	images []frontend.Image
	reader *core.Viewer
	ok     bool
}

type publicArtworkCache struct {
	epoch  uint64
	loaded bool
	users  []core.User
	items  map[publicArtworkKey]publicArtworkEntry
}

// publicArtworkUsers returns the epoch's active-user snapshot, reloading
// when the policy epoch moved. Callers hold publicMu; the snapshot is
// shared and must not be mutated.
func (s *surfaceExperience) publicArtworkUsers(ctx context.Context, epoch uint64) ([]core.User, error) {
	if s.publicCache.loaded && s.publicCache.epoch == epoch {
		return s.publicCache.users, nil
	}
	users, err := s.publicUsers(ctx)
	if err != nil {
		return nil, err
	}
	s.publicCache = publicArtworkCache{epoch: epoch, loaded: true, users: users, items: map[publicArtworkKey]publicArtworkEntry{}}
	return users, nil
}

func (s *surfaceExperience) PublicArtwork(ctx context.Context, device core.Device, item core.RatingKey) ([]frontend.Image, *core.Viewer, bool, error) {
	if s == nil || s.Service == nil || s.artwork == nil || s.publicUsers == nil {
		return nil, nil, false, nil
	}
	switch item.Kind() {
	case core.KindMovie, core.KindShow, core.KindSeason, core.KindEpisode, core.KindLocal:
	default:
		// Collections, playlists, extras and anything else addressed by
		// a non-title key stay grant-only.
		return nil, nil, false, nil
	}
	var epoch uint64
	if s.publicEpoch != nil {
		epoch = s.publicEpoch()
	}
	key := publicArtworkKey{item: item, language: device.Language}
	s.publicMu.Lock()
	users, err := s.publicArtworkUsers(ctx, epoch)
	if err == nil {
		if entry, hit := s.publicCache.items[key]; hit {
			s.publicMu.Unlock()
			return entry.images, entry.reader, entry.ok, nil
		}
	}
	s.publicMu.Unlock()
	if err != nil || len(users) == 0 {
		return nil, nil, false, nil
	}
	entry := s.publicArtworkVerdict(ctx, users, device, item)
	s.publicMu.Lock()
	if s.publicCache.epoch == epoch && s.publicCache.loaded {
		if s.publicCache.items == nil || len(s.publicCache.items) >= 1024 {
			s.publicCache.items = map[publicArtworkKey]publicArtworkEntry{}
		}
		s.publicCache.items[key] = entry
	}
	s.publicMu.Unlock()
	return entry.images, entry.reader, entry.ok, nil
}

// publicArtworkVerdict runs the normal artwork authorization once per
// active user (AuthorizeArtworkAs: sections, content rating, owner
// content policy, inherited show rules, availability) and lists the
// artwork only when every user passes. The list is read as the first
// user (the owner sorts first); every user passed, so reading as any of
// them selects from the same authorized artwork.
func (s *surfaceExperience) publicArtworkVerdict(ctx context.Context, users []core.User, device core.Device, item core.RatingKey) publicArtworkEntry {
	ctx = frontend.WithArtworkRows(ctx)
	for i := range users {
		if err := s.Service.AuthorizeArtworkAs(ctx, &users[i], device, item); err != nil {
			return publicArtworkEntry{}
		}
	}
	reader := users[0]
	images, complete, err := s.artwork.List(ctx, frontend.ArtworkRequest{Viewer: &reader, Device: device, Item: item})
	if err != nil || !complete {
		return publicArtworkEntry{}
	}
	public := make([]frontend.Image, 0, len(images))
	for _, img := range images {
		switch img.Kind {
		case frontend.ImagePoster, frontend.ImageBackdrop, frontend.ImageLogo, frontend.ImageThumb, frontend.ImageBanner, frontend.ImageStill:
			public = append(public, img)
		}
	}
	if len(public) == 0 {
		return publicArtworkEntry{}
	}
	return publicArtworkEntry{images: public, reader: &reader, ok: true}
}

var _ frontend.PublicArtwork = (*surfaceExperience)(nil)
