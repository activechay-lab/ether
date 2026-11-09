package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extras"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type plexExtras struct {
	extras *extras.Service
}

func newPlexExtras(s *extras.Service) frontend.ExtrasProvider {
	if s == nil {
		return nil
	}
	return plexExtras{extras: s}
}

func toExtraClip(e extras.Extra) frontend.ExtraClip {
	return frontend.ExtraClip{
		Key:         e.Key,
		ParentKey:   e.ParentKey,
		ParentTitle: e.ParentTitle,
		Title:       e.Title,
		Subtype:     e.Subtype,
		ExtraType:   e.ExtraType,
		Site:        e.Site,
		SiteKey:     e.SiteKey,
		Language:    e.Language,
		Official:    e.Official,
		PublishedAt: e.PublishedAt,
		Size:        e.Size,
		Duration:    e.Duration,
		Source:      e.Source,
	}
}

func (p plexExtras) Enabled() bool {
	return p.extras != nil && p.extras.Enabled()
}

func (p plexExtras) List(ctx context.Context, parent *core.Title, lang string) ([]frontend.ExtraClip, error) {
	list, err := p.extras.List(ctx, parent, lang)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	out := make([]frontend.ExtraClip, len(list))
	for i, clip := range list {
		out[i] = toExtraClip(clip)
	}
	return out, nil
}

func (p plexExtras) Lookup(key core.RatingKey) (frontend.ExtraClip, bool) {
	clip, ok := p.extras.Lookup(key)
	if !ok {
		return frontend.ExtraClip{}, false
	}
	return toExtraClip(clip), true
}

func (p plexExtras) Resolve(ctx context.Context, e frontend.ExtraClip) (frontend.ResolvedExtra, error) {
	clip, ok := p.extras.Lookup(e.Key)
	if !ok {
		clip = extras.Extra{
			Key:         e.Key,
			ParentKey:   e.ParentKey,
			ParentTitle: e.ParentTitle,
			Title:       e.Title,
			Subtype:     e.Subtype,
			ExtraType:   e.ExtraType,
			Site:        e.Site,
			SiteKey:     e.SiteKey,
			Language:    e.Language,
			Official:    e.Official,
			PublishedAt: e.PublishedAt,
			Size:        e.Size,
			Duration:    e.Duration,
			Source:      e.Source,
		}
	}
	resolved, err := p.extras.Resolve(ctx, clip)
	if err != nil {
		return frontend.ResolvedExtra{}, err
	}
	return frontend.ResolvedExtra{
		URL:       resolved.URL,
		Headers:   resolved.Headers,
		Expires:   resolved.Expires,
		Container: resolved.Container,
	}, nil
}

func (p plexExtras) PrewarmBytes() int64 {
	return p.extras.PrewarmBytes()
}
