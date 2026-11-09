//go:build !plexparity && !scorecard

package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/personalize"
	"github.com/braedonsaunders/ether/internal/tmdb"
	"time"
)

// These hooks are inert in production builds. The plexparity build tag wires
// deterministic dependencies only into disposable differential-test servers.
func parityClock() func() time.Time                  { return nil }
func parityRefreshEvery() time.Duration              { return 0 }
func parityRankerOptions() personalize.RankerOptions { return personalize.RankerOptions{} }
func parityCatalogueOptions() []catalog.Option       { return nil }
func parityTMDBOptions() []tmdb.Option               { return nil }

func parityCollectionOptions() []collections.Option { return nil }

func parityWarmPersonalization(context.Context, *App) error { return nil }

func parityPlexTVBaseURL() string { return "" }

func parityRunCatalogueWorkers() bool { return true }

func parityRunRegistrar() bool { return true }

func parityConfigureTMDB(*tmdb.Client) {}

// parityShowcase adds the shipped decade channels outside the parity harness
// and this package's tests (testShowcaseOff).
func parityShowcase() bool { return !testShowcaseOff }

// testShowcaseOff keeps the shipped decade channels out of this package's
// tests: their startup seed and refresh write livetv_channels, a viewer
// policy input, so a test's in-flight snapshot would see the policy move
// at a moment it did not choose.
var testShowcaseOff bool
