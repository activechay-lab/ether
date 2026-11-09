package experience

import (
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

// SeasonNextEpisode selects resume, then the first available episode after the
// last watched one, then the first available aired episode. An active resume
// remains eligible even if discovery availability has changed since playback.
// eps must already be in viewer-visible presentation order.
func SeasonNextEpisode(eps []core.Title, st map[core.RatingKey]core.WatchStatus, now time.Time, unavailable func(*core.Title) bool) *core.Title {
	lastWatched := -1
	for i := range eps {
		if eps[i].Type != core.TypeEpisode || (!eps[i].Released.IsZero() && eps[i].Released.After(now)) {
			continue
		}
		ws := st[eps[i].Key]
		if ws.ViewOffset > 0 && ws.ViewCount == 0 {
			return &eps[i]
		}
		if ws.ViewCount > 0 {
			lastWatched = i
		}
	}
	for i := lastWatched + 1; i < len(eps); i++ {
		if eps[i].Type != core.TypeEpisode || (!eps[i].Released.IsZero() && eps[i].Released.After(now)) {
			continue
		}
		if st[eps[i].Key].ViewCount == 0 && !unavailable(&eps[i]) {
			return &eps[i]
		}
	}
	for i := range eps {
		if eps[i].Type == core.TypeEpisode && (eps[i].Released.IsZero() || !eps[i].Released.After(now)) && !unavailable(&eps[i]) {
			return &eps[i]
		}
	}
	return nil
}
