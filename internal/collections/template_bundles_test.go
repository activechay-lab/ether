package collections_test

import (
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestTemplateBundlesPreviewAndIdempotentApply(t *testing.T) {
	e := newEnv(t)
	bundles := collections.TemplateBundles()
	if len(bundles) < 5 {
		t.Fatalf("expected at least 5 template bundles, got %d", len(bundles))
	}
	templates := collections.Templates()
	if len(templates) < 100 {
		t.Fatalf("expected at least 100 curated templates, got %d", len(templates))
	}
	byID := make(map[string]core.CollectionTemplate, len(templates))
	for _, tpl := range templates {
		byID[tpl.ID] = tpl
	}
	for _, b := range bundles {
		if b.ID == "" || b.Name == "" || b.Description == "" || len(b.TemplateIDs) == 0 {
			t.Fatalf("incomplete bundle metadata: %+v", b)
		}
		for _, tid := range b.TemplateIDs {
			if _, ok := byID[tid]; !ok {
				t.Fatalf("bundle %s references unknown template ID %q", b.ID, tid)
			}
		}
	}

	if _, err := collections.PreviewTemplateBundle(templates, nil, "unknown-bundle"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown bundle, got %v", err)
	}

	// Pre-create one collection by name ("Netflix Originals") without TemplateID
	// to verify case-insensitive name deduplication during preview and apply.
	preCreated, err := e.svc.CreateSpec(e.ctx, core.CollectionSpec{
		Name:      "  netflix   originals ",
		MediaType: collections.MediaMixed,
		Sources:   []core.CollectionSource{{Kind: "catalog"}},
	})
	if err != nil {
		t.Fatalf("CreateSpec preCreated: %v", err)
	}

	existing, err := e.svc.ListSpecs(e.ctx)
	if err != nil {
		t.Fatalf("ListSpecs: %v", err)
	}
	preview, err := collections.PreviewTemplateBundle(templates, existing, "streaming-originals")
	if err != nil {
		t.Fatalf("PreviewTemplateBundle: %v", err)
	}
	if preview.WillSkip != 1 || preview.WillCreate != len(preview.Bundle.TemplateIDs)-1 {
		t.Fatalf("expected 1 skipped and %d to create, got skip=%d create=%d", len(preview.Bundle.TemplateIDs)-1, preview.WillSkip, preview.WillCreate)
	}
	var foundSkip bool
	for _, item := range preview.Items {
		if item.TemplateID == "streaming-netflix-originals" {
			foundSkip = true
			if item.Action != "skip_existing" || item.ExistingID != preCreated.ID {
				t.Fatalf("expected streaming-netflix-originals to skip_existing onto %s, got %+v", preCreated.ID, item)
			}
		}
	}
	if !foundSkip {
		t.Fatalf("missing streaming-netflix-originals in preview items")
	}

	// Apply the bundle: creates the remaining templates.
	applied, err := collections.ApplyTemplateBundle(e.ctx, e.svc, "streaming-originals")
	if err != nil {
		t.Fatalf("ApplyTemplateBundle first run: %v", err)
	}
	if applied.BundleID != "streaming-originals" || applied.CreatedCount != preview.WillCreate || applied.SkippedCount != 1 || len(applied.Created) != preview.WillCreate || len(applied.Skipped) != 1 {
		t.Fatalf("unexpected apply result: %+v", applied)
	}

	// Applying the same bundle a second time is a no-op (all skip_existing).
	secondApply, err := collections.ApplyTemplateBundle(e.ctx, e.svc, "streaming-originals")
	if err != nil {
		t.Fatalf("ApplyTemplateBundle second run: %v", err)
	}
	if secondApply.CreatedCount != 0 || secondApply.SkippedCount != len(preview.Bundle.TemplateIDs) || len(secondApply.Skipped) != len(preview.Bundle.TemplateIDs) {
		t.Fatalf("expected second apply to create 0 and skip %d, got create=%d skip=%d",
			len(preview.Bundle.TemplateIDs), secondApply.CreatedCount, secondApply.SkippedCount)
	}
}
