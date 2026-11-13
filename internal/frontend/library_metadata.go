package frontend

import (
	"context"
	plexmeta "github.com/braedonsaunders/ether/internal/frontends/plex/metadata"
	"github.com/braedonsaunders/ether/internal/plexguid"
)

// LibraryGUIDs reads cached library identities and schedules host refreshes.
// ExactKey distinguishes ambiguous hierarchy identities from missing entries.
type LibraryGUIDs interface {
	Cached(context.Context, RatingKey) string
	CachedMany(context.Context, []RatingKey) map[RatingKey]string
	Schedule(...RatingKey)
	ExactKey(context.Context, string) (RatingKey, error)
	Lookup(context.Context, string) (RatingKey, error)
}

var ErrAmbiguousLibraryGUID = plexguid.ErrAmbiguous

// Metadata overlay values retain catalogue identity while enriching display.
type MetadataOverlay = plexmeta.Overlay
type CachedMetadataOverlay = plexmeta.Cached
type MetadataPerson = plexmeta.Person

const RemoteMetadataArt = plexmeta.RemoteArt

type LibraryMetadata interface {
	Cached(context.Context, RatingKey) (CachedMetadataOverlay, bool)
	CachedMany(context.Context, []RatingKey) map[RatingKey]CachedMetadataOverlay
	Schedule(...RatingKey)
}

func MetadataImageHost(host string) bool { return plexmeta.ImageHost(host) }
