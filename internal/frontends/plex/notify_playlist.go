package plex

import (
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// notifyPlaylistLifecycle uses the saved row: after deletion the store can no
// longer supply the title. Membership changes deliberately send no timeline.
func (s *Server) notifyPlaylistLifecycle(pl *frontend.Playlist, created bool) {
	state, metadataState, title := 9, "deleted", pl.Title
	if created {
		state, metadataState, title = 0, "created", ""
	}
	s.publishNotification(model.NotificationContainer{
		Type: "timeline", Size: 1,
		TimelineEntry: []model.TimelineEntry{{
			Identifier: model.LibraryIdentifier, SectionID: "-1",
			ItemID: pl.Key.String(), Type: 15, Title: title,
			State: state, MetadataState: metadataState, UpdatedAt: s.now().Unix(),
		}},
	}, pl.UserID)
}
