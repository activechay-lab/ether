package experience

// CompleteHome orders the remaining home producers and finalizes the response.
// The adapter owns rendered metadata and only exposes these operations here.
func CompleteHome(includePlaylists bool, playlists, discovery, live, selectRows, layout func()) {
	if includePlaylists {
		playlists()
	}
	discovery()
	live()
	selectRows()
	layout()
}
