package subtitles

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func automaticFixture() AutomaticPlayback {
	return AutomaticPlayback{Title: episode, Viewer: 1, Playing: true, Languages: []string{"de", "fr"}, UnderstoodAudio: []string{"de"}, SelectedAudio: "en", Preferences: Preferences{Mode: "always", Style: "normal"}, Release: "Show.S01E02.1080p.WEB"}
}

func TestAutomaticEligibility(t *testing.T) {
	base := automaticFixture()
	for _, tc := range []struct {
		name   string
		change func(*AutomaticPlayback)
		want   string
	}{
		{"always", func(*AutomaticPlayback) {}, "de"},
		{"prepare", func(p *AutomaticPlayback) { p.Playing = false }, ""},
		{"manual", func(p *AutomaticPlayback) { p.Manual = true }, ""},
		{"off", func(p *AutomaticPlayback) { p.Preferences.Mode = "off" }, ""},
		{"forced", func(p *AutomaticPlayback) { p.Preferences.Mode = "forced-only" }, ""},
		{"foreign", func(p *AutomaticPlayback) { p.Preferences.Mode = "foreign-only" }, "de"},
		{"understood", func(p *AutomaticPlayback) { p.Preferences.Mode = "foreign-only"; p.SelectedAudio = "de" }, ""},
		{"unknown", func(p *AutomaticPlayback) { p.Preferences.Mode = "foreign-only"; p.SelectedAudio = "" }, ""},
		{"no preferences", func(p *AutomaticPlayback) { p.Languages = nil }, ""},
		{"covered", func(p *AutomaticPlayback) { p.Streams = []core.StreamInfo{{Type: "subtitle", Language: "de"}} }, "fr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.change(&p)
			if got := NeededAutomaticLanguage(p); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestAutomaticDownloadCacheAndManualVeto(t *testing.T) {
	ctx := context.Background()
	f := newOSFake(t)
	svc := newSvc(t, f, config.OpenSubtitles{}, t.TempDir())
	budget := NewAutomaticBudget(store.OpenTest(t))
	p := automaticFixture()
	var attached core.StreamInfo
	target := AutomaticTarget{Current: func(context.Context) (AutomaticPlayback, error) { return p, nil }, Attach: func(_ context.Context, _ AutomaticPlayback, st core.StreamInfo) error { attached = st; return nil }}
	if err := svc.RunAutomatic(ctx, budget, target); err != nil {
		t.Fatal(err)
	}
	if attached.Language != "de" || f.downloads.Load() != 1 || f.searches.Load() != 1 {
		t.Fatal(attached, f.downloads.Load(), f.searches.Load())
	}
	r, _, err := svc.OpenFormat(attached.ExternalURL, "vtt")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(r)
	r.Close()
	if err != nil || !strings.Contains(string(raw), "Hallo") {
		t.Fatal(string(raw), err)
	}
	if err = svc.RunAutomatic(ctx, NewAutomaticBudget(budget.db), target); err != nil {
		t.Fatal(err)
	}
	if f.downloads.Load() != 1 || f.searches.Load() != 1 {
		t.Fatal("cache consumed provider")
	}
	// A different viewer's current manual-off choice prevents even search.
	p.Viewer = 2
	p.Manual = true
	if err = svc.RunAutomatic(ctx, budget, target); err != nil {
		t.Fatal(err)
	}
	if f.searches.Load() != 1 {
		t.Fatal("manual choice searched")
	}
}

func TestAutomaticUnknownOutcomeNeverRetriesAndManualStillWorks(t *testing.T) {
	ctx := context.Background()
	f := newOSFake(t)
	f.quota = true
	svc := newSvc(t, f, config.OpenSubtitles{}, t.TempDir())
	budget := NewAutomaticBudget(store.OpenTest(t))
	p := automaticFixture()
	target := AutomaticTarget{Current: func(context.Context) (AutomaticPlayback, error) { return p, nil }, Attach: func(context.Context, AutomaticPlayback, core.StreamInfo) error {
		t.Fatal("attached quota failure")
		return nil
	}}
	if err := svc.RunAutomatic(ctx, budget, target); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	if err := svc.RunAutomatic(ctx, NewAutomaticBudget(budget.db), target); err != nil {
		t.Fatal(err)
	}
	if f.downloads.Load() != 1 {
		t.Fatal("quota retried", f.downloads.Load())
	}
	status, err := budget.Status(ctx, p.Viewer, p.Title.Key)
	if err != nil || !status.Spent {
		t.Fatal(status, err)
	}
	// Manual search stays available even while the automatic provider pause is active.
	if _, err := svc.SearchOnDemand(ctx, p.Title, "fr", p.Release); err != nil {
		t.Fatal(err)
	}
	if f.searches.Load() != 2 {
		t.Fatal("manual search suppressed")
	}
}
