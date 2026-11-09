package app

import (
	"context"
	"database/sql"
	"errors"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/viewerpolicy"
)

// publishPolicyConfig is the config store's policy bump: it records what the
// new catalogue rules read and advances the epoch in one transaction.
func (a *App) publishPolicyConfig() error {
	in := viewerpolicy.Inputs{}
	viewerpolicy.RulesInputs(in, a.cfg.Get().Catalog.Exclusions)
	return viewerpolicy.PublishConfig(context.Background(), a.db, in)
}

// rebuildPolicyInputs replaces the viewer-policy inputs from durable state at
// startup, once the catalogue has installed its rules. The policy lease
// keeps a local publication out; a remote one changes the shared version,
// and then the config rows are left for that node's publication to extend.
func (a *App) rebuildPolicyInputs(ctx context.Context) error {
	release, err := a.cfg.AcquirePolicy(ctx)
	if err != nil {
		return err
	}
	defer release()
	cfg := a.cfg.Get().Catalog
	global, installed := a.catalog.PolicyInputs(cfg)
	clustered, version := a.cfg.Clustered(), a.cfg.SharedVersion()
	config := viewerpolicy.Rebuild{Source: viewerpolicy.SourceConfig}
	if installed {
		config.Read = func(ctx context.Context, tx *sql.Tx) (viewerpolicy.Inputs, error) {
			if clustered {
				if err := viewerpolicy.CheckSharedConfigVersion(ctx, tx, version); err != nil {
					return nil, err
				}
			}
			return global, nil
		}
	}
	err = viewerpolicy.RebuildInputs(ctx, a.db, config,
		viewerpolicy.Rebuild{Source: viewerpolicy.SourceSafety, Read: safety.ReadPolicyInputs},
		viewerpolicy.Rebuild{Source: viewerpolicy.SourceRules, Read: catalog.ReadViewerRulesInputs})
	if errors.Is(err, viewerpolicy.ErrChanged) {
		// Another node published settings meanwhile; its rows stand and the
		// conservative seed stays until the next start.
		return nil
	}
	return err
}
