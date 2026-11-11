package pluginitems

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// DefaultMatchThreshold is the default confidence score at or above which an
// automatic match is accepted as chosen when no manual pin exists (spec §7.2).
const DefaultMatchThreshold = 0.85

// MatchRecord is a persisted cross-kind match row in plugin_matches (spec §2.3, §7).
type MatchRecord struct {
	ItemRef      core.ItemRef `json:"itemRef"`
	CandidateRef string       `json:"candidateRef"`
	Score        float64      `json:"score"`
	Reasons      []string     `json:"reasons,omitempty"`
	Source       string       `json:"source"` // "auto" | "manual" | pluginID
	Chosen       bool         `json:"chosen"`
	UpdatedAt    int64        `json:"updatedAt"`
}

// SaveMatches persists automated match scores for itemRef from source
// (e.g. "auto" or matcherPluginID). Rows with source='manual' or a manual
// pin on itemRef are never overwritten by automated matcher runs (spec §7.2).
func (s *Service) SaveMatches(ctx context.Context, itemRef core.ItemRef, source string, scores []core.KindMatchScore, threshold float64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("pluginitems: nil service")
	}
	if !itemRef.Valid() {
		return fmt.Errorf("pluginitems: invalid item_ref %q", itemRef)
	}
	source = strings.TrimSpace(source)
	if source == "" || source == "manual" {
		source = "auto"
	}
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultMatchThreshold
	}

	now := s.now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Check whether a manual pin already exists for this itemRef.
	var manualCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM plugin_matches WHERE item_ref = ? AND source = 'manual' AND chosen = 1`,
		string(itemRef)).Scan(&manualCount); err != nil {
		return err
	}
	hasManualPin := manualCount > 0

	// Determine the best automated candidate at or above threshold when no manual pin exists.
	bestCandidate := ""
	bestScore := -1.0
	if !hasManualPin {
		for _, sc := range scores {
			cand := strings.TrimSpace(sc.CandidateRef)
			if cand == "" {
				continue
			}
			if sc.Score >= threshold && sc.Score > bestScore {
				bestScore = sc.Score
				bestCandidate = cand
			}
		}
		if bestCandidate != "" {
			if _, err := tx.ExecContext(ctx,
				`UPDATE plugin_matches SET chosen = 0 WHERE item_ref = ? AND source <> 'manual'`,
				string(itemRef)); err != nil {
				return err
			}
		}
	}

	for _, sc := range scores {
		cand := strings.TrimSpace(sc.CandidateRef)
		if cand == "" {
			continue
		}
		score := sc.Score
		if score < 0 {
			score = 0
		}
		if score > 1 {
			score = 1
		}
		reasonsJSON := "[]"
		if len(sc.Reasons) > 0 {
			if b, err := json.Marshal(sc.Reasons); err == nil {
				reasonsJSON = string(b)
			}
		}
		chosen := 0
		if !hasManualPin && cand == bestCandidate {
			chosen = 1
		}

		// Preserve manual rows: only update if the existing row is not manual.
		_, err := tx.ExecContext(ctx, `
			INSERT INTO plugin_matches (
				item_ref, candidate_ref, score, reasons, source, chosen, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (item_ref, candidate_ref) DO UPDATE SET
				score      = excluded.score,
				reasons    = excluded.reasons,
				source     = excluded.source,
				chosen     = excluded.chosen,
				updated_at = excluded.updated_at
			WHERE plugin_matches.source <> 'manual'`,
			string(itemRef), cand, score, reasonsJSON, source, chosen, now,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// PinMatch sets a manual match override (source='manual', chosen=1, score=1.0)
// for itemRef and clears chosen on any other candidates for itemRef (spec §7.2).
func (s *Service) PinMatch(ctx context.Context, itemRef core.ItemRef, candidateRef string, reasons []string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("pluginitems: nil service")
	}
	if !itemRef.Valid() {
		return fmt.Errorf("pluginitems: invalid item_ref %q", itemRef)
	}
	candidateRef = strings.TrimSpace(candidateRef)
	if candidateRef == "" {
		return fmt.Errorf("pluginitems: candidate_ref is required")
	}

	reasonsJSON := "[]"
	if len(reasons) > 0 {
		if b, err := json.Marshal(reasons); err == nil {
			reasonsJSON = string(b)
		}
	}
	now := s.now().Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE plugin_matches SET chosen = 0 WHERE item_ref = ?`,
		string(itemRef)); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO plugin_matches (
			item_ref, candidate_ref, score, reasons, source, chosen, updated_at
		) VALUES (?, ?, 1.0, ?, 'manual', 1, ?)
		ON CONFLICT (item_ref, candidate_ref) DO UPDATE SET
			score      = 1.0,
			reasons    = excluded.reasons,
			source     = 'manual',
			chosen     = 1,
			updated_at = excluded.updated_at`,
		string(itemRef), candidateRef, reasonsJSON, now,
	); err != nil {
		return err
	}

	return tx.Commit()
}

// ClearManualMatch removes manual pin rows for itemRef.
func (s *Service) ClearManualMatch(ctx context.Context, itemRef core.ItemRef) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("pluginitems: nil service")
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM plugin_matches WHERE item_ref = ? AND source = 'manual'`,
		string(itemRef),
	)
	return err
}

