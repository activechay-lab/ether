package catalog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type gateFilterKey struct{}

// WithGateFilter attaches an availability SQL gate filter to ctx so Search
// evaluates the sticky release gate inside its SQL queries with no extra round
// trips or Go-side filtering of mirrored candidates.
func WithGateFilter(ctx context.Context, f core.Filter) context.Context {
	return context.WithValue(ctx, gateFilterKey{}, f)
}

// GateFilterFrom reads a GateFilter attached by WithGateFilter.
func GateFilterFrom(ctx context.Context) (core.Filter, bool) {
	f, ok := ctx.Value(gateFilterKey{}).(core.Filter)
	return f, ok
}

func minuteBound(ts int64) int64 {
	if ts == 0 {
		return 0
	}
	return ts / 60
}

func normalizeGateCacheFilter(f core.Filter) core.Filter {
	f.MovieGateCutoff = minuteBound(f.MovieGateCutoff)
	f.ShowGateCutoff = minuteBound(f.ShowGateCutoff)
	f.EpisodeGateCutoff = minuteBound(f.EpisodeGateCutoff)
	f.GateNow = minuteBound(f.GateNow)
	return f
}

func clearGateFields(f *core.Filter) {
	f.GatePolicy = ""
	f.MovieGateCutoff = 0
	f.ShowGateCutoff = 0
	f.EpisodeGateCutoff = 0
	f.GateNow = 0
	f.GateUpcomingMovies = false
	f.GateRev = 0
}

func gateNowTS(f core.Filter, fallback int64) int64 {
	if f.GateNow != 0 {
		return f.GateNow
	}
	return fallback
}

// movieGateSQL builds the sticky release-gate clause for a movie row aliased as
// alias. Unary + on released_ts and key prevents SQLite's OR-by-union planner
// from abandoning the outer sort/rank index scan, while the correlated EXISTS
// probes availability_gate by its (policy, rating_key) primary key only for
// recent movies inside the evidence window.
func movieGateSQL(alias string, f core.Filter, nowTS int64) (string, []any) {
	if f.GatePolicy == "" || f.MovieGateCutoff == 0 {
		return "", nil
	}
	gNow := gateNowTS(f, nowTS)
	localStart := int64(core.LocalKey(0))
	localEnd := localStart + 1_000_000_000_000_000
	var sb strings.Builder
	sb.WriteString("(+")
	sb.WriteString(alias)
	sb.WriteString(".released_ts<=?")
	args := []any{f.MovieGateCutoff}
	if f.GateUpcomingMovies {
		sb.WriteString(" OR (+")
		sb.WriteString(alias)
		sb.WriteString(".released_ts>? AND NOT EXISTS (SELECT 1 FROM catalog_home_release uhr WHERE uhr.key=")
		sb.WriteString(alias)
		sb.WriteString(".key AND uhr.checked=1 AND uhr.earliest_ts>0 AND uhr.earliest_ts<=? LIMIT 1 OFFSET 0))")
		args = append(args, gNow, gNow)
	}
	sb.WriteString(" OR (+")
	sb.WriteString(alias)
	sb.WriteString(".key>=? AND +")
	sb.WriteString(alias)
	sb.WriteString(".key<?) OR EXISTS (SELECT 1 FROM availability_gate ag WHERE ag.policy=? AND ag.rating_key=")
	sb.WriteString(alias)
	sb.WriteString(".key LIMIT 1 OFFSET 0)")
	args = append(args, localStart, localEnd, f.GatePolicy)
	if f.DigitalDateEvidence {
		sb.WriteString(" OR EXISTS (SELECT 1 FROM catalog_home_release dh WHERE dh.key=")
		sb.WriteString(alias)
		sb.WriteString(".key AND dh.checked=1 AND dh.earliest_ts<>0 AND dh.earliest_ts<=? LIMIT 1 OFFSET 0)")
		args = append(args, homeReleaseDeadline(time.Unix(gNow, 0).UTC(), f.HomeReleaseOffsetDays))
	}
	if len(f.AvailabilityKeepKeys) > 0 {
		sb.WriteString(" OR +")
		sb.WriteString(alias)
		sb.WriteString(".key IN (")
		sb.WriteString(placeholders(len(f.AvailabilityKeepKeys)))
		sb.WriteString(")")
		for _, k := range f.AvailabilityKeepKeys {
			args = append(args, k)
		}
	}
	sb.WriteString(")")
	return sb.String(), args
}

