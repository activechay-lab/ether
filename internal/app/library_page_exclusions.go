package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

// newLibraryPageExclusionsFor captures SQL availability generations alongside
// the viewer's policy, so the same decision is used for count and page reads.
func newLibraryPageExclusionsFor(a *App, stepAside *experience.StepAside) func(context.Context, *core.User, core.MediaType) experience.LibraryPageExclusions {
	return func(ctx context.Context, viewer *core.User, typ core.MediaType) experience.LibraryPageExclusions {
		var out experience.LibraryPageExclusions
		if a != nil && a.availability != nil {
			out.Availability, out.Err = a.availability.PagingFilter(ctx, core.Filter{Type: typ})
			out.AvailabilityInSQL = true
			if viewer != nil && a.playback != nil {
				for _, session := range a.playback.Sessions() {
					if session != nil && session.UserID == viewer.ID {
						out.Availability.AvailabilityKeepKeys = append(out.Availability.AvailabilityKeepKeys, session.Key)
					}
				}
			}
		}
		if stepAside != nil {
			if keys, active := stepAside.ExcludedKeys(ctx, viewer, typ); active {
				out.Keys = append(out.Keys, keys...)
				if len(out.Keys) > 2000 {
					out.AvailabilityInSQL = false
				}
			}
		}
		if a != nil && a.safety != nil {
			out.ExcludeGenres, out.ExcludeKeywords = a.safety.BrowseBlocks(viewer)
			out.Movie = a.safety.BrowseShareFilter(viewer, core.TypeMovie)
			out.Television = a.safety.BrowseShareFilter(viewer, core.TypeShow)
		}
		return out
	}
}
