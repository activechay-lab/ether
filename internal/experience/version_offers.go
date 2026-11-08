package experience

import (
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// release_observations held first-seen times for candidate offers. Date
	// added is the release date, so the ledger is gone; v1 stays registered so
	// upgraded databases keep their migration identity.
	store.Register("release_observations", 1, `CREATE TABLE release_observations (parent_key INTEGER NOT NULL, source_hash TEXT NOT NULL, observed_ns INTEGER NOT NULL, PRIMARY KEY(parent_key,source_hash));`)
	store.RegisterAfter("release_observations_retired", "release_observations")
	store.Register("release_observations_retired", 1, `DROP TABLE release_observations;`)
}

// VersionOffer is one candidate without protocol fields. ID is the existing
// ReleaseVariant.ID, unchanged. It is not a scoped client handle: Jellyfin
// and Emby pass it to VersionHandles.Issue, which replaces it before
// MediaSources, and Plex maps the release to an integer Media/Part id.
// Publication is the release publication time when known. It is not a file
// modification time.
//
// Final merge: profile SelectionKey and remembered-release invalidation are
// absent on b5315d63131029ddb5df7b91161e31240367f602. When they land they
// must not copy ReleaseVariant.ID onto an unscoped client id, and must keep
// this ID pass-through.
type VersionOffer struct {
	ID          string
	Name        string
	Edition     string
	Media       core.MediaInfo
	AddedAt     time.Time
	Publication *time.Time
}

// VersionOfferFrom converts one candidate. It does not resolve a file, part
// list, etag, or modification time, and it does not replace the candidate id.
func VersionOfferFrom(title *core.Title, variant core.ReleaseVariant) VersionOffer {
	published := variant.Release.PubDate
	offer := VersionOffer{
		ID:      variant.ID,
		Name:    variant.Label,
		Edition: variant.Edition,
		Media:   variant.Info,
		AddedAt: title.DateAdded(time.Now()),
	}
	if !published.IsZero() {
		value := published
		offer.Publication = &value
	}
	return offer
}
