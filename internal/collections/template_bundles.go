package collections

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
)

// TemplateBundle groups a curated set of collection template IDs that an owner
// can preview (dry-run) or apply in one click from the Collection Studio or
// Setup Wizard.
type TemplateBundle struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category,omitempty"`
	TemplateIDs []string `json:"templateIds"`
}

// TemplateBundlePreviewItem describes how applying a bundle will handle one
// member template against the server's existing collections.
type TemplateBundlePreviewItem struct {
	TemplateID   string `json:"templateId"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Category     string `json:"category"`
	MediaType    string `json:"mediaType,omitempty"`
	Action       string `json:"action"` // "create" or "skip_existing"
	ExistingID   string `json:"existingId,omitempty"`
	ExistingName string `json:"existingName,omitempty"`
}

// TemplateBundlePreview is the dry-run summary returned before applying a
// template bundle.
type TemplateBundlePreview struct {
	Bundle     TemplateBundle              `json:"bundle"`
	Items      []TemplateBundlePreviewItem `json:"items"`
	WillCreate int                         `json:"willCreate"`
	WillSkip   int                         `json:"willSkip"`
}

// TemplateBundleApplyResult reports the collections created and skipped when a
// bundle is applied.
type TemplateBundleApplyResult struct {
	BundleID     string                      `json:"bundleId"`
	Bundle       TemplateBundle              `json:"bundle"`
	Items        []TemplateBundlePreviewItem `json:"items"`
	Created      []core.CollectionSpec       `json:"created"`
	Skipped      []TemplateBundlePreviewItem `json:"skipped"`
	CreatedCount int                         `json:"createdCount"`
	SkippedCount int                         `json:"skippedCount"`
}

// BundleStudio is the subset of the Collection Studio service needed to
// preview and apply template bundles. Both *collections.Service and
// admin.CollectionStudio implement it automatically.
type BundleStudio interface {
	ListSpecs(ctx context.Context) ([]core.CollectionSpec, error)
	CreateSpec(ctx context.Context, spec core.CollectionSpec) (*core.CollectionSpec, error)
	Templates() []core.CollectionTemplate
}

var templateBundleDefs = []TemplateBundle{
	{
		ID:          "core-defaults",
		Name:        extend.T("Core Defaults").Source,
		Description: extend.T("Essential trending, popular, top-rated, IMDb Top 250, and new-release shelves across movies and series.").Source,
		Category:    extend.T("Starter").Source,
		TemplateIDs: []string{
			"top-trending",
			"top-popular-movies",
			"top-popular-shows",
			"acclaimed-movies",
			"acclaimed-series",
			"top-250-movies",
			"top-100-series",
			"imdb-top-250-movies",
			"imdb-top-250-shows",
			"new-releases",
			"new-series",
			"recently-added",
		},
	},
	{
		ID:          "streaming-originals",
		Name:        extend.T("Streaming Originals").Source,
		Description: extend.T("Curated original films and series from Apple TV+, Disney+, HBO Max, Hulu, Netflix, Peacock, Prime Video, and Shudder.").Source,
		Category:    extend.T("Streaming").Source,
		TemplateIDs: []string{
			"streaming-apple-tv-originals",
			"streaming-disney-plus-originals",
			"streaming-max-originals",
			"streaming-hulu-originals",
			"streaming-netflix-originals",
			"streaming-peacock-originals",
			"streaming-prime-video-originals",
			"streaming-shudder",
		},
	},
	{
		ID:          "awards-and-yearly",
		Name:        extend.T("Awards & Yearly Picks").Source,
		Description: extend.T("Academy Award and Golden Globe winners, Rotten Tomatoes yearly best-of lists, IMDb MovieMeter, Criterion, and A24.").Source,
		Category:    extend.T("Editorial").Source,
		TemplateIDs: []string{
			"awards-oscar-winners",
			"awards-golden-globes-winners",
			"best-of-2023",
			"best-of-2024",
			"best-of-2025",
			"imdb-moviemeter-top-100",
			"criterion-collection",
			"a24",
			"ifc-films",
		},
	},
	{
		ID:          "seasonal",
		Name:        extend.T("Seasonal & Holidays").Source,
		Description: extend.T("Holiday, observance, and heritage-month shelves for Halloween, Christmas, Valentine's Day, Thanksgiving, and New Year's Eve.").Source,
		Category:    extend.T("Seasonal").Source,
		TemplateIDs: []string{
			"seasonal-halloween",
			"seasonal-halloween-family",
			"seasonal-christmas",
			"seasonal-valentines",
			"seasonal-easter",
			"seasonal-thanksgiving",
			"seasonal-new-year",
			"seasonal-aapi-heritage",
			"seasonal-latinx-heritage",
			"seasonal-pride-month",
		},
	},
	{
		ID:          "franchises",
		Name:        extend.T("Franchises & Universes").Source,
		Description: extend.T("Major movie sagas and studio universes in release order, from Star Wars and Middle-earth to the MCU, James Bond, and Dune.").Source,
		Category:    extend.T("Franchises").Source,
		TemplateIDs: []string{
			"star-wars",
			"middle-earth",
			"wizarding-world",
			"marvel-cinematic-universe",
			"james-bond",
			"jurassic",
			"fast-and-furious",
			"mission-impossible",
			"pirates-of-the-caribbean",
			"monsterverse",
			"john-wick",
			"the-matrix",
			"dune-saga",
			"pixar",
			"studio-ghibli",
		},
	},
}

// TemplateBundles returns a copy of all curated collection template bundles.
func TemplateBundles() []TemplateBundle {
	out := make([]TemplateBundle, 0, len(templateBundleDefs))
	for _, b := range templateBundleDefs {
		ids := make([]string, len(b.TemplateIDs))
		copy(ids, b.TemplateIDs)
		b.TemplateIDs = ids
		out = append(out, b)
	}
	return out
}

// LookupTemplateBundle finds a bundle by ID.
func LookupTemplateBundle(id string) (TemplateBundle, bool) {
	id = strings.TrimSpace(id)
	for _, b := range templateBundleDefs {
		if b.ID == id {
			ids := make([]string, len(b.TemplateIDs))
			copy(ids, b.TemplateIDs)
			b.TemplateIDs = ids
			return b, true
		}
	}
	return TemplateBundle{}, false
}

func bundleNameKey(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " "))
}

// PreviewTemplateBundle computes a dry-run preview of which templates in
// bundleID would be created vs. skipped because a matching collection already
// exists (by TemplateID or case-insensitive normalized name).
func PreviewTemplateBundle(templates []core.CollectionTemplate, existing []core.CollectionSpec, bundleID string) (*TemplateBundlePreview, error) {
	bundle, ok := LookupTemplateBundle(bundleID)
	if !ok {
		return nil, core.ErrNotFound
	}
	byID := make(map[string]core.CollectionTemplate, len(templates))
	for _, t := range templates {
		byID[t.ID] = t
	}
	existingByTpl := make(map[string]core.CollectionSpec, len(existing))
	existingByName := make(map[string]core.CollectionSpec, len(existing))
	for _, s := range existing {
		if tid := strings.TrimSpace(s.TemplateID); tid != "" {
			if _, seen := existingByTpl[tid]; !seen {
				existingByTpl[tid] = s
			}
		}
		if nk := bundleNameKey(s.Name); nk != "" {
			if _, seen := existingByName[nk]; !seen {
				existingByName[nk] = s
			}
		}
	}

	preview := &TemplateBundlePreview{
		Bundle: bundle,
		Items:  make([]TemplateBundlePreviewItem, 0, len(bundle.TemplateIDs)),
	}
	seenInBundle := make(map[string]bool, len(bundle.TemplateIDs))
	for _, tid := range bundle.TemplateIDs {
		if seenInBundle[tid] {
			continue
		}
		seenInBundle[tid] = true
		tpl, found := byID[tid]
		if !found {
			continue
		}
		specName := strings.TrimSpace(tpl.Spec.Name)
		if specName == "" {
			specName = strings.TrimSpace(tpl.Name)
		}
		item := TemplateBundlePreviewItem{
			TemplateID:  tpl.ID,
			Name:        specName,
			Description: tpl.Description,
			Category:    tpl.Category,
			MediaType:   tpl.Spec.MediaType,
			Action:      "create",
		}
		if match, ok := existingByTpl[tpl.ID]; ok {
			item.Action = "skip_existing"
			item.ExistingID = match.ID
			item.ExistingName = match.Name
			preview.WillSkip++
		} else if match, ok := existingByName[bundleNameKey(specName)]; ok {
			item.Action = "skip_existing"
			item.ExistingID = match.ID
			item.ExistingName = match.Name
			preview.WillSkip++
		} else if match, ok := existingByName[bundleNameKey(tpl.Name)]; ok {
			item.Action = "skip_existing"
			item.ExistingID = match.ID
			item.ExistingName = match.Name
			preview.WillSkip++
		} else {
			preview.WillCreate++
		}
		preview.Items = append(preview.Items, item)
	}
	return preview, nil
}

// ApplyTemplateBundle creates every non-duplicate collection in bundleID via
// studio.CreateSpec and returns the created specs alongside the per-template
// resolution summary.
func ApplyTemplateBundle(ctx context.Context, studio BundleStudio, bundleID string) (*TemplateBundleApplyResult, error) {
	existing, err := studio.ListSpecs(ctx)
	if err != nil {
		return nil, err
	}
	templates := studio.Templates()
	preview, err := PreviewTemplateBundle(templates, existing, bundleID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]core.CollectionTemplate, len(templates))
	for _, t := range templates {
		byID[t.ID] = t
	}

	result := &TemplateBundleApplyResult{
		BundleID:     preview.Bundle.ID,
		Bundle:       preview.Bundle,
		Items:        make([]TemplateBundlePreviewItem, 0, len(preview.Items)),
		Created:      make([]core.CollectionSpec, 0, preview.WillCreate),
		Skipped:      make([]TemplateBundlePreviewItem, 0, preview.WillSkip),
		SkippedCount: preview.WillSkip,
	}
	for _, item := range preview.Items {
		if item.Action != "create" {
			result.Items = append(result.Items, item)
			result.Skipped = append(result.Skipped, item)
			continue
		}
		tpl, ok := byID[item.TemplateID]
		if !ok {
			continue
		}
		spec := cloneSpec(tpl.Spec)
		spec.TemplateID = tpl.ID
		if strings.TrimSpace(spec.Name) == "" {
			spec.Name = tpl.Name
		}
		if strings.TrimSpace(spec.Description) == "" {
			spec.Description = tpl.Description
		}
		created, err := studio.CreateSpec(ctx, spec)
		if err != nil {
			return nil, err
		}
		if created != nil {
			result.Created = append(result.Created, *created)
			item.ExistingID = created.ID
			item.ExistingName = created.Name
		}
		result.CreatedCount++
		result.Items = append(result.Items, item)
	}
	return result, nil
}
