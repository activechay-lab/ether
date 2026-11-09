package experience

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestSeasonNextEpisodeKeepsResumeAndPresentationOrder(t *testing.T) {
	now := time.Now()
	eps := []core.Title{
		{Key: core.EpisodeKey(1, 1, 3), Type: core.TypeEpisode},
		{Key: core.EpisodeKey(1, 1, 1), Type: core.TypeEpisode},
		{Key: core.EpisodeKey(1, 1, 2), Type: core.TypeEpisode, Released: now.Add(time.Hour)},
	}
	state := map[core.RatingKey]core.WatchStatus{eps[0].Key: {ViewCount: 1}, eps[1].Key: {ViewOffset: time.Minute}}
	if got := SeasonNextEpisode(eps, state, now, func(*core.Title) bool { return true }); got == nil || got.Key != eps[1].Key {
		t.Fatalf("active resume lost: %+v", got)
	}
	state[eps[1].Key] = core.WatchStatus{}
	if got := SeasonNextEpisode(eps, state, now, func(*core.Title) bool { return false }); got == nil || got.Key != eps[1].Key {
		t.Fatalf("presentation order lost: %+v", got)
	}
	if got := SeasonNextEpisode(eps, state, now, func(t *core.Title) bool { return t.Key != eps[2].Key }); got != nil {
		t.Fatalf("future episode selected: %+v", got)
	}
}
