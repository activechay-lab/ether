package resolver

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

func (s *Service) search(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
	results, _, err := s.searchCached(ctx, req, false)
	return results, err
}
