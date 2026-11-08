package resolver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// Bounds of an order preview: the recent history it reads and the titles
// it ranks again.
const (
	orderPreviewHistory = 300
	orderPreviewTitles  = 30
	orderPreviewChanges = 10
)

// OrderPreviewChange is one recent title whose first pick would change.
type OrderPreviewChange struct {
	TitleKey  core.RatingKey `json:"titleKey,string"`
	TitleName string         `json:"titleName"`
	Before    string         `json:"before"`
	After     string         `json:"after"`
}

// OrderPreview compares two versions of a profile on recent plays.
type OrderPreview struct {
	// Titles is how many recent titles the profile resolved were ranked
	// with both versions; Changed how many of them would start a different
	// release. Changes lists the first few.
	Titles  int                  `json:"titles"`
	Changed int                  `json:"changed"`
	Changes []OrderPreviewChange `json:"changes"`
}

// previewOrder ranks the releases recent plays found again with a profile
// and with a proposed change to it (the console's "Use the standard order"
// on a converted profile), without saving anything. Like the rule preview
// it is device-neutral, and it ranks what each title's recorded search
// found: the releases the profile accepted then.
func (h *consoleAPI) previewOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Profile core.QualityProfile `json:"profile"`
		Next    core.QualityProfile `json:"next"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := h.s.compareProfiles(ctx, h.deps.Catalog, in.Profile, in.Next)
	if err != nil {
		h.fail(w, err)
		return
	}
	consoleJSON(w, http.StatusOK, out)
}

func (s *Service) compareProfiles(ctx context.Context, catalog ConsoleCatalog, before, after core.QualityProfile) (OrderPreview, error) {
	out := OrderPreview{Changes: []OrderPreviewChange{}}
	if catalog == nil {
		return out, nil
	}
	records, _, err := s.History(ctx, HistoryFilter{Limit: orderPreviewHistory})
	if err != nil {
		return out, err
	}
	seen := map[core.RatingKey]bool{}
	for _, rec := range records {
		if out.Titles >= orderPreviewTitles || ctx.Err() != nil {
			break
		}
		if rec.Profile != before.Name || rec.Candidates == 0 || seen[rec.TitleKey] {
			continue
		}
		seen[rec.TitleKey] = true
		title, err := catalog.Get(ctx, rec.TitleKey)
		if err != nil || title == nil {
			continue
		}
		media := core.MediaMovie
		if title.Type == core.TypeEpisode {
			media = core.MediaTV
		}
		if before.MediaType != "" && before.MediaType != media {
			continue
		}
		full, err := s.HistoryEntry(ctx, rec.ID)
		if err != nil || full.Trace == nil {
			continue
		}
		releases := historyReleases(*full.Trace)
		if len(releases) < 2 {
			continue
		}
		first := func(p core.QualityProfile) (core.Release, bool) {
			cands, _, err := s.rankCandidates(ctx, core.ResolveRequest{Title: title, Client: core.ClientCaps{}}, searchRequestFor(title), p, append([]core.Release(nil), releases...), nil, "")
			if err != nil || len(cands) == 0 {
				return core.Release{}, false
			}
			return cands[0].rel, true
		}
		was, ok1 := first(before)
		now, ok2 := first(after)
		if !ok1 || !ok2 {
			continue
		}
		out.Titles++
		if was.ID == now.ID {
			continue
		}
		out.Changed++
		if len(out.Changes) < orderPreviewChanges {
			out.Changes = append(out.Changes, OrderPreviewChange{TitleKey: rec.TitleKey, TitleName: rec.TitleName, Before: was.Name, After: now.Name})
		}
	}
	return out, nil
}

// historyReleases rebuilds the releases a recorded resolution accepted,
// from what its trace kept: name, indexer and size. A season pack was a
// torrent (Usenet packs are not played).
func historyReleases(tr Trace) []core.Release {
	var out []core.Release
	for _, ct := range tr.Candidates {
		if ct.Rejected || ct.Name == "" || ct.ReleaseID == "" {
			continue
		}
		rel := core.Release{ID: ct.ReleaseID, Name: ct.Name, Indexer: ct.Indexer, Size: ct.Size, Info: releaseparse.Parse(ct.Name)}
		if ct.Height > 0 && rel.Info.Height == 0 {
			rel.Info.Height = ct.Height
		}
		route := strings.ToLower(ct.Route)
		if rel.Info.FullSeason || strings.Contains(route, "debrid") || strings.Contains(route, "torrent") {
			rel.Protocol = core.ProtocolTorrent
		}
		out = append(out, rel)
	}
	return out
}
