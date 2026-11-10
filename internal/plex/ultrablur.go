package plex

import (
	"context"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

const (
	ultraBlurWorkers  = 2
	ultraBlurQueueLen = 512
)

func wirePalette(p frontend.ArtworkPalette) model.UltraBlurColors {
	return model.UltraBlurColors{TopLeft: p.TopLeft, TopRight: p.TopRight, BottomLeft: p.BottomLeft, BottomRight: p.BottomRight}
}

func (s *Server) ultraBlurFor(key frontend.RatingKey, backdrop string) *model.UltraBlurColors {
	if s.ultra == nil || backdrop == "" {
		return nil
	}
	p := s.ultra.Lookup(key, backdrop)
	if p == nil {
		return nil
	}
	result := wirePalette(*p)
	return &result
}
func (s *Server) preloadUltraBlur(ctx context.Context, titles []frontend.Title) {
	if s.ultra == nil {
		return
	}
	wants := make([]frontend.ArtworkPaletteWant, 0, len(titles))
	for _, t := range titles {
		if ultraBlurType(t.Type) {
			wants = append(wants, frontend.ArtworkPaletteWant{Key: t.Key, Path: t.BackdropPath})
		}
	}
	s.ultra.Preload(ctx, wants)
}
func ultraBlurType(t frontend.MediaType) bool {
	return t == frontend.TypeMovie || t == frontend.TypeShow
}
