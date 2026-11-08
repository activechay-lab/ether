package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/braedonsaunders/ether/internal/admin"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func (a *App) buildCatalogue(ctx context.Context, cfg config.Config) error {
	log := a.log
	var err error
	// Catalogue.
	a.tmdb = tmdb.NewFromConfig(cfg.Catalog, log.With("component", "tmdb"), parityTMDBOptions()...)
	parityConfigureTMDB(a.tmdb)
	a.closers = append(a.closers, a.tmdb.Close)
	opts := append(a.catalogOptions(), catalog.WithStartupContext(ctx))
	opts = append(opts, parityCatalogueOptions()...)
	if len(parityTMDBOptions()) == 0 {
		// The daily ID exports are the title list; fixture builds point
		// TMDB at a fake and walk its /discover instead.
		dir := filepath.Join(a.opts.DataDir, "tmdb-exports")
		opts = append(opts, catalog.WithExports(tmdb.NewExports(dir, log.With("component", "tmdb-exports"))),
			// Built and kept only where RunTail runs (nodes that serve search).
			catalog.WithTailIndex(dir, admin.DiskFree))
	}
	if a.catalog, err = catalog.New(a.db, a.tmdb, cfg.Catalog, log.With("component", "catalog"), opts...); err != nil {
		return fmt.Errorf("catalog: %w", err)
	}

	return nil
}
