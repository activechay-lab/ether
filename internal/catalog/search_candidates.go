package catalog

import (
	"context"
	"errors"
	"slices"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
)

// policyFacts is what deciding a title with f reads beyond its row: the
// facts f's rules read, and home-release dates when the release gate waits
// for a digital release. An unrestricted catalogue reads none.
func (c *Catalog) policyFacts(f *libraryfilter.Filter) core.TitleFacts {
	facts := f.Facts()
	if c.config().ReleaseMode == config.ModeDigital {
		facts |= core.FactHomeRelease
	}
	return facts
}

// viewerPolicyFacts is what the catalogue policy of ctx's viewer reads
// beyond a title's row, its browsing and its search rules alike: the
// direct and discovery checks a frontend applies to search results
// (VisibleForViewer, DiscoveryVisibleForViewer).
func (c *Catalog) viewerPolicyFacts(ctx context.Context) core.TitleFacts {
	id := core.CatalogViewerID(ctx)
	return c.policyFacts(c.viewerFilter(id, false)) | c.policyFacts(c.viewerFilter(id, true))
}

// finishCandidates completes a candidate window (core.SearchCandidates).
// Its library rows carry have, the facts the window was filtered by; they
// gain the rest of what the caller's policy and filters read, a title the
// caller judges on its full facts is read in full, and the others are
// recorded partial for the caller's page.
func (c *Catalog) finishCandidates(ctx context.Context, cand *core.SearchCandidates, items []core.Title, partial map[core.RatingKey]bool, have core.TitleFacts) error {
	var rows []core.Title
	var at []int
	for i := range items {
		if partial[items[i].Key] {
			rows = append(rows, items[i])
			at = append(at, i)
		}
	}
	if len(rows) == 0 {
		return nil
	}
	need := (cand.Facts | c.policyFacts(c.visibilityFor(ctx)) | c.viewerPolicyFacts(ctx)) &^ have
	if err := c.hydrateFacts(ctx, rows, need); err != nil {
		return err
	}
	var full []int
	marked := make([]core.RatingKey, 0, len(rows))
	for j := range rows {
		items[at[j]] = rows[j]
		if cand.Detail != nil && cand.Detail(rows[j]) {
			full = append(full, at[j])
		} else {
			marked = append(marked, rows[j].Key)
		}
	}
	cand.MarkPartial(marked)
	return c.readSearchFull(ctx, items, full)
}

// SearchDetails returns items with the titles a candidate search (the
// core.SearchCandidates in ctx) returned partial read in full, each keeping
// its search score: the page its caller shows, as a search without
// SearchCandidates would have returned it. Other titles are unchanged.
func (c *Catalog) SearchDetails(ctx context.Context, items []core.Title) ([]core.Title, error) {
	cand := core.SearchCandidatesFrom(ctx)
	out := slices.Clone(items)
	var at []int
	for i := range out {
		if cand.Partial(out[i].Key) {
			at = append(at, i)
		}
	}
	if err := c.readSearchFull(ctx, out, at); err != nil {
		return nil, err
	}
	return out, nil
}

// SearchListDetails is SearchDetails for titles shown as list items
// (core.WithSearchListFacts): the partial ones gain the facts a list shows
// (hydrateListSlice) on the rows the search read, not cached, each keeping
// its search score.
func (c *Catalog) SearchListDetails(ctx context.Context, items []core.Title) ([]core.Title, error) {
	cand := core.SearchCandidatesFrom(ctx)
	out := slices.Clone(items)
	var rows []core.Title
	var at []int
	for i := range out {
		if cand.Partial(out[i].Key) {
			t := out[i]
			// The facts the window read are read again with the list's.
			t.Genres, t.Studios, t.Countries, t.Keywords, t.AlternativeTitles = nil, nil, nil, nil, nil
			t.Cast, t.Directors, t.Writers, t.Crew = nil, nil, nil, nil
			rows = append(rows, t)
			at = append(at, i)
		}
	}
	if len(rows) == 0 {
		return out, nil
	}
	if err := c.hydrateListSlice(ctx, rows); err != nil {
		return nil, err
	}
	for j, i := range at {
		out[i] = rows[j]
	}
	return out, nil
}

// readSearchFull replaces items[i] for each i in at with its full stored
// title, read in one batch and cached as Get caches it, keeping its search
// score. A title no longer stored stays as it is.
func (c *Catalog) readSearchFull(ctx context.Context, items []core.Title, at []int) error {
	if len(at) == 0 {
		return nil
	}
	keys := make([]core.RatingKey, len(at))
	for j, i := range at {
		keys[j] = items[i].Key
	}
	c.PreloadStored(ctx, keys)
	for _, i := range at {
		st, err := c.loadByKey(ctx, items[i].Key)
		if errors.Is(err, core.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		full := st.t
		full.SearchScore = items[i].SearchScore
		items[i] = full
	}
	return nil
}

// finishRankPage completes a ranking window's page served from the page
// cache: a title read partial that this request judges on its full facts
// is read in full, and the rest are recorded partial again.
func (c *Catalog) finishRankPage(ctx context.Context, cand *core.SearchCandidates, items []core.Title, partial []core.RatingKey) error {
	was := make(map[core.RatingKey]bool, len(partial))
	for _, k := range partial {
		was[k] = true
	}
	var full []int
	marked := make([]core.RatingKey, 0, len(partial))
	for i := range items {
		if !was[items[i].Key] {
			continue
		}
		if cand.Detail != nil && cand.Detail(items[i]) {
			full = append(full, i)
		} else {
			marked = append(marked, items[i].Key)
		}
	}
	cand.MarkPartial(marked)
	return c.readSearchFull(ctx, items, full)
}

// RankingDetails is SearchDetails for a ranking window
// (core.WithRankingCandidates): items with the titles it returned partial
// read in full, as a browse without the window returns them.
func (c *Catalog) RankingDetails(ctx context.Context, items []core.Title) ([]core.Title, error) {
	cand := core.RankingCandidatesFrom(ctx)
	out := slices.Clone(items)
	var at []int
	for i := range out {
		if cand.Partial(out[i].Key) {
			at = append(at, i)
		}
	}
	if err := c.readSearchFull(ctx, out, at); err != nil {
		return nil, err
	}
	return out, nil
}