// showGateSQL builds the sticky release-gate clause for a show row aliased as
// alias. Shows premiered at or before ShowGateCutoff bypass the episode check
// immediately; first-day series require at least one released regular episode
// that is either settled or present in availability_gate (or a synthetic S01E01
// gate row when TMDB has not mirrored any regular episodes yet).
func showGateSQL(alias string, f core.Filter, nowTS int64) (string, []any) {
	if f.GatePolicy == "" || f.ShowGateCutoff == 0 {
		return "", nil
	}
	gNow := gateNowTS(f, nowTS)
	epCutoff := f.EpisodeGateCutoff
	if epCutoff == 0 {
		epCutoff = f.ShowGateCutoff
	}
	synthKey := fmt.Sprintf("(%d+(%s.key-%d)*10000000)", int64(core.EpisodeKey(0, 1, 1)), alias, int64(core.ShowKey(0)))
	clause := "(+" + alias + ".released_ts<=? OR +" + alias + ".released_ts>? OR " +
		"EXISTS (SELECT 1 FROM catalog_titles ce WHERE ce.show_key=" + alias + ".key AND ce.type='episode' AND ce.season>0 AND ce.release_known=1 AND ce.adult=0 AND ce.released_ts<=? AND (+ce.released_ts<=? OR EXISTS (SELECT 1 FROM availability_gate ag WHERE ag.policy=? AND ag.rating_key=ce.key LIMIT 1 OFFSET 0)) LIMIT 1 OFFSET 0) OR " +
		"(NOT EXISTS (SELECT 1 FROM catalog_titles ce WHERE ce.show_key=" + alias + ".key AND ce.type='episode' AND ce.season>0 LIMIT 1 OFFSET 0) AND EXISTS (SELECT 1 FROM availability_gate ag WHERE ag.policy=? AND ag.rating_key=" + synthKey + " LIMIT 1 OFFSET 0)))"
	args := []any{f.ShowGateCutoff, gNow, gNow, epCutoff, f.GatePolicy, f.GatePolicy}
	return clause, args
}

// episodeGateSQL builds the sticky release-gate clause for an episode row.
func episodeGateSQL(alias string, f core.Filter, nowTS int64) (string, []any) {
	if f.GatePolicy == "" || f.EpisodeGateCutoff == 0 {
		return "", nil
	}
	gNow := gateNowTS(f, nowTS)
	clause := "(+" + alias + ".released_ts<=? OR +" + alias + ".released_ts>? OR EXISTS (SELECT 1 FROM availability_gate ag WHERE ag.policy=? AND ag.rating_key=" + alias + ".key LIMIT 1 OFFSET 0)"
	args := []any{f.EpisodeGateCutoff, gNow, f.GatePolicy}
	if len(f.AvailabilityKeepKeys) > 0 {
		clause += " OR +" + alias + ".key IN (" + placeholders(len(f.AvailabilityKeepKeys)) + ")"
		for _, k := range f.AvailabilityKeepKeys {
			args = append(args, k)
		}
	}
	clause += ")"
	return clause, args
}

// browseGateConds returns the sticky availability_gate SQL predicates for
// browseWhere on table alias "t".
func browseGateConds(f core.Filter, nowTS int64) ([]string, []any) {
	if f.GatePolicy == "" {
		return nil, nil
	}
	var conds []string
	var args []any
	if f.Type == "" || f.Type == core.TypeMovie {
		if mc, ma := movieGateSQL("t", f, nowTS); mc != "" {
			if f.Type == core.TypeMovie {
				conds = append(conds, mc)
			} else {
				conds = append(conds, "(t.type<>'movie' OR "+mc+")")
			}
			args = append(args, ma...)
		}
	}
	if f.Type == "" || f.Type == core.TypeShow {
		if sc, sa := showGateSQL("t", f, nowTS); sc != "" {
			if f.Type == core.TypeShow {
				conds = append(conds, sc)
			} else {
				conds = append(conds, "(t.type<>'show' OR "+sc+")")
			}
			args = append(args, sa...)
		}
	}
	if (f.Type == core.TypeEpisode || f.Type == core.TypeSeason) && f.ShowGateCutoff != 0 {
		if sc, sa := showGateSQL("gsh", f, nowTS); sc != "" {
			conds = append(conds, "EXISTS (SELECT 1 FROM catalog_titles gsh WHERE gsh.key=t.show_key AND "+sc+" LIMIT 1 OFFSET 0)")
			args = append(args, sa...)
		}
	}
	if f.Type == core.TypeEpisode && f.EpisodeGateCutoff != 0 {
		if ec, ea := episodeGateSQL("t", f, nowTS); ec != "" {
			conds = append(conds, ec)
			args = append(args, ea...)
		}
	}
	return conds, args
}