// ListMatches returns all stored match candidates for itemRef, ordered with
// chosen / manual pins first, then descending score.
func (s *Service) ListMatches(ctx context.Context, itemRef core.ItemRef) ([]MatchRecord, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("pluginitems: nil service")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT item_ref, candidate_ref, score, reasons, source, chosen, updated_at
		FROM plugin_matches
		WHERE item_ref = ?
		ORDER BY chosen DESC, CASE WHEN source = 'manual' THEN 1 ELSE 0 END DESC, score DESC, candidate_ref ASC`,
		string(itemRef),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MatchRecord
	for rows.Next() {
		var (
			rec                 MatchRecord
			refStr, reasonsJSON string
			chosenInt           int
		)
		if err := rows.Scan(
			&refStr, &rec.CandidateRef, &rec.Score, &reasonsJSON, &rec.Source, &chosenInt, &rec.UpdatedAt,
		); err != nil {
			return nil, err
		}
		rec.ItemRef = core.ItemRef(refStr)
		rec.Chosen = chosenInt != 0
		if reasonsJSON != "" && reasonsJSON != "[]" {
			_ = json.Unmarshal([]byte(reasonsJSON), &rec.Reasons)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// BestMatch returns the active match for itemRef: either a chosen/manual pin
// or the highest-scoring automated match with score >= threshold (spec §7.2).
func (s *Service) BestMatch(ctx context.Context, itemRef core.ItemRef, threshold float64) (MatchRecord, bool, error) {
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultMatchThreshold
	}
	matches, err := s.ListMatches(ctx, itemRef)
	if err != nil {
		return MatchRecord{}, false, err
	}
	for _, m := range matches {
		if m.Chosen || m.Source == "manual" || m.Score >= threshold {
			return m, true, nil
		}
	}
	return MatchRecord{}, false, nil
}

// MatchItem invokes a matcher plugin's POST /ether/v1/kinds/{kind}/match route
// for an item against candidate releases and persists the resulting scores in
// plugin_matches (spec §7).
func (s *Service) MatchItem(ctx context.Context, caller Caller, matcherPluginID, kindKey string, item core.KindItem, candidates []core.SourceCandidate, settings map[string]any, threshold float64) ([]core.KindMatchScore, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("pluginitems: nil service")
	}
	if caller == nil {
		s.workersMu.Lock()
		caller = s.caller
		s.workersMu.Unlock()
	}
	if caller == nil {
		return nil, fmt.Errorf("pluginitems: nil plugin route caller")
	}
	reqBody, err := json.Marshal(core.KindMatchRequest{
		Item:       item,
		Candidates: candidates,
		Settings:   settings,
	})
	if err != nil {
		return nil, err
	}
	resp, err := caller.Call(ctx, matcherPluginID, plugins.Request{
		PluginID: matcherPluginID,
		Method:   http.MethodPost,
		Path:     fmt.Sprintf("/ether/v1/kinds/%s/match", kindKey),
		Headers:  map[string]string{"content-type": "application/json"},
		Body:     reqBody,
	})
	if err != nil {
		return nil, err
	}
	if err := checkPluginResponseError(resp); err != nil {
		return nil, err
	}
	var matchResp core.KindMatchResponse
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &matchResp); err != nil {
			return nil, fmt.Errorf("pluginitems: decode match response: %w", err)
		}
	}
	if item.Ref.Valid() && len(matchResp.Matches) > 0 {
		if err := s.SaveMatches(ctx, item.Ref, matcherPluginID, matchResp.Matches, threshold); err != nil {
			return nil, err
		}
	}
	return matchResp.Matches, nil
}
