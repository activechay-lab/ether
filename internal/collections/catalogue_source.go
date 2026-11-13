package collections

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
)

// GatherCatalogueSource reuses the installed list-source adapter, including its
// paging limits and external-ID matching. Only an opened library invokes it.
func (s *Service) GatherCatalogueSource(ctx context.Context, kind, mediaType string) ([]core.Title, error) {
	k, ok := s.kindRegistry().Lookup(kind)
	if !ok {
		return nil, fmt.Errorf("catalogue source unavailable")
	}
	src := core.CollectionSource{Kind: kind, MaxItems: 500}
	g := &Gather{Ctx: ctx, Svc: s, Spec: &core.CollectionSpec{}, Src: src, Want: 500, Kinds: []string{mediaType}}
	k.Gather(g)
	return g.Titles(), g.Res.err
}
