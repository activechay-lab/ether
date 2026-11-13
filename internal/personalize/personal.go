package personalize

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
)

var _ core.PersonalPicker = (*Engine)(nil)

// Picks reuses home assembly and its item reasons, then applies the effective
// viewer policy again at the boundary. It is intended for background builders.
func (e *Engine) Picks(ctx context.Context, u *core.User, kinds map[string]float64, n int) ([]core.PersonalPick, error) {
	if u == nil {
		return nil, fmt.Errorf("personalize: no viewer")
	}
	if n <= 0 {
		return []core.PersonalPick{}, nil
	}
	if n > 1000 {
		n = 1000
	}
	if e.opts.UserPolicy != nil {
		u = e.opts.UserPolicy(u)
	}
	if u == nil {
		return nil, core.ErrUnauthorized
	}
	excluded, _, err := e.exclusions(ctx, u)
	if err != nil {
		return nil, err
	}
	hc := core.HubContext{Now: e.now(), Device: core.DeviceTV}
	preview, err := e.PreviewHome(ctx, u, hc, "home", n)
	if err != nil {
		return nil, err
	}
	rows := preview.Rows
	b := &builder{e: e, ctx: ctx, user: *u, now: e.now()}
	for _, r := range b.genUpNext() {
		rows = append(rows, legacyRow(r.id, r.title, r.kind, r.reason, r.items))
	}
	if e.opts.PersonalWatchlist != nil {
		titles, err := e.opts.PersonalWatchlist(ctx, u)
		if err != nil {
			return nil, err
		}
		rows = append(rows, core.HomeRow{Kind: "watchlist", Items: titles, Reason: "From your watchlist"})
	}
	if e.opts.PersonalCollections != nil {
		for kind, weight := range kinds {
			if weight <= 0 || !strings.HasPrefix(kind, "collection:") {
				continue
			}
			key, err := strconv.ParseInt(strings.TrimPrefix(kind, "collection:"), 10, 64)
			if err != nil {
				continue
			}
			titles, err := e.opts.PersonalCollections(ctx, u, core.RatingKey(key))
			if err != nil {
				return nil, err
			}
			rows = append(rows, core.HomeRow{Kind: kind, Items: titles, Reason: "From your collection"})
		}
	}
	var out []core.PersonalPick
	seen := map[core.RatingKey]bool{}
	for _, row := range rows {
		kind := "picked"
		switch row.Kind {
		case KindContinue:
			kind = "continue"
		case KindUpNext:
			kind = "nextEpisodes"
		case KindComfort:
			kind = "comfort"
		case KindNewReleases, KindNewThisWeek:
			kind = "newReleases"
		case "watchlist", KindTraktWatch:
			kind = "watchlist"
		default:
			if strings.HasPrefix(row.Kind, KindCollectionPrefix) {
				kind = row.Kind
			}
		}
		weight := kinds[kind]
		if weight <= 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			continue
		}
		for i, t := range row.Items {
			originalKey := t.Key
			if t.Type == core.TypeShow {
				if excluded.contains(&t) || !contentrating.Allowed(t.ContentRating, u.MaxContentRating) || (e.opts.Allowed != nil && !e.opts.Allowed(u, &t)) {
					continue
				}
				episode, err := e.personalEpisode(ctx, u, t.Key, kind == "comfort")
				if err != nil {
					return nil, err
				}
				if episode == nil {
					continue
				}
				t = *episode
			}
			if seen[t.Key] || excluded.contains(&t) || t.Adult || !contentrating.Allowed(t.ContentRating, u.MaxContentRating) || (e.opts.Allowed != nil && !e.opts.Allowed(u, &t)) {
				continue
			}
			if t.Runtime > 24*60*60*1000000000 {
				continue
			}
			if t.Type != core.TypeMovie && t.Type != core.TypeEpisode {
				continue
			}
			if !t.Released.IsZero() && t.Released.After(e.now()) {
				continue
			}
			if kind == "nextEpisodes" && e.state != nil {
				ws, err := e.state.Get(ctx, u.ID, t.Key)
				if err != nil {
					return nil, err
				}
				if ws.ViewCount > 0 || ws.ViewOffset > 0 {
					continue
				}
			}
			reason := row.ItemReasons[originalKey]
			if reason == "" {
				reason = row.Reason
			}
			seen[t.Key] = true
			out = append(out, core.PersonalPick{Title: &t, Reason: reason, Kind: kind, Score: weight / float64(i+1)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// personalEpisode uses mirrored children only and admits one playable episode
// per show. Next-episode selection itself stays in the existing generator.
func (e *Engine) personalEpisode(ctx context.Context, u *core.User, key core.RatingKey, rewatch bool) (*core.Title, error) {
	children, err := e.catalog.Children(ctx, key)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(children, func(i, j int) bool {
		if children[i].Season != children[j].Season {
			return children[i].Season < children[j].Season
		}
		return children[i].Episode < children[j].Episode
	})
	for _, t := range children {
		if t.Type == core.TypeSeason {
			episode, err := e.personalEpisode(ctx, u, t.Key, rewatch)
			if err != nil {
				return nil, err
			}
			if episode != nil {
				return episode, nil
			}
			continue
		}
		if t.Type != core.TypeEpisode || t.Released.IsZero() || t.Released.After(e.now()) {
			continue
		}
		if e.state != nil && !rewatch {
			ws, err := e.state.Get(ctx, u.ID, t.Key)
			if err != nil {
				return nil, err
			}
			if ws.ViewCount > 0 {
				continue
			}
		}
		return &t, nil
	}
	return nil, nil
}
