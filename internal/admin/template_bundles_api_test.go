package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
)

type bundleTestStudio struct {
	*fakeStudio
	nextID int
}

func (b *bundleTestStudio) ListSpecs(context.Context) ([]core.CollectionSpec, error) {
	out := make([]core.CollectionSpec, 0, len(b.specs))
	for _, s := range b.specs {
		out = append(out, s)
	}
	return out, nil
}

func (b *bundleTestStudio) CreateSpec(_ context.Context, s core.CollectionSpec) (*core.CollectionSpec, error) {
	b.nextID++
	s.ID = "created-" + strconv.Itoa(b.nextID)
	b.specs[s.ID] = s
	return &s, nil
}

func (b *bundleTestStudio) Templates() []core.CollectionTemplate {
	return collections.Templates()
}

// tsBundleConsumerContract mirrors the exact TypeScript contracts in
// web/src/lib/contract.ts (CollectionTemplateBundle,
// CollectionTemplateBundlePreview, CollectionTemplateBundleApplyResult) to
// verify the wire JSON emitted by the Go HTTP handlers.
type tsBundleConsumerContract struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	TemplateIDs []string `json:"templateIds"`
}

type tsPreviewItemConsumerContract struct {
	TemplateID string `json:"templateId"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Action     string `json:"action"`
	ExistingID string `json:"existingId"`
}

type tsPreviewConsumerContract struct {
	Bundle     tsBundleConsumerContract        `json:"bundle"`
	Items      []tsPreviewItemConsumerContract `json:"items"`
	WillCreate int                             `json:"willCreate"`
	WillSkip   int                             `json:"willSkip"`
}

type tsApplyConsumerContract struct {
	BundleID     string                          `json:"bundleId"`
	Bundle       tsBundleConsumerContract        `json:"bundle"`
	Items        []tsPreviewItemConsumerContract `json:"items"`
	Created      []map[string]any                `json:"created"`
	Skipped      []tsPreviewItemConsumerContract `json:"skipped"`
	CreatedCount int                             `json:"createdCount"`
	SkippedCount int                             `json:"skippedCount"`
}

func TestTemplateBundlesAPI(t *testing.T) {
	studio := &bundleTestStudio{fakeStudio: newFakeStudio(), nextID: 10}
	cfg := newStore(t, nil)
	h := admin.NewHandler(admin.Deps{Config: cfg, IsOwnerToken: storedOwner(cfg), Collections: studio})

	// 1. GET /ether/api/collections/template-bundles
	rec := serve(h, req(http.MethodGet, "/ether/api/collections/template-bundles", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET template-bundles: %d %s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Bundles []tsBundleConsumerContract `json:"bundles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode bundles: %v", err)
	}
	if len(listResp.Bundles) < 5 {
		t.Fatalf("expected at least 5 bundles, got %d", len(listResp.Bundles))
	}
	for _, b := range listResp.Bundles {
		if b.ID == "" || b.Name == "" || b.Description == "" || len(b.TemplateIDs) == 0 {
			t.Fatalf("bundle wire JSON missing canonical TS consumer fields: %+v", b)
		}
	}

	// 2. POST /ether/api/collections/template-bundles/streaming-originals/preview
	rec = serve(h, req(http.MethodPost, "/ether/api/collections/template-bundles/streaming-originals/preview", "{}"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST preview: %d %s", rec.Code, rec.Body.String())
	}
	var prevResp tsPreviewConsumerContract
	if err := json.Unmarshal(rec.Body.Bytes(), &prevResp); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if prevResp.Bundle.ID != "streaming-originals" || prevResp.Bundle.Name != "Streaming Originals" {
		t.Fatalf("unexpected preview bundle metadata: %+v", prevResp.Bundle)
	}
	if prevResp.WillCreate != 8 || prevResp.WillSkip != 0 || len(prevResp.Items) != 8 {
		t.Fatalf("expected all 8 templates to be created on clean studio, got %+v", prevResp)
	}

	// 3. POST /ether/api/collections/template-bundles/streaming-originals/apply
	rec = serve(h, req(http.MethodPost, "/ether/api/collections/template-bundles/streaming-originals/apply", "{}"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST apply: %d %s", rec.Code, rec.Body.String())
	}
	var applyResp tsApplyConsumerContract
	if err := json.Unmarshal(rec.Body.Bytes(), &applyResp); err != nil {
		t.Fatalf("decode apply: %v", err)
	}
	if applyResp.BundleID != "streaming-originals" || applyResp.Bundle.Name != "Streaming Originals" || applyResp.CreatedCount != 8 || applyResp.SkippedCount != 0 || len(applyResp.Created) != 8 || len(applyResp.Skipped) != 0 {
		t.Fatalf("unexpected apply response: %+v", applyResp)
	}

	// 4. Re-previewing and re-applying after apply reports all as skip_existing.
	rec = serve(h, req(http.MethodPost, "/ether/api/collections/template-bundles/streaming-originals/preview", "{}"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST second preview: %d %s", rec.Code, rec.Body.String())
	}
	var secondPrev tsPreviewConsumerContract
	if err := json.Unmarshal(rec.Body.Bytes(), &secondPrev); err != nil {
		t.Fatalf("decode second preview: %v", err)
	}
	if secondPrev.WillCreate != 0 || secondPrev.WillSkip != 8 {
		t.Fatalf("expected idempotent preview after apply (willCreate=0, willSkip=8), got %+v", secondPrev)
	}

	rec = serve(h, req(http.MethodPost, "/ether/api/collections/template-bundles/streaming-originals/apply", "{}"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST second apply: %d %s", rec.Code, rec.Body.String())
	}
	var secondApply tsApplyConsumerContract
	if err := json.Unmarshal(rec.Body.Bytes(), &secondApply); err != nil {
		t.Fatalf("decode second apply: %v", err)
	}
	if secondApply.CreatedCount != 0 || secondApply.SkippedCount != 8 || len(secondApply.Skipped) != 8 {
		t.Fatalf("expected idempotent second apply (createdCount=0, skippedCount=8), got %+v", secondApply)
	}

	// 5. Unknown bundle returns 404.
	rec = serve(h, req(http.MethodPost, "/ether/api/collections/template-bundles/missing-bundle/preview", "{}"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown bundle, got %d %s", rec.Code, rec.Body.String())
	}
}
