package app

import (
	"errors"
	"path/filepath"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// Private execution owns a separate manager/session namespace and output tree.
// No local playback failover callbacks or adaptive/enhancement mutation can
// change a fenced remote plan after its authority checks.
func (a *App) buildPrivateEncoder(cfg config.Config) error {
	p := a.deployment
	if p == nil || p.settings.Roles.FullServer() || !p.settings.Roles.Has(config.RoleTranscode) || p.encoder != nil {
		return nil
	}
	tc := privateEncoderConfig(cfg, p.settings.Capacity)
	manager, err := transcode.New(tc, a.log.With("component", "private-encoder"), transcode.WithRuntimeDir(filepath.Join(a.opts.DataDir, "tools")))
	if err != nil {
		return errors.New("private transcode role requires a working encoder")
	}
	p.encoder = manager
	a.closers = append(a.closers, func() { _ = manager.Close() })
	return nil
}

func privateEncoderConfig(cfg config.Config, capacity int) config.Transcode {
	tc := cfg.Transcode
	tc.ABRLadder = false
	tc.ABRLadderUnderCopy = false
	tc.SpikeGovernor = false
	tc.RTXVideo.Mode = "off"
	tc.MaxTranscodes = capacity
	tc.TempDir = filepath.Join(cfg.Transcode.TempDir, "private-worker")

	return tc
}
