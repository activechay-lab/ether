package livetv

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("livetv", 9, `CREATE TABLE livetv_guide_undo(token TEXT PRIMARY KEY, expires_ms BIGINT NOT NULL, data TEXT NOT NULL);`)
}

// Only mapping fields are written or retained in an undo receipt. Names, stream
// credentials and unrelated owner edits never enter this journal.
type GuideCommitSelection struct {
	ChannelID             string `json:"channelId"`
	GuideSourceID         string `json:"guideSourceId"`
	GuideID               string `json:"guideId"`
	ExpectedGuideSourceID string `json:"expectedGuideSourceId"`
	ExpectedGuideID       string `json:"expectedGuideId"`
	ProposalKey           string `json:"proposalKey,omitempty"`
}
type guideUndoEntry struct{ ChannelID, BeforeSource, BeforeGuide, AfterSource, AfterGuide string }

func (s *Service) commitGuideReview(ctx context.Context, req GuideMatchRequest) (GuideMatchResult, error) {
	res := GuideMatchResult{Changes: []GuideMatchChange{}}
	if !req.Reviewed || req.DryRun {
		return res, fmt.Errorf("explicit owner review required")
	}
	if req.AI == "commit" {
		if len(req.Selections) == 0 || len(req.Selections) > 10000 {
			return res, fmt.Errorf("select between 1 and 10000 mappings")
		}
		// Validate AI proposals using the same fingerprint path as the old Apply.
		keys := map[string]string{}
		var ids []string
		for _, v := range req.Selections {
			if v.ProposalKey != "" {
				keys[v.ChannelID] = v.ProposalKey
				ids = append(ids, v.ChannelID)
			}
		}
		if len(ids) > 0 {
			preview, e := s.MatchGuides(ctx, GuideMatchRequest{AI: "apply", Reviewed: true, DryRun: true, ChannelIDs: ids, ProposalKeys: keys, GuideSourceID: req.GuideSourceID})
			if e != nil {
				return res, e
			}
			byID := map[string]GuideMatchChange{}
			for _, v := range preview.Changes {
				byID[v.ChannelID] = v
			}
			for _, v := range req.Selections {
				if v.ProposalKey == "" {
					continue
				}
				p, ok := byID[v.ChannelID]
				if !ok || p.GuideID != v.GuideID || p.GuideSourceID != v.GuideSourceID {
					return res, fmt.Errorf("proposal changed; review again")
				}
			}
		}
	}
	tx, e := s.o.DB.BeginTx(ctx, nil)
	if e != nil {
		return res, e
	}
	defer tx.Rollback()
	var entries []guideUndoEntry
	if req.AI == "undo" {
		var raw []byte
		e = tx.QueryRowContext(ctx, `SELECT data FROM livetv_guide_undo WHERE token=? AND expires_ms>?`, req.UndoToken, s.o.Now().UnixMilli()).Scan(&raw)
		if e != nil {
			return res, fmt.Errorf("undo expired or already used")
		}
		if e = json.Unmarshal(raw, &entries); e != nil {
			return res, e
		}
	} else {
		seen := map[string]bool{}
		for _, v := range req.Selections {
			if seen[v.ChannelID] {
				return res, fmt.Errorf("duplicate channel selection")
			}
			seen[v.ChannelID] = true
			if v.GuideID == "" || v.GuideSourceID == "" {
				return res, fmt.Errorf("choose a guide channel")
			}
			var exists int
			e = tx.QueryRowContext(ctx, guideChannelsCTE+`SELECT COUNT(*) FROM gc WHERE source_id=? AND guide_id=?`, v.GuideSourceID, v.GuideID).Scan(&exists)
			if e != nil {
				return res, e
			}
			if exists == 0 {
				return res, fmt.Errorf("guide channel no longer exists")
			}
			entries = append(entries, guideUndoEntry{ChannelID: v.ChannelID, BeforeSource: v.ExpectedGuideSourceID, BeforeGuide: v.ExpectedGuideID, AfterSource: v.GuideSourceID, AfterGuide: v.GuideID})
		}
	}
	for i, v := range entries {
		var raw []byte
		var c Channel
		if e = tx.QueryRowContext(ctx, `SELECT data FROM livetv_channels WHERE id=?`, v.ChannelID).Scan(&raw); e != nil {
			return res, e
		}
		if e = json.Unmarshal(raw, &c); e != nil {
			return res, e
		}
		beforeSource, beforeGuide, afterSource, afterGuide := v.BeforeSource, v.BeforeGuide, v.AfterSource, v.AfterGuide
		if req.AI == "undo" {
			beforeSource, beforeGuide, afterSource, afterGuide = v.AfterSource, v.AfterGuide, v.BeforeSource, v.BeforeGuide
		}
		// SourceID is implicit when guideSourceId is blank.
		currentSource := c.GuideSourceID
		if currentSource == "" && c.GuideID != "" {
			currentSource = c.SourceID
		}
		expectedSource := beforeSource
		if expectedSource == "" && beforeGuide != "" {
			expectedSource = c.SourceID
		}
		if c.Kind != "iptv" || c.GuideID != beforeGuide || currentSource != expectedSource {
			return res, fmt.Errorf("channel mapping changed; review again")
		}
		c.GuideID = afterGuide
		c.GuideSourceID = afterSource
		if c.GuideSourceID == c.SourceID {
			c.GuideSourceID = ""
		}
		if req.AI == "commit" {
			entries[i].AfterSource = c.GuideSourceID
		}
		b, _ := json.Marshal(c)
		result, e := tx.ExecContext(ctx, `UPDATE livetv_channels SET data=? WHERE id=? AND CAST(data AS TEXT)=?`, string(b), c.ID, string(raw))
		if e != nil {
			return res, e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return res, e
		}
		if n != 1 {
			return res, fmt.Errorf("channel mapping changed; review again")
		}
	}
	if req.AI == "undo" {
		_, e = tx.ExecContext(ctx, `DELETE FROM livetv_guide_undo WHERE token=?`, req.UndoToken)
	} else {
		res.UndoToken = rand.Text()
		b, _ := json.Marshal(entries)
		_, e = tx.ExecContext(ctx, `INSERT INTO livetv_guide_undo(token,expires_ms,data) VALUES(?,?,?)`, res.UndoToken, s.o.Now().Add(24*time.Hour).UnixMilli(), string(b))
	}
	if e != nil {
		return res, e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM livetv_guide_undo WHERE expires_ms<=?`, s.o.Now().UnixMilli()); e != nil {
		return res, e
	}
	res.Matched = len(entries)
	return res, tx.Commit()
}
