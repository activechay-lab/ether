package resolver

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type channelAvailabilityKey struct{}

func channelAvailabilityCheck(ctx context.Context) bool {
	v, _ := ctx.Value(channelAvailabilityKey{}).(bool)
	return v
}

// CheckChannelAvailability uses the same candidate validation as Play without
// creating a playback session. Unattended purpose retains indexer background
// allowances, disables rescue fan-out and prohibits on-demand stream opens.
func (s *Service) CheckChannelAvailability(ctx context.Context, title *core.Title) (bool, bool) {
	if title == nil {
		return false, false
	}
	if known, ready := s.Available(ctx, title.Key); known {
		return known, ready
	}
	ctx = core.WithoutOnDemandStreams(core.WithUnattendedSearch(core.WithIndexerPurpose(ctx, core.IndexerPurposeAvailability)))
	// Background validation cannot queue uncached downloads or promote links to
	// foreground work. Actual indexer admission remains in the indexer service.
	if !core.IsBackgroundCheck(ctx) {
		return false, false
	}
	ctx = context.WithValue(ctx, channelAvailabilityKey{}, true)
	_, err := s.Resolve(ctx, core.ResolveRequest{Title: title, Client: feedClient})
	if err == nil {
		return true, true
	}
	if errors.Is(err, core.ErrNotAvailable) && !errors.Is(err, core.ErrForegroundOnly) && ctx.Err() == nil {
		s.setAvailability(ctx, int64(title.Key), false, 5*time.Minute)
	}
	return s.Available(ctx, title.Key)
}
