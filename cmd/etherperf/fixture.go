package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	_ "github.com/braedonsaunders/ether/internal/app" // every migration
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/perfharness"
	"github.com/braedonsaunders/ether/internal/store"
)

// fixtureScales are the gate's synthetic libraries by name.
var fixtureScales = map[string]perfharness.Scale{
	"quick":  perfharness.SmallScale.Divide(10),
	"small":  perfharness.SmallScale,
	"large":  perfharness.LargeScale,
	"xsmall": perfharness.XLargeScale.Divide(10),
	"xlarge": perfharness.XLargeScale,
}

// runFixture writes the gate's synthetic household into a new data
// directory, which `etherperf real -source` replays like a real one.
func runFixture(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("fixture", flag.ExitOnError)
	scaleName := f.String("scale", "large", "quick, small, large (the per-push gate's pair), xsmall or xlarge (the nightly pair)")
	out := f.String("out", "", "new data directory to create")
	_ = f.Parse(args)
	scale, ok := fixtureScales[*scaleName]
	if !ok || *out == "" {
		return fmt.Errorf("usage: etherperf fixture -scale quick|small|large|xsmall|xlarge -out DIR")
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("%s exists", *out)
	}
	start := time.Now()
	cfg, err := config.Open(*out)
	if err != nil {
		return err
	}
	if err := cfg.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Frontends = map[string]config.Frontend{"plex": {Enabled: true}, "jellyfin": {Enabled: true}, "emby": {Enabled: true}, "silo": {Enabled: true}, "stremio": {Enabled: true}}
		return nil
	}); err != nil {
		return err
	}
	db, err := store.Open(ctx, *out)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := perfharness.Generate(ctx, db, scale, time.Now()); err != nil {
		return err
	}
	fmt.Printf("%s: %d titles, %d catalogue rows in %s\n", *out, scale.Titles(), scale.Rows(), time.Since(start).Round(time.Second))
	return nil
}
