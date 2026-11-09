package debrid

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// v3: retention finds expired cache rows by age. Torrent rows that
	// remember a torrent on the account are never pruned, so their index
	// leaves them out.
	store.Register("debrid", 3, `
CREATE INDEX debrid_payloads_updated ON debrid_payloads(updated_at);
CREATE INDEX debrid_torrents_checked ON debrid_torrents(checked_at) WHERE torrent_id='';`)
}

// pruneMargin is how far past its TTL a cache row is kept before Prune
// deletes it, so a clock skew between nodes never races a read.
const pruneMargin = 24 * time.Hour

// Prune deletes cache rows past their TTL by pruneMargin:
//
//   - debrid_payloads older than PayloadTTL (storedPayload ignores them);
//   - debrid_torrents availability answers older than the longer of
//     CachedTTL and UncachedTTL (knownFor ignores them), only when the row
//     records no torrent id on the account (reuse, removal and streaming
//     leases key on that id) and holds no live add claim.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	now := s.now()
	total, err := store.PruneBatched(ctx, s.db,
		`DELETE FROM debrid_payloads WHERE release_key IN (SELECT release_key FROM debrid_payloads WHERE updated_at<? ORDER BY updated_at LIMIT ?)`,
		now.Add(-s.opts.PayloadTTL-pruneMargin).Unix())
	if err != nil {
		return total, err
	}
	ttl := max(s.opts.CachedTTL, s.opts.UncachedTTL)
	// claimed_at is 0 when unclaimed; a claim older than the cutoff is long
	// past claimStale, so its holder is gone.
	cutoff := now.Add(-ttl - pruneMargin).Unix()
	n, err := store.PruneBatched(ctx, s.db,
		`DELETE FROM debrid_torrents WHERE (provider_id, info_hash) IN (SELECT provider_id, info_hash FROM debrid_torrents WHERE torrent_id='' AND checked_at<? AND claimed_at<? ORDER BY checked_at LIMIT ?)`,
		cutoff, cutoff)
	return total + n, err
}
