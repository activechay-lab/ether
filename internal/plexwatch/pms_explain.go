package plexwatch

import (
	"context"
	"fmt"
	"sort"

	"github.com/braedonsaunders/ether/internal/core"
)

// StepAside explain causes, as the console shows them. Each names one concrete
// reason a title is or is not hidden, so the owner never has to guess.
const (
	StepAsideCauseHeld            = "held"             // a step-aside server holds it; Ether hides it
	StepAsideCauseMissingEpisodes = "missing_episodes" // the server is missing aired episodes; the show stays visible
	StepAsideCauseNoEpisodeList   = "no_episode_list"  // Ether has not loaded this show's episode list yet
	StepAsideCauseNotHeld         = "not_held"         // no step-aside server holds this title
	StepAsideCauseScanCapped      = "scan_capped"      // matching stopped at the limit; held set is incomplete
	StepAsideCauseScanFailed      = "scan_failed"      // the last scan failed; held set may be stale
	StepAsideCauseNotScanned      = "not_scanned"      // step-aside has not scanned this server yet
	StepAsideCauseStepAsideOff    = "step_aside_off"   // no server has step-aside on
	StepAsideCauseNotFound        = "not_found"        // this key is not a movie or show Ether knows
)

// StepAsideExplanation answers "why is this title still showing (or hidden)?" for
// one movie or show. It is bounded work: the requested title's rows only, never
// a catalogue scan and never a network call.
type StepAsideExplanation struct {
	Key     int64    `json:"key"`
	Kind    string   `json:"kind"`
	Title   string   `json:"title"`
	Hidden  bool     `json:"hidden"`
	Cause   string   `json:"cause"`
	Servers []string `json:"servers"`
	// Show fields, populated for shows.
	AiredRegular int      `json:"airedRegular"`
	HeldEpisodes int      `json:"heldEpisodes"`
	Missing      []string `json:"missing"`
	MissingMore  int      `json:"missingMore"`
	// Scan state of the servers consulted (worst of them).
	ScanState string `json:"scanState"`
}

const stepAsideExplainMissingCap = 50

// ExplainStepAside explains one key without scanning the catalogue or the
// server: only the key's own held rows, its aired-episode list (one indexed
// query), and the per-server scan state are read.
func (s *Servers) ExplainStepAside(ctx context.Context, key core.RatingKey) (StepAsideExplanation, error) {
	out := StepAsideExplanation{Key: int64(key), Missing: []string{}, Servers: []string{}}
	if s == nil || s.db == nil {
		return out, nil
	}

	servers, err := s.stepAsideServers(ctx)
	if err != nil {
		return out, err
	}
	if len(servers) == 0 {
		out.Cause = StepAsideCauseStepAsideOff
		out.Kind, out.Title = s.describeKey(ctx, key)
		return out, nil
	}
	out.ScanState = worstScanState(servers)

	switch key.Kind() {
	case core.KindMovie:
		return s.explainMovie(ctx, out, key, servers)
	case core.KindShow:
		return s.explainShow(ctx, out, key, servers)
	default:
		out.Cause = StepAsideCauseNotFound
		return out, nil
	}
}

type explainServer struct {
	id    int64
	name  string
	state string
}

func (s *Servers) stepAsideServers(ctx context.Context) ([]explainServer, error) {
	nowNS := s.now().UnixNano()
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,held_synced_at,held_next_run,held_next_allowed,held_failures,problem
		FROM plexwatch_pms WHERE step_aside<>0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []explainServer
	for rows.Next() {
		var e explainServer
		var syncedAt, nextRun, nextAllowed int64
		var failures int
		var problem string
		if err := rows.Scan(&e.id, &e.name, &syncedAt, &nextRun, &nextAllowed, &failures, &problem); err != nil {
			return nil, err
		}
		e.state = stepAsideStateFrom(syncedAt, nextRun, nextAllowed, failures, problem, nowNS)
		out = append(out, e)
	}
	return out, rows.Err()
}

func worstScanState(servers []explainServer) string {
	rank := map[string]int{
		StepAsideStateComplete: 0,
		StepAsideStateNever:    1,
		StepAsideStateMatching: 2,
		StepAsideStateFailed:   3,
	}
	worst := StepAsideStateComplete
	for _, srv := range servers {
		if rank[srv.state] > rank[worst] {
			worst = srv.state
		}
	}
	return worst
}

func (s *Servers) explainMovie(ctx context.Context, out StepAsideExplanation, key core.RatingKey, servers []explainServer) (StepAsideExplanation, error) {
	out.Kind = "movie"
	out.Title = s.titleOf(ctx, key)
	holders, err := s.movieHolders(ctx, key)
	if err != nil {
		return out, err
	}
	for _, e := range servers {
		if _, ok := holders[e.id]; ok {
			out.Servers = append(out.Servers, e.name)
		}
	}
	switch {
	case len(out.Servers) > 0:
		out.Hidden = true
		out.Cause = StepAsideCauseHeld
	case out.ScanState == StepAsideStateFailed:
		out.Cause = StepAsideCauseScanFailed
	case out.ScanState == StepAsideStateMatching:
		out.Cause = StepAsideCauseScanCapped
	case out.ScanState == StepAsideStateNever:
		out.Cause = StepAsideCauseNotScanned
	default:
		out.Cause = StepAsideCauseNotHeld
	}
	return out, nil
}

