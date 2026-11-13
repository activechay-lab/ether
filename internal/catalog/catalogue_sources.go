package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

// SetCatalogueSourceLoader is startup wiring. A nil loader costs no work.
func (c *Catalog) SetCatalogueSourceLoader(load func(context.Context, []string) error) {
	c.catalogueSourceLoader = load
}
func (c *Catalog) ensureCatalogueSources(ctx context.Context, f core.Filter) error {
	if c.catalogueSourceLoader == nil {
		return nil
	}
	if len(f.CatalogueSources) > 0 {
		if err := c.catalogueSourceLoader(ctx, f.CatalogueSources); err != nil {
			return err
		}
	}
	var walk func(*core.FilterGroup) error
	walk = func(g *core.FilterGroup) error {
		if g == nil {
			return nil
		}
		if g.Filter != nil {
			if err := c.ensureCatalogueSources(ctx, *g.Filter); err != nil {
				return err
			}
		}
		for i := range g.Children {
			if err := walk(&g.Children[i]); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(f.Group)
}

func (c *Catalog) CatalogueSourcesChanged() { c.writeGen.Add(1) }

func (c *Catalog) BrowseSourceLibrary(ctx context.Context, ids []string) error {
	if c.catalogueSourceLoader == nil {
		return nil
	}
	return c.catalogueSourceLoader(ctx, ids)
}
