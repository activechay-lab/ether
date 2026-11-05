package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func catalogSortValid(s core.SortOrder) bool {
	switch s {
	case core.SortPopularity, core.SortRating, core.SortReleased, core.SortAdded, core.SortTitle, core.SortYear, core.SortDuration, core.SortUpdated:
		return true
	}
	return false
}

// Keep user-authored expressions bounded before compiling SQL. Policy-only
// fields have json:"-" and cannot be supplied by a console query.
func decodeCatalogFilter(raw string, f *core.Filter) error {
	if raw == "" {
		return nil
	}
	if len(raw) > 16384 {
		return errors.New("Filter exceeds 16 KiB")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(f); err != nil {
		return errors.New("Invalid filter expression")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("Invalid filter expression")
	}
	nodes, conditions := 0, 0
	var walk func(*core.Filter, int) error
	var group func(*core.FilterGroup, int) error
	group = func(g *core.FilterGroup, depth int) error {
		if g == nil {
			return nil
		}
		nodes++
		if depth > 5 || nodes > 96 {
			return errors.New("Use at most 32 conditions and five group levels")
		}
		if g.Filter != nil || g.StateField != "" || g.Unwatched != nil || g.InProgress != nil || g.OnWatchlist != nil {
			conditions++
			if conditions > 32 {
				return errors.New("Use at most 32 conditions and five group levels")
			}
		}
		if g.UserID < 0 {
			return errors.New("Invalid viewer")
		}
		if g.StateField != "" {
			if g.StateField != "lastRatedAt" || (g.StateOperator != ">" && g.StateOperator != "=") || g.StateValue != 0 || g.UserID <= 0 {
				return errors.New("Use rated, watched or in-progress viewer filters")
			}
			return nil
		}
		if g.Unwatched != nil || g.InProgress != nil || g.OnWatchlist != nil {
			if g.UserID <= 0 {
				return errors.New("Choose a viewer for watch state")
			}
			return nil
		}
		if g.Filter != nil {
			return walk(g.Filter, depth+1)
		}
		if g.Operator != "and" && g.Operator != "or" {
			return errors.New("Choose AND or OR")
		}
		if len(g.Children) == 0 {
			return errors.New("Empty filter group")
		}
		for i := range g.Children {
			if err := group(&g.Children[i], depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	walk = func(v *core.Filter, depth int) error {
		if depth > 5 {
			return errors.New("Filter groups are too deep")
		}
		if v.Type != "" && v.Type != core.TypeMovie && v.Type != core.TypeShow {
			return errors.New("Choose movies or series")
		}
		rv := reflect.ValueOf(*v)
		for i := 0; i < rv.NumField(); i++ {
			x := rv.Field(i)
			if x.Kind() == reflect.Slice && x.Len() > 32 {
				return errors.New("Use at most 32 values per condition")
			}
			if x.Kind() == reflect.String && x.Len() > 256 {
				return errors.New("Filter value is too long")
			}
		}
		if v.YearFrom < 0 || v.YearTo < 0 || (v.YearTo > 0 && v.YearFrom > v.YearTo) || v.MinRating < 0 || v.MaxRating > 10 || v.MinRating > 10 || v.MaxRating < 0 || (v.MaxRating > 0 && v.MinRating > v.MaxRating) || v.MinRuntime < 0 || v.MaxRuntime < 0 || v.MinRuntime > 48*time.Hour || v.MaxRuntime > 48*time.Hour || (v.MaxRuntime > 0 && v.MinRuntime > v.MaxRuntime) {
			return errors.New("Invalid filter range")
		}
		if v.MinVotes < 0 || v.MaxVotes < 0 || v.MinPopularity < 0 || v.MaxPopularity < 0 || v.MinSeasons < 0 || v.MaxSeasons < 0 || v.MinEpisodes < 0 || v.MaxEpisodes < 0 {
			return errors.New("Invalid numeric range")
		}
		for _, bounds := range [][2]float64{{float64(v.MinVotes), float64(v.MaxVotes)}, {v.MinPopularity, v.MaxPopularity}, {float64(v.MinSeasons), float64(v.MaxSeasons)}, {float64(v.MinEpisodes), float64(v.MaxEpisodes)}, {float64(v.EtherAddedFrom), float64(v.EtherAddedTo)}} {
			if bounds[0] < 0 || bounds[1] < 0 || (bounds[1] > 0 && bounds[0] > bounds[1]) {
				return errors.New("Invalid numeric range")
			}
		}
		if v.ReleaseGate != "" && v.ReleaseGate != "admitted" && v.ReleaseGate != "held" && v.ReleaseGate != "unknown" {
			return errors.New("Unknown release gate state")
		}
		for _, evidence := range []string{v.Playable, v.DebridCached} {
			if evidence != "" && evidence != "yes" && evidence != "unknown" {
				return errors.New("Choose verified or unknown availability")
			}
		}
		for _, quality := range v.Qualities {
			switch quality {
			case "4k", "hdr", "dv", "atmos":
			default:
				return errors.New("Unknown release quality")
			}
		}
		if (v.DateAddedFrom != 0 && v.DateAddedTo != 0 && v.DateAddedFrom > v.DateAddedTo) || v.DateAddedFrom < -62135596800 || v.DateAddedTo < -62135596800 || v.DateAddedFrom > 253402300799 || v.DateAddedTo > 253402300799 {
			return errors.New("Invalid date range")
		}
		for _, c := range v.Credits {
			if len(c.People)+len(c.Names) > 32 {
				return errors.New("Too many people")
			}
			switch c.Kind {
			case "", "cast", "director", "writer", "composer", "crew":
			default:
				return errors.New("Unknown credit role")
			}
		}
		for _, d := range v.ReleasedFilters {
			switch d.Op {
			case "=", "!=", "<<", "<=", ">>", ">=":
			default:
				return errors.New("Unknown date operator")
			}
			if d.Unix < -62135596800 || d.Unix > 253402300799 {
				return errors.New("Invalid date")
			}
		}
		for _, list := range [][]core.StringFilter{v.TitleFilters, v.StudioFilters} {
			for _, s := range list {
				switch s.Op {
				case "=", "!=", "==", "!==", "<=", ">=":
				default:
					return fmt.Errorf("Unknown text operator")
				}
			}
		}
		return group(v.Group, depth+1)
	}
	return walk(f, 0)
}
