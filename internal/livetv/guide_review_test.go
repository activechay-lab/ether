package livetv

import (
	"context"
	"encoding/json"
	"testing"
)

func reviewSelections(t *testing.T, s *Service) []GuideCommitSelection {
	t.Helper()
	ctx := context.Background()
	result, e := s.MatchGuides(ctx, GuideMatchRequest{AI: "propose"})
	if e != nil {
		t.Fatal(e)
	}
	var selections []GuideCommitSelection
	for _, p := range result.Changes {
		c, e := s.Channel(ctx, p.ChannelID)
		if e != nil {
			t.Fatal(e)
		}
		selections = append(selections, GuideCommitSelection{ChannelID: c.ID, ExpectedGuideID: c.GuideID, ExpectedGuideSourceID: c.GuideSourceID, GuideID: p.GuideID, GuideSourceID: p.GuideSourceID, ProposalKey: p.ProposalKey})
	}
	if len(selections) < 2 {
		t.Fatal("expected multiple proposals")
	}
	return selections
}
func TestGuideReviewAtomicApplyAndUndo(t *testing.T) {
	f := &guideFake{labels: map[string]string{}}
	s := seedGuideAI(t, f, 4)
	ctx := context.Background()
	selections := reviewSelections(t, s)
	result, e := s.MatchGuides(ctx, GuideMatchRequest{AI: "commit", Reviewed: true, Selections: selections})
	if e != nil {
		t.Fatal(e)
	}
	if result.Matched != len(selections) || result.UndoToken == "" {
		t.Fatalf("missing atomic receipt %+v", result)
	}
	c, e := s.Channel(ctx, selections[0].ChannelID)
	if e != nil {
		t.Fatal(e)
	}
	c.Name = "Owner edited name"
	b, _ := json.Marshal(c)
	if _, e = s.o.DB.Exec(`UPDATE livetv_channels SET data=? WHERE id=?`, string(b), c.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.MatchGuides(ctx, GuideMatchRequest{AI: "undo", Reviewed: true, UndoToken: result.UndoToken})
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range selections {
		c, e := s.Channel(ctx, v.ChannelID)
		if e != nil {
			t.Fatal(e)
		}
		if c.GuideID != v.ExpectedGuideID || c.GuideSourceID != v.ExpectedGuideSourceID {
			t.Fatalf("not restored %+v", c)
		}
	}
	c, _ = s.Channel(ctx, selections[0].ChannelID)
	if c.Name != "Owner edited name" {
		t.Fatal("undo clobbered unrelated edit")
	}
	if _, e = s.MatchGuides(ctx, GuideMatchRequest{AI: "undo", Reviewed: true, UndoToken: result.UndoToken}); e == nil {
		t.Fatal("undo receipt reusable")
	}
}
func TestGuideReviewConflictRollsBackAll(t *testing.T) {
	f := &guideFake{labels: map[string]string{}}
	s := seedGuideAI(t, f, 4)
	ctx := context.Background()
	selections := reviewSelections(t, s)
	selections[1].ExpectedGuideID = "changed"
	_, e := s.MatchGuides(ctx, GuideMatchRequest{AI: "commit", Reviewed: true, Selections: selections})
	if e == nil {
		t.Fatal("conflict ignored")
	}
	c, _ := s.Channel(ctx, selections[0].ChannelID)
	if c.GuideID != selections[0].ExpectedGuideID || c.GuideSourceID != selections[0].ExpectedGuideSourceID {
		t.Fatal("partial commit")
	}
}