func (s *Servers) explainShow(ctx context.Context, out StepAsideExplanation, key core.RatingKey, servers []explainServer) (StepAsideExplanation, error) {
	out.Kind = "show"
	out.Title = s.titleOf(ctx, key)

	heldRows, err := s.heldEpisodeRows(ctx, key)
	if err != nil {
		return out, err
	}
	holders, err := s.showHolders(ctx, key)
	if err != nil {
		return out, err
	}
	for _, e := range servers {
		if _, ok := holders[e.id]; ok {
			out.Servers = append(out.Servers, e.name)
		}
	}
	out.HeldEpisodes = len(heldRows)

	// Ether's aired, regular (season > 0) episode list for this show.
	var aired []core.RatingKey
	if ac, ok := s.catalog.(airedShowCatalog); ok {
		m, err := ac.AiredShowEpisodes(ctx, []core.RatingKey{key})
		if err != nil {
			return out, err
		}
		for _, ep := range m[key] {
			if ep.Season() > 0 && ep.Episode() > 0 {
				aired = append(aired, ep)
			}
		}
	}
	out.AiredRegular = len(aired)

	if out.HeldEpisodes == 0 {
		// No step-aside server holds any episode of this show.
		switch out.ScanState {
		case StepAsideStateFailed:
			out.Cause = StepAsideCauseScanFailed
		case StepAsideStateMatching:
			out.Cause = StepAsideCauseScanCapped
		case StepAsideStateNever:
			out.Cause = StepAsideCauseNotScanned
		default:
			out.Cause = StepAsideCauseNotHeld
		}
		return out, nil
	}

	if out.AiredRegular == 0 {
		// The server has episodes but Ether has no local aired episode list to
		// judge completeness against, so the show stays visible.
		out.Cause = StepAsideCauseNoEpisodeList
		return out, nil
	}

	held := make(map[core.RatingKey]struct{}, len(heldRows))
	for _, ep := range heldRows {
		held[ep] = struct{}{}
	}
	sort.Slice(aired, func(i, j int) bool { return aired[i] < aired[j] })
	var missing []string
	for _, ep := range aired {
		if _, ok := held[ep]; ok {
			continue
		}
		if len(missing) >= stepAsideExplainMissingCap {
			out.MissingMore++
			continue
		}
		missing = append(missing, fmt.Sprintf("S%02dE%02d", ep.Season(), ep.Episode()))
	}
	out.Missing = missing
	if len(missing) == 0 && out.MissingMore == 0 {
		out.Hidden = true
		out.Cause = StepAsideCauseHeld
		return out, nil
	}
	out.Cause = StepAsideCauseMissingEpisodes
	return out, nil
}

// movieHolders returns the ids of step-aside servers holding key.
func (s *Servers) movieHolders(ctx context.Context, key core.RatingKey) (map[int64]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT h.server_id FROM plexwatch_pms_held h
		JOIN plexwatch_pms p ON p.id=h.server_id
		WHERE p.step_aside<>0 AND h.rating_key=?`, int64(key))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]struct{}{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// showHolders returns the ids of step-aside servers holding any episode of key.
func (s *Servers) showHolders(ctx context.Context, key core.RatingKey) (map[int64]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT h.server_id FROM plexwatch_pms_held h
		JOIN plexwatch_pms p ON p.id=h.server_id
		WHERE p.step_aside<>0 AND h.show_key=? AND h.rating_key/?=4`, int64(key), pmsKeyKindBase)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]struct{}{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// heldEpisodeRows returns the held episode keys for a show across step-aside
// servers.
func (s *Servers) heldEpisodeRows(ctx context.Context, showKey core.RatingKey) ([]core.RatingKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT h.rating_key FROM plexwatch_pms_held h
		JOIN plexwatch_pms p ON p.id=h.server_id
		WHERE p.step_aside<>0 AND h.show_key=? AND h.rating_key/?=4`, int64(showKey), pmsKeyKindBase)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.RatingKey{}
	for rows.Next() {
		var rk int64
		if err := rows.Scan(&rk); err != nil {
			return nil, err
		}
		out = append(out, core.RatingKey(rk))
	}
	return out, rows.Err()
}

// titleOf reads a key's display title from the catalogue, best-effort.
func (s *Servers) titleOf(ctx context.Context, key core.RatingKey) string {
	if s.catalog == nil {
		return ""
	}
	t, err := s.catalog.Get(ctx, key)
	if err != nil || t == nil {
		return ""
	}
	return t.Title
}

// describeKey sets Kind and Title for a key when no step-aside server is active.
func (s *Servers) describeKey(ctx context.Context, key core.RatingKey) (string, string) {
	switch key.Kind() {
	case core.KindMovie:
		return "movie", s.titleOf(ctx, key)
	case core.KindShow:
		return "show", s.titleOf(ctx, key)
	default:
		return "", s.titleOf(ctx, key)
	}
}