// searchGateConds builds the SQL availability predicate across movie, show,
// season, and episode rows for Search queries when WithGateFilter is set on ctx.
func (c *Catalog) searchGateConds(ctx context.Context, alias string) (string, []any) {
	f, ok := GateFilterFrom(ctx)
	if !ok {
		return "", nil
	}
	nowTS := c.now().Unix()
	var parts []string
	var args []any
	if f.GatePolicy != "" {
		if mc, ma := movieGateSQL(alias, f, nowTS); mc != "" {
			parts = append(parts, "("+alias+".type<>'movie' OR "+mc+")")
			args = append(args, ma...)
		}
		if sc, sa := showGateSQL(alias, f, nowTS); sc != "" {
			parts = append(parts, "("+alias+".type<>'show' OR "+sc+")")
			args = append(args, sa...)
		}
		if f.ShowGateCutoff != 0 {
			if sc, sa := showGateSQL("gsh", f, nowTS); sc != "" {
				parts = append(parts, "("+alias+".type NOT IN ('season','episode') OR EXISTS (SELECT 1 FROM catalog_titles gsh WHERE gsh.key="+alias+".show_key AND "+sc+" LIMIT 1 OFFSET 0))")
				args = append(args, sa...)
			}
		}
		if ec, ea := episodeGateSQL(alias, f, nowTS); ec != "" {
			parts = append(parts, "("+alias+".type<>'episode' OR "+ec+")")
			args = append(args, ea...)
		}
	}
	if f.DigitalDiscovery && f.HiddenMovieSeq == 0 {
		digital := "1=0"
		if f.DigitalDateEvidence {
			digital = "EXISTS (SELECT 1 FROM catalog_home_release dh WHERE dh.key=" + alias + ".key AND dh.checked=1 AND dh.earliest_ts<>0 AND dh.earliest_ts<=?)"
		}
		if len(f.AvailabilityKeepKeys) > 0 {
			digital += " OR " + alias + ".key IN (" + placeholders(len(f.AvailabilityKeepKeys)) + ")"
		}
		localStart := int64(core.LocalKey(0))
		parts = append(parts, "("+alias+".type<>'movie' OR ("+alias+".key>=? AND "+alias+".key<?) OR ("+alias+".release_known=1 AND "+alias+".released_ts>? AND ?<>0) OR ("+digital+"))")
		args = append(args, localStart, localStart+1_000_000_000_000_000, nowTS, flagInt(c.config().ShowUpcomingMovies))
		if f.DigitalDateEvidence {
			args = append(args, homeReleaseDeadline(c.now(), f.HomeReleaseOffsetDays))
		}
		for _, k := range f.AvailabilityKeepKeys {
			args = append(args, k)
		}
	}
	for _, hidden := range []struct {
		kind string
		seq  int64
	}{
		{"movie", f.HiddenMovieSeq},
		{"show", f.HiddenShowSeq},
		{"episode", f.HiddenEpisodeSeq},
	} {
		if hidden.seq != 0 {
			parts = append(parts, "NOT EXISTS (SELECT 1 FROM availability_hidden ah WHERE ah.kind=? AND ah.from_seq<=? AND ah.to_seq>? AND ah.rating_key="+alias+".key)")
			args = append(args, hidden.kind, hidden.seq, hidden.seq)
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, " AND "), args
}

// ScheduledGateCandidates returns recent and near-horizon movies and episodes
// for seeding availability_title_checks so air/release-time retries wake via
// the due-items index without polling catalog_titles on idle ticks.
func (c *Catalog) ScheduledGateCandidates(ctx context.Context, movieSince, epSince, movieHorizon, epHorizon time.Time) ([]core.Title, error) {
	var out []core.Title
	if !movieSince.IsZero() {
		movies, err := c.queryTitles(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE type='movie' AND browsable=1 AND release_known=1 AND released_ts>? AND released_ts<=? ORDER BY key`, movieSince.Unix(), movieHorizon.Unix())
		if err != nil {
			return nil, err
		}
		out = append(out, movies...)
	}
	if !epSince.IsZero() {
		eps, err := c.queryTitles(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE type='episode' AND adult=0 AND release_known=1 AND released_ts>? AND released_ts<=? ORDER BY released_ts, key`, epSince.Unix(), epHorizon.Unix())
		if err != nil {
			return nil, err
		}
		out = append(out, eps...)
	}
	return out, nil
}
