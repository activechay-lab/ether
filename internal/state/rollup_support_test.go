package state

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

// Rollup summarises the watched state of a set of children (the episodes of
// a season/show) for Plex's viewedLeafCount-style fields.
type Rollup struct {
	Total      int
	Watched    int  // viewCount > 0
	InProgress int  // has a resume point
	AllWatched bool // Total > 0 && Watched == Total
	AnyWatched bool
}

// Summarize rolls up children using an already-fetched status map. Missing
// entries count as untouched.
func Summarize(children []core.RatingKey, statuses map[core.RatingKey]core.WatchStatus) Rollup {
	r := Rollup{Total: len(children)}
	for _, k := range children {
		ws := statuses[k]
		if IsWatched(ws) {
			r.Watched++
		}
		if IsInProgress(ws) {
			r.InProgress++
		}
	}
	r.AnyWatched = r.Watched > 0
	r.AllWatched = r.Total > 0 && r.Watched == r.Total
	return r
}

// ChildrenStatus fetches statuses for children and rolls them up.
func (s *State) ChildrenStatus(ctx context.Context, userID int64, children []core.RatingKey) (Rollup, map[core.RatingKey]core.WatchStatus, error) {
	m, err := s.GetMany(ctx, userID, children)
	if err != nil {
		return Rollup{}, nil, err
	}
	return Summarize(children, m), m, nil
}
