package app

import (
	"github.com/braedonsaunders/ether/internal/config"
	"path/filepath"
	"testing"
)

func TestPrivateEncoderPolicyIsolation(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Transcode.FastStart = true
	cfg.Transcode.ABRLadder = true
	cfg.Transcode.ABRLadderUnderCopy = true
	cfg.Transcode.SpikeGovernor = true
	cfg.Transcode.RTXVideo.Mode = "auto"
	tc := privateEncoderConfig(cfg, 2)
	if !tc.FastStart || tc.ABRLadder || tc.ABRLadderUnderCopy || tc.SpikeGovernor || tc.RTXVideo.Mode != "off" {
		t.Fatal("private plan permits autonomous mutation")
	}
	if tc.MaxTranscodes != 2 || tc.TempDir != filepath.Join(cfg.Transcode.TempDir, "private-worker") {
		t.Fatal("private execution namespace/capacity not isolated")
	}
	if !cfg.Transcode.ABRLadder || cfg.Transcode.RTXVideo.Mode != "auto" {
		t.Fatal("full-server encoder policy changed")
	}
}
