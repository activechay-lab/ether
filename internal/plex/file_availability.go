package plex

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

type fileAvailabilityChecker interface {
	CheckFileAvailability(context.Context, *core.User, core.RatingKey, core.ClientCaps, string) (bool, error)
}

// fileCheckBudget bounds one response's file checks; fileCheckParallel bounds
// how many of them run at once. The playback manager additionally caps file
// checks process-wide (its fileCheckSlots) and answers a busy slot as
// unchecked, so a burst of requests cannot fan out further.
const (
	fileCheckBudget   = 2 * time.Second
	fileCheckParallel = 4
)

// projectFileAvailability answers checkFiles / asyncCheckFiles /
// checkFileAvailability: every Part carries exists and accessible. Versions
// are checked concurrently within one shared budget; a Part whose check did
// not reach a verdict (budget spent, check slots busy, a transient source
// error) keeps the state it is offered with, available, rather than losing
// the attributes.
func (s *Server) projectFileAvailability(r *http.Request, mc *model.MediaContainer) {
	q := r.URL.Query()
	if !metadataOptionOn(q.Get("checkFiles")) && !metadataOptionOn(q.Get("asyncCheckFiles")) && !metadataOptionOn(q.Get("checkFileAvailability")) {
		return
	}
	checker, ok := s.d.Playback.(fileAvailabilityChecker)
	if !ok {
		return
	}
	type target struct {
		key     core.RatingKey
		release string
	}
	parts := map[target][]*model.Part{}
	var order []target
	for i := range mc.Metadata {
		md := &mc.Metadata[i]
		for j := range md.Media {
			for k := range md.Media[j].Part {
				part := &md.Media[j].Part[k]
				key, release, found := s.partTitle(part.ID)
				if !found {
					continue
				}
				t := target{key, release}
				if _, seen := parts[t]; !seen {
					order = append(order, t)
				}
				parts[t] = append(parts[t], part)
			}
		}
	}
	if len(order) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), fileCheckBudget)
	defer cancel()
	pr := FromContext(ctx)
	caps := s.clientCaps(pr)
	verdicts := make([]*bool, len(order)) // nil: no verdict
	slots := make(chan struct{}, fileCheckParallel)
	var wg sync.WaitGroup
dispatch:
	for i, t := range order {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			live, err := checker.CheckFileAvailability(ctx, pr.User, t.key, caps, t.release)
			if err != nil && !errors.Is(err, core.ErrNotAvailable) && !errors.Is(err, core.ErrNotFound) {
				return
			}
			verdicts[i] = &live
		}()
	}
	wg.Wait()
	for i, t := range order {
		live := true
		if verdicts[i] != nil {
			live = *verdicts[i]
		}
		for _, part := range parts[t] {
			part.Exists, part.Accessible = model.Ptr(live), model.Ptr(live)
		}
	}
}
