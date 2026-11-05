package newznab

import (
	"context"

	"github.com/braedonsaunders/ether/internal/config"
)

// DiscoverProwlarr imports enabled Usenet (Newznab) and torrent (Torznab)
// indexers; torrent indexers get Protocol "torrent" and stream through a
// debrid provider. Each receives a stable
// source-scoped ID and its own Ether hit/grab budget, cache and reputation.
// Prowlarr's /{id}/api alias is intentionally used so ordinary Newznab URL
// construction and all existing budget enforcement apply unchanged.
func DiscoverProwlarr(ctx context.Context, cfg config.Prowlarr, opts Options) ([]config.Indexer, error) {
	report, err := DiscoverProwlarrReport(ctx, cfg, opts)
	if err != nil {
		return nil, err
	}
	return report.Indexers, nil
}
