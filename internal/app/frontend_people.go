package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func (s *surfaceExperience) personArtwork(ctx context.Context, viewer *frontend.Viewer, device frontend.Device, person frontend.PersonDetail) (frontend.PersonDetail, error) {
	var result frontend.PersonDetail
	err := s.Service.WithPersonCredit(ctx, viewer, device, person.ArtworkItem, person.TMDB, func(current *core.User, credit frontend.PersonDetail, title core.Title) error {
		if credit.ID != person.ID {
			return core.ErrNotFound
		}
		result = credit
		artwork, ok := s.artwork.(frontend.PersonArtwork)
		if !ok {
			return nil
		}
		images, err := artwork.PersonImages(ctx, frontend.ArtworkRequest{Viewer: current, Device: device, Item: credit.ArtworkItem}, title)
		if errors.Is(err, core.ErrUnauthorized) || errors.Is(err, core.ErrNotFound) {
			return core.ErrNotFound
		}
		if err == nil {
			subject := "tmdb:" + strconv.FormatInt(credit.TMDB, 10)
			for _, image := range images {
				if image.Kind == frontend.ImagePerson && image.Subject == subject && image.Path != "" {
					result.Images = append(result.Images, image)
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return frontend.PersonDetail{}, err
	}
	return result, nil
}

// The artwork source receives the owned current title rather than loading a
// node-cached movie. Descriptor identity and credit policy share one epoch proof.
func (s *surfaceExperience) AuthorizePersonImage(ctx context.Context, viewer *frontend.Viewer, device frontend.Device, item frontend.RatingKey, image frontend.Image, admission func(context.Context) error) error {
	raw, hasPrefix := strings.CutPrefix(image.Subject, "tmdb:")
	id, err := strconv.ParseInt(raw, 10, 64)
	if image.Kind != frontend.ImagePerson || !hasPrefix || err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return core.ErrUnauthorized
	}
	return s.Service.WithPersonCredit(ctx, viewer, device, item, id, func(current *core.User, _ frontend.PersonDetail, title core.Title) error {
		artwork, ok := s.artwork.(frontend.PersonArtwork)
		if !ok {
			return core.ErrNotAvailable
		}
		images, err := artwork.PersonImages(ctx, frontend.ArtworkRequest{Viewer: current, Device: device, Item: item}, title)
		if err != nil {
			return err
		}
		for _, candidate := range images {
			if frontend.SameImage(candidate, image) {
				if admission != nil {
					return admission(ctx)
				}
				return nil
			}
		}
		return core.ErrUnauthorized
	})
}

func (s *surfaceExperience) People(ctx context.Context, viewer *frontend.Viewer, device frontend.Device, q frontend.PeopleQuery) ([]frontend.PersonDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	people, err := s.Service.People(ctx, viewer, device, q)
	if err != nil {
		return nil, err
	}
	// Every person's portrait in one policy proof (one people read and two
	// batched title reads), not a read per person.
	refs := make([]experience.PersonCreditRef, len(people))
	for n, person := range people {
		refs[n] = experience.PersonCreditRef{Item: person.ArtworkItem, TMDB: person.TMDB}
	}
	out := make([]frontend.PersonDetail, len(people))
	skipped, err := s.Service.WithPersonCredits(ctx, viewer, device, refs, func(n int, current *core.User, credit frontend.PersonDetail, title core.Title) error {
		if credit.ID != people[n].ID {
			return core.ErrNotFound
		}
		var err error
		out[n], err = s.withPersonImage(ctx, current, device, credit, title)
		return err
	})
	if err != nil {
		return nil, err
	}
	for n := range skipped {
		if skipped[n] {
			// As personArtwork: a credit that is no longer current fails the read.
			return nil, core.ErrNotFound
		}
	}
	return out, nil
}

// withPersonImage adds the person's portrait descriptor to credit.
func (s *surfaceExperience) withPersonImage(ctx context.Context, current *core.User, device frontend.Device, credit frontend.PersonDetail, title core.Title) (frontend.PersonDetail, error) {
	artwork, ok := s.artwork.(frontend.PersonArtwork)
	if !ok {
		return credit, nil
	}
	images, err := artwork.PersonImages(ctx, frontend.ArtworkRequest{Viewer: current, Device: device, Item: credit.ArtworkItem}, title)
	if errors.Is(err, core.ErrUnauthorized) || errors.Is(err, core.ErrNotFound) {
		return frontend.PersonDetail{}, core.ErrNotFound
	}
	if err != nil {
		return credit, nil
	}
	subject := "tmdb:" + strconv.FormatInt(credit.TMDB, 10)
	for _, image := range images {
		if image.Kind == frontend.ImagePerson && image.Subject == subject && image.Path != "" {
			credit.Images = append(credit.Images, image)
			break
		}
	}
	return credit, nil
}
func (s *surfaceExperience) Person(ctx context.Context, viewer *frontend.Viewer, device frontend.Device, id string, options frontend.PersonReadOptions) (frontend.PersonDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	person, err := s.Service.Person(ctx, viewer, device, id, options)
	if err != nil {
		return frontend.PersonDetail{}, err
	}
	if options.Prefetch {
		// Artwork List can enqueue metadata/variant work. A prefetch is strictly
		// a current cached-credit read and must not schedule any refresh.
		return person, nil
	}
	return s.personArtwork(ctx, viewer, device, person)
}

var _ frontend.PeopleReader = (*surfaceExperience)(nil)

// AuthorizePersonImages shares the policy/credit proof across the entire page.
func (s *surfaceExperience) AuthorizePersonImages(ctx context.Context, viewer *frontend.Viewer, device frontend.Device, targets []frontend.PersonImageTarget) ([]bool, error) {
	if len(targets) > 100 {
		return nil, frontend.ErrImageBatchLimit
	}
	refs := make([]experience.PersonCreditRef, len(targets))
	for n, target := range targets {
		raw, prefix := strings.CutPrefix(target.Image.Subject, "tmdb:")
		id, err := strconv.ParseInt(raw, 10, 64)
		if target.Item == 0 || target.Image.Kind != frontend.ImagePerson || !prefix || err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
			return nil, core.ErrUnauthorized
		}
		refs[n] = experience.PersonCreditRef{Item: target.Item, TMDB: id}
	}
	allowed := make([]bool, len(targets))
	artwork, ok := s.artwork.(frontend.PersonArtwork)
	if !ok {
		return nil, core.ErrNotAvailable
	}
	_, err := s.Service.WithPersonCredits(ctx, viewer, device, refs, func(n int, current *core.User, _ frontend.PersonDetail, title core.Title) error {
		images, err := artwork.PersonImages(ctx, frontend.ArtworkRequest{Viewer: current, Device: device, Item: targets[n].Item}, title)
		if errors.Is(err, core.ErrUnauthorized) || errors.Is(err, core.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, image := range images {
			if frontend.SameImage(image, targets[n].Image) {
				allowed[n] = true
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return allowed, nil
}
