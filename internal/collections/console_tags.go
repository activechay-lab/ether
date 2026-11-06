package collections

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
)

func (s *Service) TitleTags(ctx context.Context, key core.RatingKey) ([]string, error) {
	if tagger, ok := s.cat.(interface {
		TitleTags(context.Context, core.RatingKey) ([]string, error)
	}); ok {
		return tagger.TitleTags(ctx, key)
	}
	return nil, errors.New("tags unavailable")
}
func (s *Service) SetTitleTags(ctx context.Context, key core.RatingKey, tags []string) error {
	if tagger, ok := s.cat.(interface {
		SetTitleTags(context.Context, core.RatingKey, []string) error
	}); ok {
		return tagger.SetTitleTags(ctx, key, tags)
	}
	return errors.New("tags unavailable")
}
