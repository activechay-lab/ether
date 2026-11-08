package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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
		Bundles []collections.TemplateBundle `json:"bundles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode bundles: %v", err)
	}
	if len(listResp.Bundles) < 5 {
		t.Fatalf("expected at least 5 bundles, got %d", len(listResp.Bundles))
	}

	// 2. POST /ether/api/collections/template-bundles/streaming-originals/preview
	rec = serve(h, req(http.MethodPost, "/ether/api/collections/template-bundles/streaming-originals/preview", "{}"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST preview: %d %s", rec.Code, rec.Body.String())
	}
	var prevResp collections.TemplateBundlePreview
	if err := json.Unmarshal(rec.Body.Bytes(), &prevResp); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if prevResp.CreateCount != 8 || prevResp.SkipCount != 0 {
		t.Fatalf("expected all 8 templates to be created on clean studio, got %+v", prevResp)
	}

	// 3. POST /ether/api/collections/template-bundles/streaming-originals/apply
	rec = serve(h, req(http.MethodPost, "/ether/api/collections/template-bundles/streaming-originals/apply", "{}"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST apply: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"createCount":8`) || !strings.Contains(rec.Body.String(), `"skipCount":0`) {
		t.Fatalf("unexpected apply response: %s", rec.Body.String())
	}

	// 4. Re-previewing after apply reports all as skip_existing.
	rec = serve(h, req(http.MethodPost, "/ether/api/collections/template-bundles/streaming-originals/preview", "{}"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"createCount":0`) || !strings.Contains(rec.Body.String(), `"skipCount":8`) {
		t.Fatalf("expected idempotent preview after apply: %d %s", rec.Code, rec.Body.String())
	}

	// 5. Unknown bundle returns 404.
	rec = serve(h, req(http.MethodPost, "/ether/api/collections/template-bundles/missing-bundle/preview", "{}"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown bundle, got %d %s", rec.Code, rec.Body.String())
	}
}
